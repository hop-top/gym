package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/fsutil"
	"hop.top/gym/internal/ide"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
)

// Request holds installation inputs.
type Request struct {
	Entry    registry.SkillEntry
	Source   string
	Scope    string // global|project
	PathHint string
	// AgentPatterns filters install paths by name/path. Use "!" prefix to exclude.
	AgentPatterns []string
	Config        config.Config
	ProjectRoot   string
	CWD           string

	// Gate is an optional pre-flight policy hook invoked before any
	// filesystem write. The cli layer wires this to publish
	// gym.skill.install.requested on the bus and block on the guard's
	// policy.guard.install.evaluated reply. Returning a
	// non-nil error short-circuits the install with that error.
	//
	// Gate receives the resolved install path so policies that gate
	// on path-allowlist see the real target. install.go calls Gate
	// once per install path (so multi-path installs evaluate each).
	Gate GateFunc

	// PreviousDataClass is the installed version's data class on
	// upgrade; a higher new class raises data-class-escalated.
	PreviousDataClass string

	// ApprovalsPath overrides the user-level approvals file. Empty
	// uses the default under the gym user config dir.
	ApprovalsPath string
}

// GateFunc is the optional pre-install policy hook. paths is the list
// of resolved install destinations (one per InstallSkillMulti
// iteration; one for InstallSkill). scan carries the staging-dir scan
// (postinstall, shell hook, secret scan, biggest binary) collected
// before the gate runs so CEL policies that key on
// payload.postinstall / payload.shell_hook / payload.scan /
// payload.largest_binary_bytes can fire on real install metadata.
// scan is the zero value for trivial source dirs. adm carries the
// admission facts (digest, tool coverage, data class, approval) that
// the admission-* policies adjudicate. Returning a non-nil error
// aborts the install with that error.
type GateFunc func(ctx context.Context, entry registry.SkillEntry, scope string, paths []string, scan ScanResult, adm AdmissionResult) error

// ScanResult captures what the install-pipeline scanner extracted from
// the staging directory before publishing gym.skill.install.requested.
// Empty fields mean "nothing found" — guard policies should treat them
// as benign.
type ScanResult struct {
	Postinstall        string
	ShellHook          string
	Scan               string
	LargestBinaryBytes int64
}

func defaultValidator(v config.ValidatorConfig) config.ValidatorConfig {
	if len(v.Command) == 0 {
		v.Command = config.DefaultValidatorCommand()
	}
	if v.TimeoutSeconds <= 0 {
		v.TimeoutSeconds = 20
	}
	return v
}

func replacePathTokens(parts []string, path string) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.ReplaceAll(p, "{path}", path)
	}
	return out
}

// ValidateSkillPackage runs configured validator command (default: skills-ref validate) against path.
func ValidateSkillPackage(path string, v config.ValidatorConfig) error {
	v = defaultValidator(v)
	cmdLine := replacePathTokens(v.Command, path)
	if len(cmdLine) == 0 {
		return &ValidatorError{Err: errors.New("validator command is empty")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(v.TimeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdLine[0], cmdLine[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &ValidatorError{Err: err, Output: strings.TrimSpace(string(out))}
	}
	return nil
}

func nameFromSource(source string) string {
	s := strings.TrimSuffix(filepath.Base(source), ".git")
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == string(filepath.Separator) {
		return "skill"
	}
	return s
}

func resolveInstallPaths(req Request) ([]config.InstallPath, error) {
	paths := make([]config.InstallPath, 0)
	for _, p := range req.Config.InstallPaths {
		if p.Scope == req.Scope {
			paths = append(paths, p)
		}
	}

	if len(paths) == 0 {
		home := os.Getenv("HOME")
		if req.Scope == "global" {
			detected := ide.DetectExistingGlobalPaths(home)
			if len(detected) > 0 {
				for _, p := range detected {
					paths = append(paths, config.InstallPath{Name: p.Name, Path: p.Path, Scope: "global"})
				}
			} else {
				defaultPath := filepath.Join(home, ".claude", "skills")
				paths = append(paths, config.InstallPath{Name: "claude-global", Path: defaultPath, Scope: "global"})
			}
		} else {
			root := req.ProjectRoot
			if root == "" {
				root = req.CWD
			}
			dflt := filepath.Join(root, ".gym", "skills")
			// A repository can make .gym/skills a symlink out of its tree.
			if abs, err := filepath.Abs(root); err != nil || !config.ContainedIn(abs, dflt) {
				return nil, fmt.Errorf("install path %s escapes project %s", dflt, root)
			}
			paths = append(paths, config.InstallPath{
				Name:  "gym-project",
				Path:  dflt,
				Scope: "project",
			})
		}
	}

	if req.PathHint != "" {
		for _, p := range paths {
			if p.Name == req.PathHint || p.Path == req.PathHint {
				return []config.InstallPath{p}, nil
			}
		}
		if pathLike(req.PathHint) {
			p := normalizeHintPath(req.PathHint, req.Scope, req.ProjectRoot, req.CWD)
			return []config.InstallPath{{Name: filepath.Base(p) + "-" + req.Scope, Path: p, Scope: req.Scope}}, nil
		}
		return nil, fmt.Errorf("path hint %q did not match configured %s paths", req.PathHint, req.Scope)
	}

	filtered := filterPathsByAgentPatterns(paths, req.AgentPatterns)
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no install paths matched for scope %q", req.Scope)
	}
	return filtered, nil
}

func pathLike(s string) bool {
	return strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") || strings.Contains(s, "/") || strings.Contains(s, "\\")
}

func normalizeHintPath(pathHint, scope, projectRoot, cwd string) string {
	p := pathHint
	if strings.HasPrefix(p, "~/") {
		home := os.Getenv("HOME")
		p = filepath.Join(home, p[2:])
	}
	if scope == "project" && !filepath.IsAbs(p) {
		root := projectRoot
		if root == "" {
			root = cwd
		}
		p = filepath.Join(root, p)
	}
	return p
}

func matchPathPattern(pat string, path config.InstallPath) bool {
	if pat == "" {
		return false
	}
	if ok, _ := filepath.Match(pat, path.Name); ok {
		return true
	}
	if ok, _ := filepath.Match(pat, path.Path); ok {
		return true
	}
	if pat == path.Name || pat == path.Path {
		return true
	}
	if !strings.ContainsAny(pat, "*?[]") && !strings.Contains(pat, string(filepath.Separator)) {
		if strings.HasPrefix(path.Name, pat+"-") {
			return true
		}
	}
	return false
}

func filterPathsByAgentPatterns(paths []config.InstallPath, patterns []string) []config.InstallPath {
	if len(patterns) == 0 {
		return paths
	}
	includes := []string{}
	excludes := []string{}
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			excludes = append(excludes, strings.TrimPrefix(p, "!"))
		} else {
			includes = append(includes, p)
		}
	}

	out := make([]config.InstallPath, 0, len(paths))
	for _, p := range paths {
		if len(includes) > 0 {
			matched := false
			for _, inc := range includes {
				if matchPathPattern(inc, p) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		excluded := false
		for _, exc := range excludes {
			if matchPathPattern(exc, p) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		out = append(out, p)
	}
	return out
}

// InstallSkill installs a skill package after validation.
func InstallSkill(ctx context.Context, req Request) (state.InstalledSkill, error) {
	if req.Scope == "" {
		req.Scope = "global"
	}
	if req.Scope != "global" && req.Scope != "project" {
		return state.InstalledSkill{}, errors.New("scope must be global or project")
	}

	entry := req.Entry
	if entry.SourceURL == "" {
		entry.SourceURL = req.Source
	}
	if entry.Name == "" {
		entry.Name = nameFromSource(entry.SourceURL)
	}
	if entry.Version == "" {
		entry.Version = "0.0.0"
	}
	if entry.SourceURL == "" {
		return state.InstalledSkill{}, fmt.Errorf("source URL is required")
	}

	if err := CheckSourceURL(entry.SourceURL); err != nil {
		return state.InstalledSkill{}, err
	}
	root, cleanup, err := stageSource(ctx, entry.SourceURL)
	if err != nil {
		return state.InstalledSkill{}, err
	}
	defer cleanup()

	skillPath := root
	if entry.Path != "" {
		skillPath = filepath.Join(root, entry.Path)
	}
	if err := ValidateSkillPackage(skillPath, req.Config.Validator); err != nil {
		return state.InstalledSkill{}, err
	}
	adm, err := admitStaged(req, entry, skillPath)
	if err != nil {
		return state.InstalledSkill{}, err
	}

	installPaths, err := resolveInstallPaths(req)
	if err != nil {
		return state.InstalledSkill{}, err
	}
	if len(installPaths) != 1 {
		return state.InstalledSkill{}, fmt.Errorf("expected one install path, got %d", len(installPaths))
	}
	if req.Gate != nil {
		scan := ScanStaging(skillPath)
		if err := req.Gate(ctx, entry, req.Scope, []string{installPaths[0].Path}, scan, adm); err != nil {
			return state.InstalledSkill{}, err
		}
	}
	return installSkillToPath(skillPath, entry, installPaths[0], adm)
}

// InstallSkillMulti installs a skill into all resolved paths.
func InstallSkillMulti(ctx context.Context, req Request) ([]state.InstalledSkill, error) {
	if req.Scope == "" {
		req.Scope = "global"
	}
	if req.Scope != "global" && req.Scope != "project" {
		return nil, errors.New("scope must be global or project")
	}

	entry := req.Entry
	if entry.SourceURL == "" {
		entry.SourceURL = req.Source
	}
	if entry.Name == "" {
		entry.Name = nameFromSource(entry.SourceURL)
	}
	if entry.Version == "" {
		entry.Version = "0.0.0"
	}
	if entry.SourceURL == "" {
		return nil, fmt.Errorf("source URL is required")
	}

	if err := CheckSourceURL(entry.SourceURL); err != nil {
		return nil, err
	}
	root, cleanup, err := stageSource(ctx, entry.SourceURL)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	skillPath := root
	if entry.Path != "" {
		skillPath = filepath.Join(root, entry.Path)
	}
	if err := ValidateSkillPackage(skillPath, req.Config.Validator); err != nil {
		return nil, err
	}
	adm, err := admitStaged(req, entry, skillPath)
	if err != nil {
		return nil, err
	}

	installPaths, err := resolveInstallPaths(req)
	if err != nil {
		return nil, err
	}

	if req.Gate != nil {
		paths := make([]string, len(installPaths))
		for i, p := range installPaths {
			paths[i] = p.Path
		}
		scan := ScanStaging(skillPath)
		if err := req.Gate(ctx, entry, req.Scope, paths, scan, adm); err != nil {
			return nil, err
		}
	}

	installed := make([]state.InstalledSkill, 0, len(installPaths))
	for _, p := range installPaths {
		item, err := installSkillToPath(skillPath, entry, p, adm)
		if err != nil {
			return nil, err
		}
		installed = append(installed, item)
	}
	return installed, nil
}

// admitStaged runs the admission stage over the staged skill.
func admitStaged(req Request, entry registry.SkillEntry, skillPath string) (AdmissionResult, error) {
	return Admit(AdmissionInput{
		SkillPath:         skillPath,
		Entry:             entry,
		Admission:         req.Config.Admission,
		PreviousDataClass: req.PreviousDataClass,
		ApprovalsPath:     req.ApprovalsPath,
	})
}

// skipGitDir skips .git directories below the copy root, the same ones
// Digest skips.
func skipGitDir(rel string, d fs.DirEntry) bool {
	return rel != "." && d.IsDir() && d.Name() == ".git"
}

func installSkillToPath(skillPath string, entry registry.SkillEntry, installPath config.InstallPath, adm AdmissionResult) (state.InstalledSkill, error) {
	// A registry-supplied name must not steer the copy or the RemoveAll
	// out of the install path.
	if !ValidSkillName(entry.Name) {
		return state.InstalledSkill{}, fmt.Errorf("invalid skill name %q", entry.Name)
	}
	if err := os.MkdirAll(installPath.Path, 0o755); err != nil {
		return state.InstalledSkill{}, err
	}
	dest := filepath.Join(installPath.Path, entry.Name)
	_ = os.RemoveAll(dest)
	// .git is skipped by the digest, so it must not be installed either:
	// installed bytes equal hashed bytes.
	if err := fsutil.CopyDir(skillPath, dest, skipGitDir); err != nil {
		return state.InstalledSkill{}, err
	}
	// The source may be a live local directory mutated since it was
	// hashed: verify what actually landed on disk.
	if got, err := Digest(dest); err != nil || got != adm.Digest {
		_ = os.RemoveAll(dest)
		if err != nil {
			return state.InstalledSkill{}, err
		}
		return state.InstalledSkill{}, admissionErrorf(ReasonDigestMismatch,
			"installed copy %s differs from admitted %s", got, adm.Digest)
	}
	return state.InstalledSkill{
		Name:        entry.Name,
		Version:     entry.Version,
		SourceURL:   RedactURL(entry.SourceURL),
		Repository:  entry.Repository,
		InstalledAt: time.Now().UTC(),
		InstallPath: dest,
		Digest:      adm.Digest,
		DataClass:   adm.DataClass,
	}, nil
}

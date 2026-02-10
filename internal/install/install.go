package install

import (
	"context"
	"errors"
	"fmt"
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
}

func defaultValidator(v config.ValidatorConfig) config.ValidatorConfig {
	if len(v.Command) == 0 {
		v.Command = []string{"skills-ref", "test", "{path}"}
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

// ValidateSkillPackage runs configured skills-ref test command against path.
func ValidateSkillPackage(path string, v config.ValidatorConfig) error {
	v = defaultValidator(v)
	cmdLine := replacePathTokens(v.Command, path)
	if len(cmdLine) == 0 {
		return fmt.Errorf("validator command is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(v.TimeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdLine[0], cmdLine[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("validation failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://")
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
			paths = append(paths, config.InstallPath{
				Name:  "gym-project",
				Path:  filepath.Join(root, ".gym", "skills"),
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

func stageSource(ctx context.Context, source string) (string, func(), error) {
	if strings.HasPrefix(source, "file://") {
		source = strings.TrimPrefix(source, "file://")
	}
	if st, err := os.Stat(source); err == nil && st.IsDir() {
		return source, func() {}, nil
	}
	if !looksLikeURL(source) {
		return "", nil, fmt.Errorf("unsupported source %q: expected URL or existing directory", source)
	}
	tmp, err := os.MkdirTemp("", "skills-install-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", source, tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git clone failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return tmp, cleanup, nil
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

	installPaths, err := resolveInstallPaths(req)
	if err != nil {
		return state.InstalledSkill{}, err
	}
	if len(installPaths) != 1 {
		return state.InstalledSkill{}, fmt.Errorf("expected one install path, got %d", len(installPaths))
	}
	return installSkillToPath(skillPath, entry, installPaths[0])
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

	installPaths, err := resolveInstallPaths(req)
	if err != nil {
		return nil, err
	}

	installed := make([]state.InstalledSkill, 0, len(installPaths))
	for _, p := range installPaths {
		item, err := installSkillToPath(skillPath, entry, p)
		if err != nil {
			return nil, err
		}
		installed = append(installed, item)
	}
	return installed, nil
}

func installSkillToPath(skillPath string, entry registry.SkillEntry, installPath config.InstallPath) (state.InstalledSkill, error) {
	if err := os.MkdirAll(installPath.Path, 0o755); err != nil {
		return state.InstalledSkill{}, err
	}
	dest := filepath.Join(installPath.Path, entry.Name)
	_ = os.RemoveAll(dest)
	if err := fsutil.CopyDir(skillPath, dest); err != nil {
		return state.InstalledSkill{}, err
	}
	return state.InstalledSkill{
		Name:        entry.Name,
		Version:     entry.Version,
		SourceURL:   entry.SourceURL,
		Repository:  entry.Repository,
		InstalledAt: time.Now().UTC(),
		InstallPath: dest,
	}, nil
}

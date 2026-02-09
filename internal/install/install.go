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

	"github.com/hop-top/gym/internal/config"
	"github.com/hop-top/gym/internal/fsutil"
	"github.com/hop-top/gym/internal/ide"
	"github.com/hop-top/gym/internal/registry"
	"github.com/hop-top/gym/internal/state"
)

// Request holds installation inputs.
type Request struct {
	Entry       registry.SkillEntry
	Source      string
	Scope       string // global|project
	PathHint    string
	Config      config.Config
	ProjectRoot string
	CWD         string
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

func resolveInstallPath(req Request) (config.InstallPath, error) {
	paths := make([]config.InstallPath, 0)
	for _, p := range req.Config.InstallPaths {
		if p.Scope == req.Scope {
			paths = append(paths, p)
		}
	}

	if req.PathHint != "" {
		for _, p := range paths {
			if p.Name == req.PathHint || p.Path == req.PathHint {
				return p, nil
			}
		}
		return config.InstallPath{}, fmt.Errorf("path hint %q did not match configured %s paths", req.PathHint, req.Scope)
	}

	if len(paths) > 0 {
		return paths[0], nil
	}

	home := os.Getenv("HOME")
	if req.Scope == "global" {
		detected := ide.DetectExistingGlobalPaths(home)
		if len(detected) > 0 {
			return config.InstallPath{Name: detected[0].Name, Path: detected[0].Path, Scope: "global"}, nil
		}
		defaultPath := filepath.Join(home, ".claude", "skills")
		return config.InstallPath{Name: "claude-global", Path: defaultPath, Scope: "global"}, nil
	}

	root := req.ProjectRoot
	if root == "" {
		root = req.CWD
	}
	candidates := ide.StandardProjectPathCandidates(root)
	if len(candidates) == 0 {
		return config.InstallPath{}, fmt.Errorf("no project install path candidates")
	}
	return config.InstallPath{Name: candidates[0].Name, Path: candidates[0].Path, Scope: "project"}, nil
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

	installPath, err := resolveInstallPath(req)
	if err != nil {
		return state.InstalledSkill{}, err
	}
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

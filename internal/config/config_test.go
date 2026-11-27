package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"hop.top/gym/internal/config"
)

func TestLoadMergedConfig_UserAndProjectHierarchy(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	userCfgDir := filepath.Join(home, ".config", "gym")
	if err := os.MkdirAll(userCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userConfig := `
repositories:
  - {name: curated, url: "https://example.com/curated.json", curated: true}
install_paths:
  - {name: claude-global, path: "~/.claude/skills", scope: global}
curation: {min_stars: 100}
validator: {command: [skills-ref, test, "{path}"], timeout_seconds: 15}
`
	if err := os.WriteFile(filepath.Join(userCfgDir, "config.yaml"), []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	projCfgDir := filepath.Join(projectRoot, ".gym")
	if err := os.MkdirAll(projCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := `
install_paths:
  - {name: claude-local, path: .claude/skills, scope: project}
upgrade: {mirror_global_from_project: false}
`
	if err := os.WriteFile(filepath.Join(projCfgDir, "config.yaml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	// Pin XDG_CONFIG_HOME so kit/xdg uses Linux-style paths on every OS;
	// test relies on the user config landing under <home>/.config/gym/.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	cfg, err := config.LoadMergedConfig(projectRoot, nil, nil)
	if err != nil {
		t.Fatalf("LoadMergedConfig() error = %v", err)
	}
	// Lists follow kit's layering: a later layer replaces the earlier
	// list instead of appending to it.
	if len(cfg.InstallPaths) != 1 || cfg.InstallPaths[0].Name != "claude-local" {
		t.Fatalf("InstallPaths = %#v, want project list only", cfg.InstallPaths)
	}
	if len(cfg.Repositories) != 1 || cfg.Repositories[0].Name != "curated" {
		t.Fatalf("Repositories = %#v, want user list (project sets none)", cfg.Repositories)
	}
	if cfg.Validator.TimeoutSeconds != 15 {
		t.Fatalf("TimeoutSeconds = %d, want 15 (user)", cfg.Validator.TimeoutSeconds)
	}
	if cfg.Upgrade.MirrorGlobalFromProject {
		t.Fatalf("MirrorGlobalFromProject = true, want false (project override)")
	}
	if cfg.Curation.MinStars != 100 {
		t.Fatalf("MinStars = %d, want 100", cfg.Curation.MinStars)
	}
}

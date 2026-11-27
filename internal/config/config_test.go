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
	userConfig := `{
		"repositories": [{"name": "curated", "url": "https://example.com/curated.json", "curated": true}],
		"install_paths": [{"name": "claude-global", "path": "~/.claude/skills", "scope": "global"}],
		"curation": {"min_stars": 100},
		"validator": {"command": ["skills-ref", "test", "{path}"], "timeout_seconds": 15}
	}`
	if err := os.WriteFile(filepath.Join(userCfgDir, "config.json"), []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	projCfgDir := filepath.Join(projectRoot, ".gym")
	if err := os.MkdirAll(projCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := `{
		"install_paths": [{"name": "claude-local", "path": ".claude/skills", "scope": "project"}],
		"upgrade": {"mirror_global_from_project": false}
	}`
	if err := os.WriteFile(filepath.Join(projCfgDir, "config.json"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	// Pin XDG_CONFIG_HOME so kit/xdg uses Linux-style paths on every OS;
	// test relies on the user config landing under <home>/.config/gym/.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	cfg, src, err := config.LoadMergedConfig(projectRoot)
	if err != nil {
		t.Fatalf("LoadMergedConfig() error = %v", err)
	}
	if !src.HasUser || !src.HasProject {
		t.Fatalf("LoadMergedConfig() source = %#v, want user+project", src)
	}
	if len(cfg.InstallPaths) != 2 {
		t.Fatalf("InstallPaths len = %d, want 2", len(cfg.InstallPaths))
	}
	if cfg.Upgrade.MirrorGlobalFromProject {
		t.Fatalf("MirrorGlobalFromProject = true, want false (project override)")
	}
	if cfg.Curation.MinStars != 100 {
		t.Fatalf("MinStars = %d, want 100", cfg.Curation.MinStars)
	}
}

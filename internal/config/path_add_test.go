package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"hop.top/gym/internal/config"
)

func TestAddInstallPathUserWritesToXDGConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	cwd := t.TempDir()
	cfgPath, err := config.AddInstallPath(cwd, "user", config.InstallPath{
		Name:  "claude-global",
		Path:  filepath.Join(home, ".claude", "skills"),
		Scope: "global",
	})
	if err != nil {
		t.Fatalf("AddInstallPath() error = %v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	cfg, _, err := config.LoadMergedConfig(cwd)
	if err != nil {
		t.Fatalf("LoadMergedConfig() error = %v", err)
	}
	if len(cfg.InstallPaths) != 1 || cfg.InstallPaths[0].Name != "claude-global" {
		t.Fatalf("unexpected paths: %#v", cfg.InstallPaths)
	}
}

func TestAddInstallPathProjectWritesToProjectConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()

	cfgPath, err := config.AddInstallPath(root, "project", config.InstallPath{
		Name:  "cursor-project",
		Path:  filepath.Join(root, ".cursor", "skills"),
		Scope: "project",
	})
	if err != nil {
		t.Fatalf("AddInstallPath() error = %v", err)
	}
	if want := filepath.Join(root, ".gym", "config.json"); cfgPath != want {
		t.Fatalf("cfgPath = %q, want %q", cfgPath, want)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file not written: %v", err)
	}
}

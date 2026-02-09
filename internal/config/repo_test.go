package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"hop/skills/internal/config"
)

func TestAddRepositoryUserWritesToXDGConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	cwd := t.TempDir()
	cfgPath, err := config.AddRepository(cwd, "user", config.RepositoryConfig{
		Name:    "community-pack",
		URL:     "https://example.com/community/skills.json",
		Curated: false,
	})
	if err != nil {
		t.Fatalf("AddRepository() error = %v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	cfg, _, err := config.LoadMergedConfig(cwd)
	if err != nil {
		t.Fatalf("LoadMergedConfig() error = %v", err)
	}
	if len(cfg.Repositories) != 1 {
		t.Fatalf("Repositories len = %d, want 1", len(cfg.Repositories))
	}
	if cfg.Repositories[0].Name != "community-pack" {
		t.Fatalf("Repositories[0].Name = %q", cfg.Repositories[0].Name)
	}
	if cfg.Repositories[0].URL != "https://example.com/community/skills.json" {
		t.Fatalf("Repositories[0].URL = %q", cfg.Repositories[0].URL)
	}
}

func TestAddRepositoryProjectWritesToProjectConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()

	cfgPath, err := config.AddRepository(root, "project", config.RepositoryConfig{
		Name:    "local-pack",
		URL:     "https://example.com/local/skills.json",
		Curated: false,
	})
	if err != nil {
		t.Fatalf("AddRepository() error = %v", err)
	}
	if want := filepath.Join(root, ".skills", "config.json"); cfgPath != want {
		t.Fatalf("cfgPath = %q, want %q", cfgPath, want)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file not written: %v", err)
	}
}

func TestRemoveRepositoryByName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	cwd := t.TempDir()
	_, err := config.AddRepository(cwd, "user", config.RepositoryConfig{Name: "a", URL: "https://example.com/a/skills.json"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = config.AddRepository(cwd, "user", config.RepositoryConfig{Name: "b", URL: "https://example.com/b/skills.json"})
	if err != nil {
		t.Fatal(err)
	}

	cfgPath, removed, err := config.RemoveRepository(cwd, "user", "a")
	if err != nil {
		t.Fatalf("RemoveRepository() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file missing: %v", err)
	}

	cfg, _, err := config.LoadMergedConfig(cwd)
	if err != nil {
		t.Fatalf("LoadMergedConfig() error = %v", err)
	}
	if len(cfg.Repositories) != 1 || cfg.Repositories[0].Name != "b" {
		t.Fatalf("Repositories = %#v", cfg.Repositories)
	}
}

func TestRemoveRepositoryByURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	cwd := t.TempDir()
	_, err := config.AddRepository(cwd, "user", config.RepositoryConfig{Name: "a", URL: "https://example.com/a/skills.json"})
	if err != nil {
		t.Fatal(err)
	}

	_, removed, err := config.RemoveRepository(cwd, "user", "https://example.com/a/skills.json")
	if err != nil {
		t.Fatalf("RemoveRepository() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
}

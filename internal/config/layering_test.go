package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeYAML(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadConfig_LayerOrder pins defaults < system < user < project <
// extra -c file < -c key=value. Each case enables one more layer, all
// setting the same key, so the winner identifies the precedence step.
func TestLoadConfig_LayerOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	cwd := t.TempDir()

	system := filepath.Join(t.TempDir(), "system", "config.yaml")
	user := filepath.Join(home, "cfg", AppName, "config.yaml")
	proj := filepath.Join(cwd, ".gym", "config.yaml")
	extra := filepath.Join(t.TempDir(), "extra.yaml")

	layers := []struct {
		name string
		want int
		put  func()
	}{
		{"system", 200, func() { writeYAML(t, system, "curation:\n  min_stars: 200\n") }},
		{"user", 300, func() { writeYAML(t, user, "curation:\n  min_stars: 300\n") }},
		{"project", 400, func() { writeYAML(t, proj, "curation:\n  min_stars: 400\n") }},
		{"extra", 500, func() { writeYAML(t, extra, "curation:\n  min_stars: 500\n") }},
	}

	for n := 0; n <= len(layers); n++ {
		for _, p := range []string{system, user, proj, extra} {
			_ = os.Remove(p)
		}
		want := 100 // defaultConfig
		var paths []string
		for _, l := range layers[:n] {
			l.put()
			want = l.want
			if l.name == "extra" {
				paths = []string{extra}
			}
		}
		opts := layerOptions(cwd)
		opts.SystemConfigPath = system
		cfg, err := loadConfig(cwd, opts, paths, nil)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if cfg.Curation.MinStars != want {
			t.Fatalf("n=%d (%d layers on): MinStars = %d, want %d", n, n, cfg.Curation.MinStars, want)
		}
	}

	// -c key=value beats every file layer.
	for _, p := range []string{system, user, proj, extra} {
		writeYAML(t, p, "curation:\n  min_stars: 999\n")
	}
	opts := layerOptions(cwd)
	opts.SystemConfigPath = system
	cfg, err := loadConfig(cwd, opts, []string{extra}, map[string]any{"curation": map[string]any{"min_stars": 600}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Curation.MinStars != 600 {
		t.Fatalf("override: MinStars = %d, want 600", cfg.Curation.MinStars)
	}
}

func TestLoadConfig_ExtraFilesLayerInOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	cwd := t.TempDir()
	dir := t.TempDir()
	var paths []string
	for i, v := range []int{11, 22} {
		p := filepath.Join(dir, "x"+strconv.Itoa(i)+".yaml")
		writeYAML(t, p, "curation:\n  min_stars: "+strconv.Itoa(v)+"\n")
		paths = append(paths, p)
	}
	cfg, err := LoadMergedConfig(cwd, paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Curation.MinStars != 22 {
		t.Fatalf("MinStars = %d, want 22 (later -c file wins)", cfg.Curation.MinStars)
	}
}

func TestLoadMergedConfig_MissingExtraFileErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	if _, err := LoadMergedConfig(t.TempDir(), []string{filepath.Join(home, "nope.yaml")}, nil); err == nil {
		t.Fatal("want error for missing -c file")
	}
}

// Project config is discovered by walking up from a subdirectory.
func TestLoadMergedConfig_ProjectWalkUp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	root := t.TempDir()
	writeYAML(t, filepath.Join(root, ".gym", "config.yaml"), "curation:\n  min_stars: 42\n")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadMergedConfig(sub, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Curation.MinStars != 42 {
		t.Fatalf("MinStars = %d, want 42", cfg.Curation.MinStars)
	}
}

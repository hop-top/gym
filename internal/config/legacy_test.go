package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"hop.top/gym/internal/config"
)

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	return home
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const legacyJSON = `{
  "curation": {"min_stars": 7},
  "install_paths": [{"name": "old", "path": "/old/skills", "scope": "global"}]
}`

// Existing installs keep a JSON user config in the previous XDG-style dir;
// it must still load, with config.yaml winning when both exist.
func TestUserConfigNames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		json     string
		yaml     string
		wantStar int
	}{
		{"only json", `{"curation":{"min_stars":7}}`, "", 7},
		{"both yaml wins", `{"curation":{"min_stars":7}}`, "curation:\n  min_stars: 9\n", 9},
		{"neither defaults", "", "", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateHome(t)
			dir := filepath.Join(home, ".config", "gym")
			if tc.json != "" {
				put(t, filepath.Join(dir, "config.json"), tc.json)
			}
			if tc.yaml != "" {
				put(t, filepath.Join(dir, "config.yaml"), tc.yaml)
			}
			cfg, err := config.LoadMergedConfig(t.TempDir(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Curation.MinStars != tc.wantStar {
				t.Fatalf("MinStars = %d, want %d", cfg.Curation.MinStars, tc.wantStar)
			}
		})
	}
}

func TestProjectConfigNames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		json     string
		yaml     string
		wantStar int
	}{
		{"only json", `{"curation":{"min_stars":7}}`, "", 7},
		{"both yaml wins", `{"curation":{"min_stars":7}}`, "curation:\n  min_stars: 9\n", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			root := t.TempDir()
			if tc.json != "" {
				put(t, filepath.Join(root, ".gym", "config.json"), tc.json)
			}
			if tc.yaml != "" {
				put(t, filepath.Join(root, ".gym", "config.yaml"), tc.yaml)
			}
			sub := filepath.Join(root, "a", "b")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadMergedConfig(sub, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Curation.MinStars != tc.wantStar {
				t.Fatalf("MinStars = %d, want %d", cfg.Curation.MinStars, tc.wantStar)
			}
		})
	}
}

func listing(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// A mutation starting from only config.json writes config.yaml holding the
// old settings plus the change and leaves the legacy file untouched.
func TestMutationAdoptsLegacy(t *testing.T) {
	for _, target := range []string{"user", "project"} {
		t.Run(target, func(t *testing.T) {
			home := isolateHome(t)
			cwd := t.TempDir()
			dir := filepath.Join(home, ".config", "gym")
			if target == "project" {
				dir = filepath.Join(cwd, ".gym")
			}
			legacy := filepath.Join(dir, "config.json")
			// A project file may only name install paths inside its tree.
			legacyBody, wantPath := legacyJSON, "/old/skills"
			if target == "project" {
				legacyBody = `{
  "curation": {"min_stars": 7},
  "install_paths": [{"name": "old", "path": "old/skills", "scope": "project"}]
}`
				wantPath = filepath.Join(cwd, "old", "skills")
			}
			put(t, legacy, legacyBody)

			path, err := config.AddRepository(cwd, target, config.RepositoryConfig{Name: "r", URL: "https://example.com/r"})
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(dir, "config.yaml"); path != want {
				t.Fatalf("wrote %q, want %q", path, want)
			}
			if got, _ := os.ReadFile(legacy); !bytes.Equal(got, []byte(legacyBody)) {
				t.Fatalf("legacy file changed: %q", got)
			}
			cfg, err := config.LoadMergedConfig(cwd, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Curation.MinStars != 7 || len(cfg.InstallPaths) != 1 || cfg.InstallPaths[0].Path != wantPath {
				t.Fatalf("old settings lost: %#v", cfg)
			}
			if len(cfg.Repositories) != 1 || cfg.Repositories[0].Name != "r" {
				t.Fatalf("change missing: %#v", cfg.Repositories)
			}
		})
	}
}

func TestReadsDoNotWrite(t *testing.T) {
	home := isolateHome(t)
	cwd := t.TempDir()
	put(t, filepath.Join(home, ".config", "gym", "config.json"), legacyJSON)
	put(t, filepath.Join(cwd, ".gym", "config.json"), legacyJSON)
	before := listing(t, home)
	beforeCwd := listing(t, cwd)

	if _, err := config.LoadMergedConfig(cwd, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := config.LoadUserRepositories(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := config.LoadProjectRepositories(cwd); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, home); !reflect.DeepEqual(got, before) {
		t.Fatalf("home listing changed:\n%v\n%v", before, got)
	}
	if got := listing(t, cwd); !reflect.DeepEqual(got, beforeCwd) {
		t.Fatalf("cwd listing changed:\n%v\n%v", beforeCwd, got)
	}
}

// JSON with escapes and non-BMP characters must survive adoption.
func TestAdoptLegacyRoundTrip(t *testing.T) {
	home := isolateHome(t)
	body := `{"install_paths":[{"name":"a\tb 😀 \/x","path":"/p","scope":"global"}]}`
	put(t, filepath.Join(home, ".config", "gym", "config.json"), body)
	cwd := t.TempDir()
	if _, err := config.AddRepository(cwd, "user", config.RepositoryConfig{Name: "r", URL: "u"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadMergedConfig(cwd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.InstallPaths[0].Name, "a\tb \U0001F600 /x"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func projectFixture(t *testing.T) (root string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	return t.TempDir()
}

// Only allowlisted keys are honoured from a project file; the rest warn once
// per key and leave the operator's value alone.
func TestProjectLayer_KeyAllowlist(t *testing.T) {
	root := projectFixture(t)
	writeYAML(t, filepath.Join(root, ".gym", "config.yaml"), `
validator: {command: [touch, /x], timeout_seconds: 99}
future_key: 1
curation: {min_stars: 7}
upgrade: {mirror_global_from_project: false}
repositories:
  - {name: r, url: "https://example.com/r.json", curated: false}
`)
	cfg, warns, err := LoadMergedConfigWarn(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`config: ignoring project key "future_key" (user/system/-c only)`,
		`config: ignoring project key "validator" (user/system/-c only)`,
	}
	if strings.Join(warns, "\n") != strings.Join(want, "\n") {
		t.Fatalf("warnings = %q, want %q", warns, want)
	}
	if got := cfg.Validator.Command; strings.Join(got, " ") != strings.Join(DefaultValidatorCommand(), " ") {
		t.Fatalf("project validator honoured: %v", got)
	}
	if cfg.Validator.TimeoutSeconds != 20 {
		t.Fatalf("project validator timeout honoured: %d", cfg.Validator.TimeoutSeconds)
	}
	if cfg.Curation.MinStars != 7 || cfg.Upgrade.MirrorGlobalFromProject || len(cfg.Repositories) != 1 {
		t.Fatalf("allowed keys not honoured: %+v", cfg)
	}
}

func TestProjectLayer_LegacyJSONAllowlist(t *testing.T) {
	root := projectFixture(t)
	writeYAML(t, filepath.Join(root, ".gym", "config.json"), `{"validator":{"command":["touch","/x"]},"curation":{"min_stars":3}}`)
	cfg, warns, err := LoadMergedConfigWarn(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], `"validator"`) {
		t.Fatalf("warnings = %q", warns)
	}
	if cfg.Validator.Command[0] != "skills-ref" || cfg.Curation.MinStars != 3 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

// The operator's layers are unrestricted, and they beat the project.
func TestProjectLayer_OperatorLayersUnrestricted(t *testing.T) {
	root := projectFixture(t)
	writeYAML(t, filepath.Join(root, ".gym", "config.yaml"), "validator: {command: [touch, /x]}\n")
	extra := filepath.Join(t.TempDir(), "x.yaml")
	writeYAML(t, extra, "validator: {command: [from-file]}\n")
	cfg, _, err := LoadMergedConfigWarn(root, []string{extra}, nil)
	if err != nil || cfg.Validator.Command[0] != "from-file" {
		t.Fatalf("-c file: %v %+v", err, cfg.Validator)
	}
	cfg, _, err = LoadMergedConfigWarn(root, []string{extra}, map[string]any{"validator": map[string]any{"command": []any{"from-flag"}}})
	if err != nil || cfg.Validator.Command[0] != "from-flag" {
		t.Fatalf("-c key=value: %v %+v", err, cfg.Validator)
	}
}

func TestProjectLayer_InstallPaths(t *testing.T) {
	root := projectFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := os.Symlink(out, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	cases := []struct {
		path string
		ok   bool
	}{
		{".claude/skills", true},
		{"a/../b", true},
		{"real", true},
		{"/etc", false},
		{"..", false},
		{"../x", false},
		{"a/../../x", false},
		{"~/x", false},
		{"~", false},
		{"", false},
		{".", false},
		{"link", false},
		{"link/sub", false},
	}
	for _, c := range cases {
		writeYAML(t, filepath.Join(root, ".gym", "config.yaml"),
			"install_paths:\n  - {name: p, path: \""+c.path+"\", scope: project}\n  - {name: keep, path: ok, scope: project}\n")
		cfg, warns, err := LoadMergedConfigWarn(root, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := false
		for _, p := range cfg.InstallPaths {
			if p.Name == "p" {
				got = true
				if !filepath.IsAbs(p.Path) || !strings.HasPrefix(p.Path, root) {
					t.Errorf("%q: path %q not resolved under root", c.path, p.Path)
				}
			}
		}
		if got != c.ok {
			t.Errorf("%q: kept=%v want %v", c.path, got, c.ok)
		}
		esc := 0
		for _, w := range warns {
			if strings.HasPrefix(w, "config: install path "+c.path+" escapes project "+root) {
				esc++
			}
		}
		if (esc == 1) == c.ok {
			t.Errorf("%q: warnings %q", c.path, warns)
		}
	}
}

// Operator layers may still use absolute and ~ paths.
func TestOperatorLayers_InstallPathsUnrestricted(t *testing.T) {
	root := projectFixture(t)
	extra := filepath.Join(t.TempDir(), "x.yaml")
	writeYAML(t, extra, "install_paths:\n  - {name: a, path: /abs/skills, scope: project}\n  - {name: b, path: \"~/s\", scope: project}\n")
	cfg, warns, err := LoadMergedConfigWarn(root, []string{extra}, nil)
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %q", err, warns)
	}
	if cfg.InstallPaths[0].Path != "/abs/skills" || cfg.InstallPaths[1].Path != filepath.Join(os.Getenv("HOME"), "s") {
		t.Fatalf("%+v", cfg.InstallPaths)
	}
}

// A relative project-scope path from an operator layer anchors at the
// project root even when run from a subdirectory.
func TestNormalizePaths_RelativeUsesProjectRoot(t *testing.T) {
	root := projectFixture(t)
	writeYAML(t, filepath.Join(root, ".gym", "config.yaml"), "curation: {min_stars: 1}\n")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(t.TempDir(), "x.yaml")
	writeYAML(t, extra, "install_paths:\n  - {name: a, path: rel/skills, scope: project}\n")
	cfg, _, err := LoadMergedConfigWarn(sub, []string{extra}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "rel", "skills"); cfg.InstallPaths[0].Path != want {
		t.Fatalf("path = %q, want %q", cfg.InstallPaths[0].Path, want)
	}
}

// A malformed value under an ignored key is harmless.
func TestProjectLayer_IgnoredKeyMalformedIsHarmless(t *testing.T) {
	root := projectFixture(t)
	writeYAML(t, filepath.Join(root, ".gym", "config.yaml"), "validator: not-a-map\n")
	if _, warns, err := LoadMergedConfigWarn(root, nil, nil); err != nil || len(warns) != 1 {
		t.Fatalf("err=%v warns=%q", err, warns)
	}
}

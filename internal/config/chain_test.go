package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
)

// chainEnv isolates HOME/XDG and lays out home/org/unit/proj with the
// given config.yaml bodies ("" = no file; a root marker is still made so
// the folder is a scope). Returns the folders, innermost last.
func chainEnv(t *testing.T, orgYAML, unitYAML, projYAML string) (org, unit, proj string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	org = filepath.Join(home, "org")
	unit = filepath.Join(org, "unit")
	proj = filepath.Join(unit, "proj")
	for dir, body := range map[string]string{org: orgYAML, unit: unitYAML, proj: projYAML} {
		if err := os.MkdirAll(filepath.Join(dir, ".gym"), 0o755); err != nil {
			t.Fatal(err)
		}
		if body == "" {
			body = "{}\n"
		}
		writeAdmFile(t, filepath.Join(dir, ".gym", "config.yaml"), body)
	}
	return org, unit, proj
}

// A7: the org ceiling still applies when a deeper folder has its own
// config.yaml that says nothing about admission (nearest-file-only loading
// dropped it).
func TestChain_OrgCeilingSurvivesSilentInnerConfig(t *testing.T) {
	_, _, proj := chainEnv(t, "admission:\n  max_data_class: public\n", "", "curation: {min_stars: 5}\n")
	cfg, err := config.LoadMergedConfig(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Admission.MaxDataClass; got != "public" {
		t.Fatalf("max_data_class = %q, want public (org ceiling dropped)", got)
	}
	if cfg.Curation.MinStars != 5 {
		t.Fatalf("inner curation not honoured: %d", cfg.Curation.MinStars)
	}
}

// A8: an inner folder cannot raise the ceiling, lower require_manifest, or
// switch require_approval off; the outer value is kept. An inner folder can
// still tighten further.
func TestChain_InnerFolderCannotLoosen(t *testing.T) {
	org := "admission:\n  max_data_class: public\n  require_manifest: reject\n  require_approval: true\n"
	inner := "admission:\n  max_data_class: confidential\n  require_manifest: off\n  require_approval: false\n"
	_, _, proj := chainEnv(t, org, inner, "")
	cfg, err := config.LoadMergedConfig(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Admission
	if a.MaxDataClass != "public" || a.RequireManifest != "reject" || !a.RequireApproval {
		t.Fatalf("inner folder loosened outer admission: %+v", a)
	}

	_, _, proj = chainEnv(t, "admission:\n  max_data_class: internal\n", "admission:\n  max_data_class: public\n  require_approval: true\n", "admission:\n  max_data_class: internal\n")
	cfg, err = config.LoadMergedConfig(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a := cfg.Admission; a.MaxDataClass != "public" || !a.RequireApproval {
		t.Fatalf("tightening lost: %+v", a)
	}
}

// Operator layers (-c) still win over the folder chain.
func TestChain_OperatorOverrideBeatsChain(t *testing.T) {
	_, _, proj := chainEnv(t, "admission:\n  max_data_class: public\n", "", "")
	cfg, err := config.LoadMergedConfig(proj, nil, map[string]any{"admission": map[string]any{"max_data_class": "personal"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.MaxDataClass != "personal" {
		t.Fatalf("override ignored: %q", cfg.Admission.MaxDataClass)
	}
}

// A9: a folder's install_paths that escape it (.. or absolute) are ignored
// with a warning, in an ancestor as in the innermost folder.
func TestChain_FolderInstallPathsContained(t *testing.T) {
	bad := "install_paths:\n  - {name: up, path: ../x, scope: project}\n  - {name: abs, path: /tmp/elsewhere, scope: project}\n  - {name: ok, path: .claude/skills, scope: project}\n"
	org, _, proj := chainEnv(t, bad, "", "")
	cfg, warns, err := config.LoadMergedConfigWarn(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.InstallPaths {
		if p.Name == "up" || p.Name == "abs" {
			t.Fatalf("escaping path honoured: %+v", p)
		}
	}
	for _, p := range []string{"../x", "/tmp/elsewhere"} {
		want := "config: install path " + p + " escapes project " + org
		found := false
		for _, w := range warns {
			found = found || w == want
		}
		if !found {
			t.Errorf("missing warning %q in %q", want, warns)
		}
	}
	// The kept relative path resolves against the scope root, not the
	// ancestor that defined it.
	if len(cfg.InstallPaths) != 1 || cfg.InstallPaths[0].Path != filepath.Join(proj, ".claude", "skills") {
		t.Fatalf("install paths = %+v, want one under %s", cfg.InstallPaths, proj)
	}
}

// install_paths: the innermost layer that sets it replaces; a layer that
// leaves it unset inherits. Other lists follow innermost-wins.
func TestChain_InstallPathsInnermostSetsReplaces(t *testing.T) {
	org := "install_paths:\n  - {name: a, path: .a/skills, scope: project}\nrepositories:\n  - {name: orgrepo, url: \"https://example.com/o.json\"}\n"
	unit := "install_paths:\n  - {name: b, path: .b/skills, scope: project}\n"
	_, _, proj := chainEnv(t, org, unit, "")
	cfg, err := config.LoadMergedConfig(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.InstallPaths) != 1 || cfg.InstallPaths[0].Name != "b" {
		t.Fatalf("install paths = %+v, want only b (unit replaces org)", cfg.InstallPaths)
	}
	if len(cfg.Repositories) != 1 || cfg.Repositories[0].Name != "orgrepo" {
		t.Fatalf("repositories = %+v, want inherited orgrepo", cfg.Repositories)
	}
}

// A hostile validator in an ancestor folder is ignored like one in the
// innermost folder.
func TestChain_AncestorValidatorIgnored(t *testing.T) {
	_, _, proj := chainEnv(t, "validator: {command: [touch, /tmp/x]}\n", "", "")
	cfg, warns, err := config.LoadMergedConfigWarn(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Validator.Command, " ") != strings.Join(config.DefaultValidatorCommand(), " ") {
		t.Fatalf("ancestor validator honoured: %v", cfg.Validator.Command)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], `"validator"`) {
		t.Fatalf("warnings = %q", warns)
	}
}

// A planted ancestor (group-writable .gym) contributes nothing.
func TestChain_UnsafeAncestorConfigIgnored(t *testing.T) {
	org, _, proj := chainEnv(t, "admission:\n  max_data_class: public\n", "", "")
	if err := os.Chmod(filepath.Join(org, ".gym"), 0o775); err != nil {
		t.Fatal(err)
	}
	cfg, warns, err := config.LoadMergedConfigWarn(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.MaxDataClass != "internal" {
		t.Fatalf("unsafe ancestor honoured: %q", cfg.Admission.MaxDataClass)
	}
	if len(warns) != 1 || !strings.HasPrefix(warns[0], "scope: ignoring "+org+"/.gym") {
		t.Fatalf("warnings = %q", warns)
	}
}

// LoadScoped anchors relative operator-layer paths at the given scope root
// even when that folder is not a root yet.
func TestLoadScoped_NewScopeBelowRoot(t *testing.T) {
	org, _, _ := chainEnv(t, "admission:\n  max_data_class: public\n", "", "")
	fresh := filepath.Join(org, "fresh")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadScoped(fresh, []string{org}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.MaxDataClass != "public" {
		t.Fatalf("org ceiling not applied to new scope: %q", cfg.Admission.MaxDataClass)
	}
}

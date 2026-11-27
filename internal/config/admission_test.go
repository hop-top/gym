package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
)

// admissionEnv isolates HOME and every XDG dir, then writes optional
// user + project config.yaml files. Returns the project root (cwd).
func admissionEnv(t *testing.T, userYAML, projectYAML string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if userYAML != "" {
		writeAdmFile(t, filepath.Join(home, ".config", "gym", "config.yaml"), userYAML)
	}
	root := t.TempDir()
	if projectYAML != "" {
		writeAdmFile(t, filepath.Join(root, ".gym", "config.yaml"), projectYAML)
	}
	return root
}

func writeAdmFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// load is LoadMergedConfig with no -c layers.
func load(root string) (config.Config, error) { return config.LoadMergedConfig(root, nil, nil) }

// Case 19: ceiling unset defaults to internal.
func TestAdmissionConfig_Defaults(t *testing.T) {
	root := admissionEnv(t, "", "")
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Admission
	if a.MaxDataClass != "internal" || a.RequireApproval || a.RequireManifest != "warn" {
		t.Fatalf("defaults = %+v", a)
	}
}

func TestAdmissionConfig_UserSetsValues(t *testing.T) {
	root := admissionEnv(t, "admission:\n  max_data_class: personal\n  require_approval: true\n  require_manifest: reject\n", "")
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Admission
	if a.MaxDataClass != "personal" || !a.RequireApproval || a.RequireManifest != "reject" {
		t.Fatalf("admission = %+v", a)
	}
}

// Case 20: project config may only lower the ceiling.
func TestAdmissionConfig_ProjectCannotRaiseCeiling(t *testing.T) {
	root := admissionEnv(t,
		"admission:\n  max_data_class: confidential\n",
		"admission:\n  max_data_class: special_category\n")
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.MaxDataClass != "confidential" {
		t.Fatalf("ceiling = %q; project raised it", cfg.Admission.MaxDataClass)
	}
}

func TestAdmissionConfig_ProjectCannotRaiseDefaultCeiling(t *testing.T) {
	root := admissionEnv(t, "", "admission:\n  max_data_class: special_category\n")
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.MaxDataClass != "internal" {
		t.Fatalf("ceiling = %q; project raised the default", cfg.Admission.MaxDataClass)
	}
}

func TestAdmissionConfig_ProjectMayLowerCeiling(t *testing.T) {
	root := admissionEnv(t,
		"admission:\n  max_data_class: confidential\n",
		"admission:\n  max_data_class: public\n")
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.MaxDataClass != "public" {
		t.Fatalf("ceiling = %q; want public", cfg.Admission.MaxDataClass)
	}
}

func TestAdmissionConfig_ProjectCannotRelaxRequirements(t *testing.T) {
	root := admissionEnv(t,
		"admission:\n  require_approval: true\n  require_manifest: reject\n",
		"admission:\n  require_approval: false\n  require_manifest: 'off'\n")
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Admission.RequireApproval || cfg.Admission.RequireManifest != "reject" {
		t.Fatalf("project relaxed requirements: %+v", cfg.Admission)
	}
}

func TestAdmissionConfig_ProjectMayTightenRequirements(t *testing.T) {
	root := admissionEnv(t, "", "admission:\n  require_approval: true\n  require_manifest: reject\n")
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Admission.RequireApproval || cfg.Admission.RequireManifest != "reject" {
		t.Fatalf("admission = %+v", cfg.Admission)
	}
}

// Invalid config values are a config error before any install.
func TestAdmissionConfig_InvalidValuesError(t *testing.T) {
	for name, user := range map[string]string{
		"class":    "admission:\n  max_data_class: secret\n",
		"manifest": "admission:\n  require_manifest: maybe\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := admissionEnv(t, user, "")
			_, err := load(root)
			if err == nil || !strings.Contains(err.Error(), "admission.") {
				t.Fatalf("err = %v; want admission config error", err)
			}
		})
	}
}

func TestClampMaxDataClass(t *testing.T) {
	cases := []struct{ user, project, want string }{
		{"confidential", "", "confidential"},
		{"confidential", "special_category", "confidential"}, // cannot raise
		{"confidential", "confidential", "confidential"},
		{"confidential", "public", "public"}, // may lower
		{"internal", "personal", "internal"},
		{"internal", "bogus", "internal"},
	}
	for _, c := range cases {
		if got := config.ClampMaxDataClass(c.user, c.project); got != c.want {
			t.Errorf("ClampMaxDataClass(%q, %q) = %q; want %q", c.user, c.project, got, c.want)
		}
	}
}

func TestClampManifestModeAndApproval(t *testing.T) {
	if got := config.ClampManifestMode("reject", "off"); got != "reject" {
		t.Errorf("project relaxed reject -> %s", got)
	}
	if got := config.ClampManifestMode("warn", "reject"); got != "reject" {
		t.Errorf("project could not tighten -> %s", got)
	}
	if got := config.ClampManifestMode("warn", "off"); got != "warn" {
		t.Errorf("project relaxed warn -> %s", got)
	}
	if !config.ClampRequireApproval(true, false) || !config.ClampRequireApproval(false, true) || config.ClampRequireApproval(false, false) {
		t.Error("ClampRequireApproval wrong")
	}
}

func TestResolveAdmission_Pure(t *testing.T) {
	s := func(v string) *string { return &v }
	cfg, err := config.ResolveAdmission(&config.AdmissionOverrides{MaxDataClass: s("personal")},
		&config.AdmissionOverrides{MaxDataClass: s("special_category")})
	if err != nil || cfg.MaxDataClass != "personal" {
		t.Fatalf("%+v %v", cfg, err)
	}
	if _, err := config.ResolveAdmission(nil, &config.AdmissionOverrides{MaxDataClass: s("x")}); err == nil {
		t.Fatal("invalid project value accepted")
	}
	if cfg, _ := config.ResolveAdmission(nil, nil); cfg != config.DefaultAdmission() {
		t.Fatalf("defaults = %+v", cfg)
	}
}

// Project cannot relax through a legacy project config.json either:
// the kit-loaded project layer uses the same marker selection.
func TestAdmissionConfig_LegacyProjectJSONStillClamped(t *testing.T) {
	root := admissionEnv(t, "admission:\n  max_data_class: personal\n  require_manifest: reject\n", "")
	writeAdmFile(t, filepath.Join(root, ".gym", "config.json"),
		`{"admission":{"max_data_class":"special_category","require_manifest":"off","require_approval":true}}`)
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Admission
	if a.MaxDataClass != "personal" || a.RequireManifest != "reject" || !a.RequireApproval {
		t.Fatalf("admission = %+v", a)
	}
}

// Case (c): admission keys in a legacy user config.json are honoured.
func TestAdmissionConfig_LegacyUserJSONHonoured(t *testing.T) {
	root := admissionEnv(t, "", "")
	home := os.Getenv("HOME")
	writeAdmFile(t, filepath.Join(home, ".config", "gym", "config.json"),
		`{"admission":{"max_data_class":"confidential","require_approval":true,"require_manifest":"reject"}}`)
	cfg, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Admission
	if a.MaxDataClass != "confidential" || !a.RequireApproval || a.RequireManifest != "reject" {
		t.Fatalf("admission = %+v", a)
	}
	// and the project clamp is measured against the legacy user values
	writeAdmFile(t, filepath.Join(root, ".gym", "config.yaml"), "admission:\n  max_data_class: special_category\n")
	if cfg, err = load(root); err != nil || cfg.Admission.MaxDataClass != "confidential" {
		t.Fatalf("clamp vs legacy user: %+v %v", cfg.Admission, err)
	}
}

func admOverride(class string) map[string]any {
	return map[string]any{"admission": map[string]any{"max_data_class": class}}
}

// Case (b): -c admission.max_data_class=... is operator-trusted and
// applies after the project clamp: it may raise or lower.
func TestAdmissionConfig_OverrideRaisesOverProject(t *testing.T) {
	root := admissionEnv(t, "", "admission:\n  max_data_class: public\n")
	cfg, err := config.LoadMergedConfig(root, nil, admOverride("confidential"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.MaxDataClass != "confidential" {
		t.Fatalf("ceiling = %q; -c override must beat project", cfg.Admission.MaxDataClass)
	}
	cfg, err = config.LoadMergedConfig(root, nil, map[string]any{"admission": map[string]any{
		"require_manifest": "off", "require_approval": false}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.RequireManifest != "off" || cfg.Admission.MaxDataClass != "public" {
		t.Fatalf("admission = %+v", cfg.Admission)
	}
}

func TestAdmissionConfig_OverrideLowersBelowUser(t *testing.T) {
	root := admissionEnv(t, "admission:\n  max_data_class: confidential\n", "")
	cfg, err := config.LoadMergedConfig(root, nil, admOverride("public"))
	if err != nil || cfg.Admission.MaxDataClass != "public" {
		t.Fatalf("%+v %v", cfg.Admission, err)
	}
}

// -c <file> applies after the project clamp too, and -c key=value
// beats -c <file>.
func TestAdmissionConfig_ExtraFileAfterClampBeforeOverride(t *testing.T) {
	root := admissionEnv(t, "", "admission:\n  max_data_class: public\n  require_approval: true\n")
	extra := filepath.Join(t.TempDir(), "extra.yaml")
	writeAdmFile(t, extra, "admission:\n  max_data_class: confidential\n  require_approval: false\n")
	cfg, err := config.LoadMergedConfig(root, []string{extra}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admission.MaxDataClass != "confidential" || cfg.Admission.RequireApproval {
		t.Fatalf("extra file did not beat project: %+v", cfg.Admission)
	}
	cfg, err = config.LoadMergedConfig(root, []string{extra}, admOverride("personal"))
	if err != nil || cfg.Admission.MaxDataClass != "personal" {
		t.Fatalf("override did not beat extra file: %+v %v", cfg.Admission, err)
	}
}

// Invalid values from any layer are a config error.
func TestAdmissionConfig_InvalidProjectAndOverrideError(t *testing.T) {
	root := admissionEnv(t, "", "admission:\n  max_data_class: secret\n")
	if _, err := load(root); err == nil || !strings.Contains(err.Error(), "admission.") {
		t.Fatalf("invalid project value: err = %v", err)
	}
	root = admissionEnv(t, "", "")
	if _, err := config.LoadMergedConfig(root, nil, admOverride("secret")); err == nil || !strings.Contains(err.Error(), "admission.") {
		t.Fatalf("invalid override: err = %v", err)
	}
}

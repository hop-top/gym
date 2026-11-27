package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
)

func writeUserYAML(t *testing.T, body string) {
	t.Helper()
	writeAdmFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "gym", "config.yaml"), body)
}

// B14 (config half): a project or folder may raise admission.require_pin
// and never lower it.
func TestB14ChainRequirePinClamps(t *testing.T) {
	// Org says remote; inner folders try none, then all.
	_, _, proj := chainEnv(t, "admission:\n  require_pin: remote\n", "admission:\n  require_pin: none\n", "")
	cfg, err := config.LoadMergedConfig(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Admission.RequirePin; got != "remote" {
		t.Fatalf("inner folder lowered require_pin to %q", got)
	}

	_, _, proj = chainEnv(t, "admission:\n  require_pin: remote\n", "admission:\n  require_pin: all\n", "admission:\n  require_pin: none\n")
	cfg, err = config.LoadMergedConfig(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Admission.RequirePin; got != "all" {
		t.Fatalf("require_pin = %q, want all (raise kept, later none ignored)", got)
	}
}

func TestB14UserRequirePinBeatsProjectNone(t *testing.T) {
	_, _, proj := chainEnv(t, "", "", "admission:\n  require_pin: none\n")
	writeUserYAML(t, "admission:\n  require_pin: remote\n")
	cfg, err := config.LoadMergedConfig(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Admission.RequirePin; got != "remote" {
		t.Fatalf("project lowered the user's require_pin to %q", got)
	}
	// A project can raise it.
	_, _, proj = chainEnv(t, "", "", "admission:\n  require_pin: all\n")
	writeUserYAML(t, "admission:\n  require_pin: remote\n")
	if cfg, err = config.LoadMergedConfig(proj, nil, nil); err != nil || cfg.Admission.RequirePin != "all" {
		t.Fatalf("project raise lost: %q %v", cfg.Admission.RequirePin, err)
	}
	// The operator's -c override is trusted and may lower.
	if cfg, err = config.LoadMergedConfig(proj, nil, map[string]any{"admission": map[string]any{"require_pin": "none"}}); err != nil || cfg.Admission.RequirePin != "none" {
		t.Fatalf("operator override: %q %v", cfg.Admission.RequirePin, err)
	}
}

func TestRequirePinDefaultAndValidation(t *testing.T) {
	_, _, proj := chainEnv(t, "", "", "")
	cfg, err := config.LoadMergedConfig(proj, nil, nil)
	if err != nil || cfg.Admission.RequirePin != config.PinNone {
		t.Fatalf("default = %q %v, want none", cfg.Admission.RequirePin, err)
	}
	_, _, proj = chainEnv(t, "admission:\n  require_pin: sometimes\n", "", "")
	if _, err = config.LoadMergedConfig(proj, nil, nil); err == nil || !strings.Contains(err.Error(), "invalid admission.require_pin") {
		t.Fatalf("err = %v", err)
	}
}

func TestClampRequirePin(t *testing.T) {
	for _, c := range []struct{ user, project, want string }{
		{"none", "remote", "remote"}, {"none", "all", "all"}, {"remote", "all", "all"},
		{"remote", "none", "remote"}, {"all", "remote", "all"}, {"all", "", "all"},
		{"remote", "bogus", "remote"},
	} {
		if got := config.ClampRequirePin(c.user, c.project); got != c.want {
			t.Errorf("Clamp(%q, %q) = %q, want %q", c.user, c.project, got, c.want)
		}
	}
	o := "remote"
	none := "none"
	got, err := config.ResolveAdmission(&config.AdmissionOverrides{RequirePin: &o}, &config.AdmissionOverrides{RequirePin: &none})
	if err != nil || got.RequirePin != "remote" {
		t.Fatalf("ResolveAdmission = %+v %v", got, err)
	}
}

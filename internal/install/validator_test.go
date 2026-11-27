package install_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/install"
)

func TestValidateSkillPackagePasses(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("# test"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.ValidatorConfig{
		Command:        []string{"sh", "-c", "test -f '{path}/SKILL.md'"},
		TimeoutSeconds: 3,
	}
	if err := install.ValidateSkillPackage(d, cfg); err != nil {
		t.Fatalf("ValidateSkillPackage() error = %v, want nil", err)
	}
}

func TestValidateSkillPackageFails(t *testing.T) {
	d := t.TempDir()
	cfg := config.ValidatorConfig{
		Command:        []string{"sh", "-c", "test -f '{path}/SKILL.md'"},
		TimeoutSeconds: 3,
	}
	if err := install.ValidateSkillPackage(d, cfg); err == nil {
		t.Fatalf("ValidateSkillPackage() error = nil, want non-nil")
	}
}

// fakeSkillsRef puts a skills-ref stand-in on PATH that mimics the
// reference CLI: only `validate <dir>` exists; any other subcommand
// exits 2; validate exits 1 when SKILL.md is missing.
func fakeSkillsRef(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
[ "$1" = "validate" ] || { echo "Error: No such command '$1'." >&2; exit 2; }
[ -f "$2/SKILL.md" ] || { echo "missing SKILL.md" >&2; exit 1; }
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "skills-ref"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestValidateSkillPackageDefaultCommandUsesValidate(t *testing.T) {
	fakeSkillsRef(t)
	good := t.TempDir()
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"), []byte("# test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := install.ValidateSkillPackage(good, config.ValidatorConfig{}); err != nil {
		t.Fatalf("default validator on valid skill: %v", err)
	}
	if err := install.ValidateSkillPackage(t.TempDir(), config.ValidatorConfig{}); err == nil {
		t.Fatal("default validator on invalid skill: error = nil, want non-nil")
	}
}

func TestDefaultValidatorCommandAgrees(t *testing.T) {
	want := []string{"skills-ref", "validate", "{path}"}
	if got := config.DefaultValidatorCommand(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultValidatorCommand() = %v, want %v", got, want)
	}
	// returned slice must be a copy
	c := config.DefaultValidatorCommand()
	c[0] = "mutated"
	if got := config.DefaultValidatorCommand(); got[0] != "skills-ref" {
		t.Fatalf("DefaultValidatorCommand() aliased: %v", got)
	}
}

package install_test

import (
	"os"
	"path/filepath"
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

package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/registry"
)

// B14: require_pin remote denies an unpinned registry skill, accepts a
// pinned one and a directory source; a project cannot lower it.
func TestB14RequirePinRemoteDeniesUnpinnedRegistrySkill(t *testing.T) {
	e := newLkEnv(t, "admission:\n  require_pin: remote\n")
	src := lkSkillSrc(t, scratchDir(t), "lint", "a")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: src})
	// A project asks for less: ignored.
	writeFiles(t, e.proj, map[string]string{".gym/config.yaml": "admission:\n  require_pin: none\n"})

	code, _, errs := e.gym(t, "learn", "lint")
	if code != 1 || !strings.Contains(errs, "admission-digest-required") {
		t.Fatalf("unpinned registry skill: exit %d\n%s", code, errs)
	}
	if installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("denied install wrote files")
	}

	d, err := install.Digest(src)
	if err != nil {
		t.Fatal(err)
	}
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: src, Digest: d})
	e.mustGym(t, "learn", "lint")

	// Directory sources are exempt under remote.
	dirSrc := lkSkillSrc(t, scratchDir(t), "fmt", "a")
	e.mustGym(t, "learn", dirSrc)
}

func TestB14RequirePinAllDeniesDirectorySource(t *testing.T) {
	e := newLkEnv(t, "admission:\n  require_pin: all\n")
	code, _, errs := e.gym(t, "learn", lkSkillSrc(t, scratchDir(t), "fmt", "a"))
	if code != 1 || !strings.Contains(errs, "admission-digest-required") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

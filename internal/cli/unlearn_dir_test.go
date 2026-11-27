package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func dirEnv(t *testing.T) (home, proj, sub string) {
	t.Helper()
	home = isolate(t)
	writeUserConfig(t, home, "")
	proj = filepath.Join(scratchDir(t), "proj")
	sub = filepath.Join(proj, "sub")
	writeFiles(t, sub, map[string]string{"keep": ""})
	scopeTree(t, proj)
	scopeTree(t, sub)
	return
}

func TestUnlearnDir_RemovesFromNamedFolderOnly(t *testing.T) {
	_, proj, sub := dirEnv(t)
	other := filepath.Join(scratchDir(t), "elsewhere")
	scopeTree(t, other)
	learnInto(t, sub, proj, "lint")
	learnInto(t, sub, sub, "lint")
	code, out, errs := runGym(t, sub, "unlearn", "--dir", proj, "lint")
	if code != 0 || !strings.Contains(out, "removed lint "+filepath.Join(proj, ".gym", "skills", "lint")) {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if exists(filepath.Join(proj, ".gym", "skills", "lint")) {
		t.Fatal("--dir folder not removed")
	}
	if !exists(filepath.Join(sub, ".gym", "skills", "lint")) {
		t.Fatal("current directory scope was touched")
	}
	// relative --dir resolves against the working directory
	if code, _, errs := runGym(t, proj, "unlearn", "--dir", "sub", "lint"); code != 0 {
		t.Fatalf("relative --dir: exit %d\n%s", code, errs)
	}
	if exists(filepath.Join(sub, ".gym", "skills", "lint")) {
		t.Fatal("relative --dir not removed")
	}
}

func TestUnlearnDir_Validation(t *testing.T) {
	_, proj, _ := dirEnv(t)
	missing := filepath.Join(proj, "nope")
	if code, _, errs := runGym(t, proj, "unlearn", "--dir", missing, "lint"); code != 1 || !strings.Contains(errs, "--dir "+missing+": not a directory") {
		t.Fatalf("missing: exit %d\n%s", code, errs)
	}
	file := filepath.Join(proj, "afile")
	writeFiles(t, proj, map[string]string{"afile": "x"})
	if code, _, errs := runGym(t, proj, "unlearn", "--dir", file, "lint"); code != 1 || !strings.Contains(errs, "--dir "+file+": not a directory") {
		t.Fatalf("file: exit %d\n%s", code, errs)
	}
	if code, _, errs := runGym(t, proj, "unlearn", "--dir", proj, "-g", "lint"); code != 1 || !strings.Contains(errs, "--dir and --global are mutually exclusive") {
		t.Fatalf("-g: exit %d\n%s", code, errs)
	}
}

// A skill installed only at an ancestor root is not removed from a
// subfolder: the scope is not inherited, and the hint says where it is.
func TestUnlearnDir_NotInheritedFromAncestor(t *testing.T) {
	_, proj, _ := dirEnv(t)
	sub := filepath.Join(proj, "plain")
	writeFiles(t, sub, map[string]string{"x": ""})
	learnInto(t, proj, proj, "lint")
	if code, _, errs := runGym(t, proj, "learn", "-g", hostileSkill(t, "lint", "1")); code != 0 {
		t.Fatalf("learn -g: %d\n%s", code, errs)
	}
	before := snapshot(t, filepath.Join(proj, ".gym"))
	code, _, errs := runGym(t, sub, "unlearn", "lint")
	if code != 1 || !strings.Contains(errs, "skill-not-installed: lint in "+sub+" (installed in: "+proj+", global)") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if snapshot(t, filepath.Join(proj, ".gym")) != before || !exists(filepath.Join(proj, ".gym", "skills", "lint")) {
		t.Fatal("ancestor scope was modified")
	}
}

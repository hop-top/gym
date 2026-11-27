package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
)

// The lock is per scope root: learn, train and list --check with --dir read
// and write the lock of that folder, not the current one.
func TestDirFlagUsesThatRootsLock(t *testing.T) {
	e := newLkEnv(t, "")
	base := scratchDir(t)
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	scopeTree(t, a)
	scopeTree(t, b)
	srcOld := lkSkillSrc(t, filepath.Join(a, "old"), "lint", "old")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: srcOld})

	e.mustGym(t, "learn", "--dir", a, "lint")
	if _, err := os.Stat(e.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("learn --dir wrote the current folder's lock: %v", err)
	}
	if _, err := os.Stat(lock.ProjectPath(b)); !os.IsNotExist(err) {
		t.Fatal("learn --dir a wrote b's lock")
	}
	fa, err := lock.Load(lock.ProjectPath(a), lock.ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if ent, ok := fa.Find("lint"); !ok || ent.Version != "1.0.0" {
		t.Fatalf("a's lock = %+v", fa.Skills)
	}

	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: srcOld},
		registry.SkillEntry{Name: "lint", Version: "1.1.0", SourceURL: lkSkillSrc(t, filepath.Join(a, "new"), "lint", "new")})
	e.mustGym(t, "train", "--dir", a)
	fa, _ = lock.Load(lock.ProjectPath(a), lock.ScopeProject)
	if ent, _ := fa.Find("lint"); ent.Version != "1.1.0" {
		t.Fatalf("train --dir did not rewrite a's lock: %+v", ent)
	}
	if _, err := os.Stat(lock.ProjectPath(b)); !os.IsNotExist(err) {
		t.Fatal("train --dir a wrote b's lock")
	}

	if code, out, _ := e.gym(t, "list", "--check", "--dir", a); code != 0 || !strings.Contains(out, "lock ok") {
		t.Fatalf("list --check --dir a: exit %d\n%s", code, out)
	}
	if code, _, errs := e.gym(t, "list", "--check", "--dir", b); code != 1 || !strings.Contains(errs, "lock-missing") {
		t.Fatalf("list --check --dir b: exit %d\n%s", code, errs)
	}
}

// train -r rewrites each scope's own lock.
func TestTrainRecursiveRewritesEachScopesLock(t *testing.T) {
	e := newLkEnv(t, "")
	// The sources live inside a, which is inside e.proj: in-tree for both
	// scopes, so both locks can hold them.
	a := filepath.Join(e.proj, "a")
	scopeTree(t, e.proj)
	scopeTree(t, a)
	srcOld := lkSkillSrc(t, filepath.Join(a, "old"), "lint", "old")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: srcOld})
	e.mustGym(t, "learn", "--dir", a, "lint")
	e.mustGym(t, "learn", "--dir", e.proj, "lint")
	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: srcOld},
		registry.SkillEntry{Name: "lint", Version: "1.1.0", SourceURL: lkSkillSrc(t, filepath.Join(a, "new"), "lint", "new")})
	e.mustGym(t, "train", "-r")
	for _, root := range []string{e.proj, a} {
		f, err := lock.Load(lock.ProjectPath(root), lock.ScopeProject)
		if err != nil {
			t.Fatal(err)
		}
		if ent, _ := f.Find("lint"); ent.Version != "1.1.0" {
			t.Fatalf("%s lock = %+v", root, ent)
		}
	}
}

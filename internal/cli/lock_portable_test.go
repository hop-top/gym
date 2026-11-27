package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/lock"
)

const notLockedWarning = "lock-source-not-portable: "

// A directory outside the scope root installs, but a project lock never gets
// an entry it would refuse to read: it stays untracked, and learning it again
// works the same way.
func TestOutOfTreeDirIsInstalledButNotLocked(t *testing.T) {
	e := newLkEnv(t, "")
	out := lkSkillSrc(t, scratchDir(t), "outer", "o")
	in := lkSkillSrc(t, filepath.Join(e.proj, "src"), "inner", "i")
	e.mustGym(t, "learn", in)

	for i := 0; i < 2; i++ {
		got := e.mustGym(t, "learn", out)
		if !strings.Contains(got, notLockedWarning+out+" is outside "+e.proj+"; not written to the lock") || strings.Contains(got, "escapes") {
			t.Fatalf("run %d: warning missing or misworded:\n%s", i, got)
		}
		if !installed(filepath.Join(e.proj, ".gym", "skills"), "outer") {
			t.Fatalf("run %d: not installed", i)
		}
		if _, ok := e.loadLock(t).Find("outer"); ok {
			t.Fatalf("run %d: out-of-tree source was locked", i)
		}
	}
	// The lock still holds the in-tree entry, relative; restore ignores the rest.
	if ent := e.lockEntry(t, "inner"); ent.Source != "src/inner" {
		t.Fatalf("in-tree entry = %+v", ent)
	}
	if got := e.mustGym(t, "learn"); !strings.Contains(got, "nothing to restore") {
		t.Fatalf("restore:\n%s", got)
	}
	code, tbl, _ := e.gym(t, "list", "--check")
	if code != 0 || !strings.Contains(tbl, "untracked") || !strings.Contains(tbl, "outer") {
		t.Fatalf("list --check: exit %d\n%s", code, tbl)
	}
	// A restore is unaffected even when the untracked skill's directory is gone.
	if err := os.RemoveAll(e.skillDir("outer")); err != nil {
		t.Fatal(err)
	}
	if got := e.mustGym(t, "learn"); !strings.Contains(got, "nothing to restore") {
		t.Fatalf("restore after the out-of-tree dir vanished:\n%s", got)
	}
	e.mustGym(t, "learn", out)
	// An in-tree entry restores.
	if err := os.RemoveAll(e.skillDir("inner")); err != nil {
		t.Fatal(err)
	}
	e.mustGym(t, "learn")
	if !installed(filepath.Join(e.proj, ".gym", "skills"), "inner") {
		t.Fatal("in-tree entry did not restore")
	}
}

// Installing the same name from outside drops the entry that no longer
// describes what is installed.
func TestOutOfTreeDirReplacingLockedNameRemovesEntry(t *testing.T) {
	e := newLkEnv(t, "")
	in := lkSkillSrc(t, filepath.Join(e.proj, "src"), "lint", "in")
	e.mustGym(t, "learn", in)
	if _, ok := e.loadLock(t).Find("lint"); !ok {
		t.Fatal("setup: no entry")
	}
	out := lkSkillSrc(t, scratchDir(t), "lint", "out")
	got := e.mustGym(t, "learn", out)
	if !strings.Contains(got, notLockedWarning) {
		t.Fatalf("no warning:\n%s", got)
	}
	if _, ok := e.loadLock(t).Find("lint"); ok {
		t.Fatal("stale entry kept")
	}
	if !strings.Contains(lkRead(t, filepath.Join(e.skillDir("lint"), "SKILL.md")), "body out") {
		t.Fatal("out-of-tree content not installed")
	}
}

// Frozen: an out-of-tree install changes nothing in the lock, so it is
// allowed; one that would have to remove an entry is a modification.
func TestFrozenOutOfTreeDir(t *testing.T) {
	e, _ := lkLocked(t, "lock:\n  frozen: true\n")
	before := lkRead(t, e.lockPath())

	fresh := lkSkillSrc(t, scratchDir(t), "fresh", "x")
	got := e.mustGym(t, "learn", fresh)
	if !strings.Contains(got, notLockedWarning) || !installed(filepath.Join(e.proj, ".gym", "skills"), "fresh") {
		t.Fatalf("frozen out-of-tree install:\n%s", got)
	}
	if lkRead(t, e.lockPath()) != before {
		t.Fatal("lock changed under frozen")
	}

	clash := lkSkillSrc(t, scratchDir(t), "lint", "clash")
	code, _, errs := e.gym(t, "learn", clash)
	if code != 1 || !strings.Contains(errs, "lock-frozen: learn "+clash+" would modify "+e.lockPath()) {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if strings.Contains(lkRead(t, filepath.Join(e.skillDir("lint"), "SKILL.md")), "clash") {
		t.Fatal("frozen learn replaced the locked skill")
	}
}

// The global lock is the operator's own and keeps absolute local sources.
func TestGlobalLockKeepsAbsoluteDirSource(t *testing.T) {
	e := newLkEnv(t, "")
	out := lkSkillSrc(t, scratchDir(t), "lint", "g")
	got := e.mustGym(t, "learn", "-g", out)
	if strings.Contains(got, notLockedWarning) {
		t.Fatalf("global learn warned:\n%s", got)
	}
	f, err := lock.Load(filepath.Join(e.dirs.ConfigHome, "lock.json"), lock.ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if ent, ok := f.Find("lint"); !ok || ent.Source != out {
		t.Fatalf("global entry = %+v", f.Skills)
	}
}

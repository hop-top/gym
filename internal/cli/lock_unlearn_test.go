package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
)

func lkHasEntry(t *testing.T, e lkEnv, name string) bool {
	t.Helper()
	_, ok := e.loadLock(t).Find(name)
	return ok
}

// Removing the last record of a skill removes its lock entry, after the
// directory and the state.
func TestUnlearnRemovesLockEntry(t *testing.T) {
	e, _ := lkLocked(t, "")
	e.mustGym(t, "unlearn", "lint")
	if lkHasEntry(t, e, "lint") {
		t.Fatal("lock entry kept")
	}
	if code, out, _ := e.gym(t, "list", "--check"); code != 0 {
		t.Fatalf("list --check after unlearn: exit %d\n%s", code, out)
	}
}

func TestUnlearnKeepLock(t *testing.T) {
	e, _ := lkLocked(t, "")
	e.mustGym(t, "unlearn", "--keep-lock", "lint")
	if !lkHasEntry(t, e, "lint") {
		t.Fatal("--keep-lock dropped the entry")
	}
	// The kept entry now reads as missing, and a restore brings it back.
	if code, out, _ := e.gym(t, "list", "--check"); code != 1 || !strings.Contains(out, "missing") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	e.mustGym(t, "learn")
	if !installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("restore after --keep-lock failed")
	}
}

// A skill that is not removed (locally modified) keeps its entry; so does
// --dry-run.
func TestUnlearnRefusedOrDryRunKeepsLockEntry(t *testing.T) {
	e, _ := lkLocked(t, "")
	e.mustGym(t, "unlearn", "--dry-run", "lint")
	if !lkHasEntry(t, e, "lint") {
		t.Fatal("--dry-run changed the lock")
	}
	p := filepath.Join(e.skillDir("lint"), "SKILL.md")
	if err := os.WriteFile(p, []byte(lkRead(t, p)+"edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := e.gym(t, "unlearn", "lint"); code != 1 || !strings.Contains(errs, "skill-modified") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if !lkHasEntry(t, e, "lint") {
		t.Fatal("a refused removal dropped the lock entry")
	}
	e.mustGym(t, "unlearn", "--force", "lint")
	if lkHasEntry(t, e, "lint") {
		t.Fatal("forced removal kept the lock entry")
	}
}

func TestUnlearnWithoutLockIsFine(t *testing.T) {
	e := newLkEnv(t, "")
	e.mustGym(t, "learn", "--no-lock", lkSkillSrc(t, scratchDir(t), "lint", "a"))
	e.mustGym(t, "unlearn", "lint")
	if _, err := os.Stat(e.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("unlearn created a lock: %v", err)
	}
}

func TestUnlearnCorruptLockFailsAfterRemoval(t *testing.T) {
	e, _ := lkLocked(t, "")
	writeFiles(t, e.proj, map[string]string{".gym/lock.json": "not json"})
	code, _, errs := e.gym(t, "unlearn", "lint")
	if code != 1 || !strings.Contains(errs, "remove-failed") || !strings.Contains(errs, "lock-invalid") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if lkRead(t, e.lockPath()) != "not json" {
		t.Fatal("a corrupt lock was rewritten")
	}
}

func TestUnlearnGlobalRemovesGlobalLockEntry(t *testing.T) {
	e := newLkEnv(t, "")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "a")})
	e.mustGym(t, "learn", "-g", "lint")
	gl := filepath.Join(e.dirs.ConfigHome, "lock.json")
	if f, err := lock.Load(gl, lock.ScopeGlobal); err != nil || len(f.Skills) != 1 {
		t.Fatalf("setup: %v %v", f, err)
	}
	e.mustGym(t, "unlearn", "-g", "lint")
	if f, err := lock.Load(gl, lock.ScopeGlobal); err != nil || len(f.Skills) != 0 {
		t.Fatalf("global lock after unlearn: %v %v", f, err)
	}
}

// --dir: a folder's unlearn edits that folder's lock only.
func TestUnlearnDirEditsThatFoldersLock(t *testing.T) {
	e, _ := lkLocked(t, "")
	other := filepath.Join(scratchDir(t), "other")
	scopeTree(t, other)
	e.mustGym(t, "learn", "--dir", other, lkSkillSrc(t, scratchDir(t), "lint", "o"))
	e.mustGym(t, "unlearn", "--dir", other, "lint")
	if !lkHasEntry(t, e, "lint") {
		t.Fatal("unlearn --dir edited the current folder's lock")
	}
	f, err := lock.Load(lock.ProjectPath(other), lock.ScopeProject)
	if err != nil || len(f.Skills) != 0 {
		t.Fatalf("other lock = %v %v", f, err)
	}
}

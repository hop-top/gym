package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
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

// The rule is about the stored source, not the kind: a registry entry whose
// source_url is a local path outside the root is installed and stays
// untracked, with the same warning; inside the root it is stored relative.
func TestOutOfTreeRegistrySourceIsInstalledButNotLocked(t *testing.T) {
	e := newLkEnv(t, "")
	out := lkSkillSrc(t, scratchDir(t), "lint", "o")
	in := lkSkillSrc(t, filepath.Join(e.proj, "src"), "fmt", "i")
	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: out},
		registry.SkillEntry{Name: "fmt", Version: "1.0.0", SourceURL: in})

	got := e.mustGym(t, "learn", "lint", "fmt")
	if !strings.Contains(got, notLockedWarning+out+" is outside "+e.proj+"; not written to the lock") {
		t.Fatalf("warning missing:\n%s", got)
	}
	if !installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("out-of-tree registry skill not installed")
	}
	if _, ok := e.loadLock(t).Find("lint"); ok {
		t.Fatal("out-of-tree registry source was locked")
	}
	if ent := e.lockEntry(t, "fmt"); ent.Kind != lock.KindRegistry || ent.Source != "src/fmt" {
		t.Fatalf("in-tree entry = %+v", ent)
	}
	if got := e.mustGym(t, "learn"); !strings.Contains(got, "nothing to restore") {
		t.Fatalf("restore:\n%s", got)
	}
}

// An archive file outside the tree installs untracked; inside it is stored
// relative. The global lock keeps an absolute archive path.
func TestArchiveFilePortability(t *testing.T) {
	e := newLkEnv(t, "")
	outArch := makeTarGz(t, scratchDir(t), "outer.tar.gz", "outer/", skillFiles("outer", "o"))
	inArch := makeTarGz(t, filepath.Join(e.proj, "pkgs"), "inner.tar.gz", "inner/", skillFiles("inner", "i"))

	got := e.mustGym(t, "learn", outArch, inArch)
	if !strings.Contains(got, notLockedWarning+outArch+" is outside "+e.proj+"; not written to the lock") {
		t.Fatalf("warning missing:\n%s", got)
	}
	skills := filepath.Join(e.proj, ".gym", "skills")
	if !installed(skills, "outer") || !installed(skills, "inner") {
		t.Fatal("archives not installed")
	}
	if _, ok := e.loadLock(t).Find("outer"); ok {
		t.Fatal("out-of-tree archive was locked")
	}
	if ent := e.lockEntry(t, "inner"); ent.Kind != lock.KindArchive || ent.Source != "pkgs/inner.tar.gz" {
		t.Fatalf("in-tree archive entry = %+v", ent)
	}
	// the lock a reader would refuse never exists: it loads and restores.
	if got := e.mustGym(t, "learn"); !strings.Contains(got, "nothing to restore") {
		t.Fatalf("restore:\n%s", got)
	}

	// global scope: the operator's own lock keeps the absolute path.
	g := e.mustGym(t, "learn", "-g", outArch)
	if strings.Contains(g, notLockedWarning) {
		t.Fatalf("global learn warned:\n%s", g)
	}
	f, err := lock.Load(filepath.Join(e.dirs.ConfigHome, "lock.json"), lock.ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if ent, ok := f.Find("outer"); !ok || ent.Kind != lock.KindArchive || ent.Source != outArch {
		t.Fatalf("global entry = %+v", f.Skills)
	}
}

// train never writes an entry a reader would refuse: when the upgraded
// source is out of tree, the entry is dropped with the warning.
func TestTrainDropsEntryWhoseNewSourceIsOutOfTree(t *testing.T) {
	e := newLkEnv(t, "")
	oldSrc := lkSkillSrc(t, filepath.Join(e.proj, "old"), "lint", "old")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: oldSrc})
	e.mustGym(t, "learn", "lint")
	if _, ok := e.loadLock(t).Find("lint"); !ok {
		t.Fatal("setup: no entry")
	}
	newSrc := lkSkillSrc(t, scratchDir(t), "lint", "new")
	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: oldSrc},
		registry.SkillEntry{Name: "lint", Version: "1.1.0", SourceURL: newSrc})
	got := e.mustGym(t, "train")
	if !strings.Contains(got, "upgraded lint") || !strings.Contains(got, notLockedWarning+newSrc) {
		t.Fatalf("train:\n%s", got)
	}
	if _, ok := e.loadLock(t).Find("lint"); ok {
		t.Fatal("train wrote an out-of-tree source into the project lock")
	}
}

// Frozen: an out-of-tree archive changes nothing in the lock, so it installs.
func TestFrozenOutOfTreeArchive(t *testing.T) {
	frozen := newLkEnv(t, "lock:\n  frozen: true\n")
	got := frozen.mustGym(t, "learn", makeTarGz(t, scratchDir(t), "fresh.tar.gz", "fresh/", skillFiles("fresh", "f")))
	if !strings.Contains(got, notLockedWarning) || !installed(filepath.Join(frozen.proj, ".gym", "skills"), "fresh") {
		t.Fatalf("frozen out-of-tree archive:\n%s", got)
	}
	if _, err := os.Stat(frozen.lockPath()); err == nil {
		t.Fatal("frozen learn created a lock")
	}
}

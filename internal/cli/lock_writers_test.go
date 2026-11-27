package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
)

func TestLearnWritesLockForRegistrySkill(t *testing.T) {
	e := newLkEnv(t, "")
	src := lkSkillSrc(t, scratchDir(t), "lint", "a")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.2.0", SourceURL: src, OriginURL: "https://r.example/skills.json"})
	e.mustGym(t, "learn", "lint")

	got := e.lockEntry(t, "lint")
	digest, err := install.Digest(e.skillDir("lint"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != lock.KindRegistry || got.Version != "1.2.0" || got.Requested != "" ||
		got.Registry != "https://r.example/skills.json" || got.Digest != digest || got.Source != src {
		t.Fatalf("lock entry = %+v (want digest %s)", got, digest)
	}
	if b := lkRead(t, filepath.Join(e.proj, ".gym", ".gitignore")); b != "installed.json\nskills/\n" {
		t.Fatalf(".gitignore = %q", b)
	}
	f := e.loadLock(t)
	if f.Scope != "project" || f.Schema != lock.Schema {
		t.Fatalf("header = %+v", f)
	}
}

func TestLearnNameAtVersionRecordsRequested(t *testing.T) {
	e := newLkEnv(t, "")
	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.2.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "a")},
		registry.SkillEntry{Name: "lint", Version: "1.3.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "b")},
	)
	e.mustGym(t, "learn", "lint@1.2.0")
	if got := e.lockEntry(t, "lint"); got.Requested != "1.2.0" || got.Version != "1.2.0" {
		t.Fatalf("entry = %+v", got)
	}
}

func TestLearnLockIsSortedAndPortableSourcesAreRelative(t *testing.T) {
	e := newLkEnv(t, "")
	inside := lkSkillSrc(t, filepath.Join(e.proj, "mirror"), "zed", "a")
	outside := lkSkillSrc(t, scratchDir(t), "alpha", "a")
	e.mustGym(t, "learn", inside)
	_, _, errs := e.gym(t, "learn", outside)
	if !strings.Contains(errs, "lock-source-not-portable") {
		t.Fatalf("outside source must warn lock-source-not-portable:\n%s", errs)
	}
	f := e.loadLock(t)
	if len(f.Skills) != 2 || f.Skills[0].Name != "alpha" || f.Skills[1].Name != "zed" {
		t.Fatalf("skills = %+v", f.Skills)
	}
	if z, _ := f.Find("zed"); z.Kind != lock.KindDir || z.Source != "mirror/zed" {
		t.Fatalf("inside source = %+v", z)
	}
	if a, _ := f.Find("alpha"); a.Source != outside {
		t.Fatalf("outside source = %+v", a)
	}
}

func TestLearnGitRefRecordsCommit(t *testing.T) {
	lkRequireGit(t)
	e := newLkEnv(t, "")
	repo := filepath.Join(scratchDir(t), "up")
	c1 := lkGitRepo(t, repo, map[string]string{"SKILL.md": skillBody("first")})
	lkGit(t, repo, "tag", "v1.0")
	c2 := lkGitCommit(t, repo, map[string]string{"SKILL.md": skillBody("second")})

	e.mustGym(t, "learn", "file://"+repo+"#v1.0")
	if got := e.lockEntry(t, "up"); got.Kind != lock.KindGit || got.Requested != "v1.0" || got.Commit != c1 {
		t.Fatalf("tag pin entry = %+v, want commit %s", got, c1)
	}
	e.mustGym(t, "learn", "file://"+repo+"#main")
	if got := e.lockEntry(t, "up"); got.Kind != lock.KindGit || got.Requested != "main" || got.Commit != c2 {
		t.Fatalf("branch pin entry = %+v, want commit %s", got, c2)
	}
}

func TestLearnNoLockLeavesLockAlone(t *testing.T) {
	e := newLkEnv(t, "")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "a")},
		registry.SkillEntry{Name: "fmt", Version: "1.0.0", SourceURL: lkSkillSrc(t, scratchDir(t), "fmt", "a")})
	e.mustGym(t, "learn", "lint")
	before := lkRead(t, e.lockPath())

	out := e.mustGym(t, "learn", "--no-lock", "lint", "fmt")
	if !strings.Contains(out, "note: --no-lock: left the lint entry") || strings.Contains(out, "left the fmt entry") {
		t.Fatalf("want a note for lint only:\n%s", out)
	}
	if lkRead(t, e.lockPath()) != before {
		t.Fatal("--no-lock changed the lock")
	}
	if _, ok := e.loadLock(t).Find("fmt"); ok {
		t.Fatal("--no-lock added fmt")
	}
}

func TestLearnNoLockWritesNoLockFile(t *testing.T) {
	e := newLkEnv(t, "")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "a")})
	e.mustGym(t, "learn", "--no-lock", "lint")
	if _, err := os.Stat(e.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("lock written despite --no-lock: %v", err)
	}
}

func TestLearnRefusesCorruptLockBeforeInstalling(t *testing.T) {
	e := newLkEnv(t, "")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "a")})
	writeFiles(t, e.proj, map[string]string{".gym/lock.json": `{"schema":"gym.lock/v9"}`})
	code, _, errs := e.gym(t, "learn", "lint")
	if code != 1 || !strings.Contains(errs, "lock-invalid") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("installed despite a corrupt lock")
	}
}

func TestLearnGlobalWritesLockInConfigDir(t *testing.T) {
	e := newLkEnv(t, "")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "a")})
	e.mustGym(t, "learn", "-g", "lint")
	gl := filepath.Join(e.dirs.ConfigHome, "lock.json")
	f, err := lock.Load(gl, lock.ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Find("lint"); !ok {
		t.Fatalf("global lock = %+v", f)
	}
	if _, err := os.Stat(e.lockPath()); !os.IsNotExist(err) {
		t.Fatal("global learn wrote a project lock")
	}
}

// train rewrites a tracking entry and leaves a requested one alone.
func TestTrainRewritesTrackingEntryAndSkipsPinned(t *testing.T) {
	e := newLkEnv(t, "")
	oldLint := lkSkillSrc(t, scratchDir(t), "lint", "old")
	oldFmt := lkSkillSrc(t, scratchDir(t), "fmt", "old")
	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: oldLint},
		registry.SkillEntry{Name: "fmt", Version: "1.0.0", SourceURL: oldFmt})
	e.mustGym(t, "learn", "lint", "fmt@1.0.0")
	fmtBefore := e.lockEntry(t, "fmt")

	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: oldLint},
		registry.SkillEntry{Name: "lint", Version: "1.1.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "new")},
		registry.SkillEntry{Name: "fmt", Version: "1.0.0", SourceURL: oldFmt},
		registry.SkillEntry{Name: "fmt", Version: "2.0.0", SourceURL: lkSkillSrc(t, scratchDir(t), "fmt", "new")})
	out := e.mustGym(t, "train")

	if !strings.Contains(out, "skip fmt: pinned to 1.0.0 (gym learn fmt@<new> to change)") {
		t.Fatalf("no skip line:\n%s", out)
	}
	if !strings.Contains(out, "upgraded lint") || strings.Contains(out, "upgraded fmt") {
		t.Fatalf("train output:\n%s", out)
	}
	if got := lkRead(t, filepath.Join(e.skillDir("fmt"), "SKILL.md")); !strings.Contains(got, "old") {
		t.Fatalf("pinned fmt was upgraded:\n%s", got)
	}
	lint := e.lockEntry(t, "lint")
	recs, _ := state.LoadProject(e.proj)
	var lintDigest string
	for _, r := range recs {
		if r.Name == "lint" {
			lintDigest = r.Digest
		}
	}
	if lint.Version != "1.1.0" || lint.Requested != "" || lint.Digest != lintDigest {
		t.Fatalf("lint entry = %+v, state digest %s", lint, lintDigest)
	}
	if e.lockEntry(t, "fmt") != fmtBefore {
		t.Fatalf("pinned entry changed: %+v", e.lockEntry(t, "fmt"))
	}
}

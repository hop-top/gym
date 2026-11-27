package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
)

const lkBogus = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// lkLocked learns lint (a registry entry whose source sits inside the
// project, so its lock source is portable) and returns the env and source.
// userCfg is written to the user config once lint is learned.
func lkLocked(t *testing.T, userCfg string) (lkEnv, string) {
	t.Helper()
	e := newLkEnv(t, "")
	src := lkSkillSrc(t, filepath.Join(e.proj, "src"), "lint", "v1")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: src, OriginURL: "https://r.example/skills.json"})
	e.mustGym(t, "learn", "lint")
	if userCfg != "" {
		// Applied after the first learn: a frozen config would refuse it.
		writeUserConfig(t, e.home, userCfg)
	}
	return e, src
}

func lkWriteSkill(t *testing.T, src, tag string) {
	t.Helper()
	writeFiles(t, src, map[string]string{"SKILL.md": "---\nname: lint\ndescription: d\n---\nbody " + tag + "\n"})
}

// B8: a locked skill whose source changed is a hard digest-mismatch: with
// `learn lint` and with a no-argument restore, and nothing is written.
func TestB8LockedSkillChangedUpstreamIsDigestMismatch(t *testing.T) {
	e, src := lkLocked(t, "")
	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	lkWriteSkill(t, src, "tampered")
	lockBefore := lkRead(t, e.lockPath())

	for _, args := range [][]string{{"learn", "lint"}, {"learn"}} {
		code, _, errs := e.gym(t, args...)
		if code != 1 || !strings.Contains(errs, "digest-mismatch") || !strings.Contains(errs, "lockfile pin") {
			t.Fatalf("%v: exit %d\n%s", args, code, errs)
		}
		if installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
			t.Fatalf("%v: wrote the skill despite the mismatch", args)
		}
	}
	if lkRead(t, e.lockPath()) != lockBefore {
		t.Fatal("a rejected install changed the lock")
	}
}

// B8 (git): the lock holds the commit, so a tag that moved afterwards does
// not change what is installed.
func TestB8MovedTagInstallsLockedCommit(t *testing.T) {
	lkRequireGit(t)
	e := newLkEnv(t, "")
	repo := filepath.Join(e.proj, "up")
	c1 := lkGitRepo(t, repo, map[string]string{"SKILL.md": skillBody("first")})
	lkGit(t, repo, "tag", "v1.0")
	e.mustGym(t, "learn", "file://"+repo+"#v1.0")
	if got := e.lockEntry(t, "up"); got.Commit != c1 || got.Source != "up" {
		t.Fatalf("entry = %+v", got)
	}

	c2 := lkGitCommit(t, repo, map[string]string{"SKILL.md": skillBody("second")})
	lkGit(t, repo, "tag", "-f", "v1.0", c2)
	if err := os.RemoveAll(e.skillDir("up")); err != nil {
		t.Fatal(err)
	}
	e.mustGym(t, "learn")
	if got := lkRead(t, filepath.Join(e.skillDir("up"), "SKILL.md")); !strings.Contains(got, "first") {
		t.Fatalf("restore followed the moved tag:\n%s", got)
	}
}

// B9: restore works from the lock alone. The registry index is made
// unreadable (a directory where the file should be), so any read of it fails.
func TestB9RestoreFromLockOffline(t *testing.T) {
	e, _ := lkLocked(t, "repositories:\n  - {name: r, url: \"http://127.0.0.1:1/skills.json\"}\n")
	lockBefore := lkRead(t, e.lockPath())
	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	indexFile := filepath.Join(e.dirs.DataHome, "registry-index.json")
	if err := os.Remove(indexFile); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(indexFile, 0o755); err != nil {
		t.Fatal(err)
	}
	// The trap works: anything that reads the index fails.
	if code, _, _ := e.gym(t, "learn", "lint"); code == 0 {
		t.Fatal("learn <name> must read the index (the trap is not armed)")
	}

	out := e.mustGym(t, "learn")
	if !installed(filepath.Join(e.proj, ".gym", "skills"), "lint") || !strings.Contains(out, "installed lint 1.0.0") {
		t.Fatalf("not restored:\n%s", out)
	}
	if lkRead(t, e.lockPath()) != lockBefore {
		t.Fatal("restore rewrote the lock")
	}
	// Nothing left to do: says so, exit 0.
	if out := e.mustGym(t, "learn"); !strings.Contains(out, "nothing to restore") {
		t.Fatalf("second restore:\n%s", out)
	}
}

func TestLearnNoArgsWithoutLockIsLockMissing(t *testing.T) {
	e := newLkEnv(t, "")
	code, _, errs := e.gym(t, "learn")
	if code != 1 || !strings.Contains(errs, "lock-missing: "+e.lockPath()+" not found") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

func TestLearnNamePrintsUsingLocked(t *testing.T) {
	e, _ := lkLocked(t, "")
	out := e.mustGym(t, "learn", "lint")
	if !strings.Contains(out, "using locked 1.0.0 (gym train to upgrade)") {
		t.Fatalf("output:\n%s", out)
	}
}

// A locked name installs the locked version even when the registry has a
// newer one; name@newer replaces the entry.
func TestLearnNameHonoursLockOverNewerRegistry(t *testing.T) {
	e, src := lkLocked(t, "")
	newer := lkSkillSrc(t, filepath.Join(e.proj, "src2"), "lint", "v2")
	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: src},
		registry.SkillEntry{Name: "lint", Version: "2.0.0", SourceURL: newer})
	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	e.mustGym(t, "learn", "lint")
	if got := lkRead(t, filepath.Join(e.skillDir("lint"), "SKILL.md")); !strings.Contains(got, "body v1") {
		t.Fatalf("lock not honoured:\n%s", got)
	}
	e.mustGym(t, "learn", "lint@2.0.0")
	if got := e.lockEntry(t, "lint"); got.Version != "2.0.0" || got.Requested != "2.0.0" {
		t.Fatalf("entry = %+v", got)
	}
}

// B6 (integration): the lock and the registry pin different digests for the
// same version; the lock wins and a warning is raised. B7: a registry that
// only has a newer version is silent.
func TestB6B7LockRegistryConflictThroughLearn(t *testing.T) {
	e, src := lkLocked(t, "")
	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: src, Digest: lkBogus})
	out := e.mustGym(t, "learn", "lint")
	if !strings.Contains(out, "admission-digest-pin-conflict") || !installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatalf("same-version disagreement must warn and install:\n%s", out)
	}

	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.1.0", SourceURL: src, Digest: lkBogus})
	out = e.mustGym(t, "learn", "lint")
	if strings.Contains(out, "admission-digest-pin-conflict") {
		t.Fatalf("a newer registry version is not a conflict:\n%s", out)
	}
}

// B10: learn --frozen fails on missing, digest-drift and modified; untracked
// alone only warns. It never writes the lock and installs nothing.
func TestB10FrozenVerification(t *testing.T) {
	type scenario struct {
		name   string
		break_ func(t *testing.T, e lkEnv)
		want   string // "" = passes
	}
	statePath := func(e lkEnv) string { return filepath.Join(e.proj, ".gym", "installed.json") }
	cases := []scenario{
		{"clean", func(*testing.T, lkEnv) {}, ""},
		{"missing", func(t *testing.T, e lkEnv) {
			if err := os.RemoveAll(e.skillDir("lint")); err != nil {
				t.Fatal(err)
			}
		}, "missing lint"},
		{"digest-drift", func(t *testing.T, e lkEnv) {
			real := e.lockEntry(t, "lint").Digest
			if err := os.WriteFile(statePath(e), []byte(strings.ReplaceAll(lkRead(t, statePath(e)), real, lkBogus)), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "digest-drift lint"},
		{"modified", func(t *testing.T, e lkEnv) {
			p := filepath.Join(e.skillDir("lint"), "SKILL.md")
			if err := os.WriteFile(p, []byte(lkRead(t, p)+"edit\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "modified lint"},
		{"untracked only", func(t *testing.T, e lkEnv) {
			e.mustGym(t, "learn", "--no-lock", lkSkillSrc(t, scratchDir(t), "extra", "x"))
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := lkLocked(t, "")
			c.break_(t, e)
			lockBefore := lkRead(t, e.lockPath())
			stateBefore := lkRead(t, statePath(e))
			code, out, errs := e.gym(t, "learn", "--frozen")
			if c.want == "" {
				if code != 0 {
					t.Fatalf("exit %d\n%s\n%s", code, out, errs)
				}
				if c.name == "untracked only" && !strings.Contains(errs, "untracked extra") {
					t.Fatalf("want an untracked warning:\n%s", errs)
				}
			} else {
				if code != 1 || !strings.Contains(out, c.want) || !strings.Contains(errs, "lock-drift: 1 of 1 locked skills differ") {
					t.Fatalf("exit %d\n%s\n%s", code, out, errs)
				}
			}
			if lkRead(t, e.lockPath()) != lockBefore || lkRead(t, statePath(e)) != stateBefore {
				t.Fatal("--frozen changed the lock or the state")
			}
			if c.name == "missing" && installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
				t.Fatal("--frozen installed something")
			}
		})
	}
}

func TestFrozenRefusesLockBypassAndChanges(t *testing.T) {
	e, _ := lkLocked(t, "lock:\n  frozen: true\n")
	if code, _, errs := e.gym(t, "learn", "--no-lock", "lint"); code != 1 || !strings.Contains(errs, "lock-frozen") {
		t.Fatalf("--no-lock under frozen: exit %d\n%s", code, errs)
	}
	other := lkSkillSrc(t, filepath.Join(e.proj, "src"), "other", "x")
	code, _, errs := e.gym(t, "learn", other)
	if code != 1 || !strings.Contains(errs, "lock-frozen: learn "+other+" would modify "+e.lockPath()) {
		t.Fatalf("new skill under frozen: exit %d\n%s", code, errs)
	}
	if installed(filepath.Join(e.proj, ".gym", "skills"), "other") {
		t.Fatal("frozen learn installed a new skill")
	}
	// A locked name may be reinstalled: the lock does not change.
	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	e.mustGym(t, "learn", "lint")
	// No arguments under the config is a verification, like --frozen.
	if out := e.mustGym(t, "learn"); !strings.Contains(out, "lock ok") {
		t.Fatalf("frozen config, no args:\n%s", out)
	}
	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := e.gym(t, "learn"); code != 1 || !strings.Contains(errs, "lock-drift") {
		t.Fatalf("frozen config must verify, not restore: exit %d\n%s", code, errs)
	}
}

// A project may turn frozen on for itself.
func TestFrozenFromProjectConfig(t *testing.T) {
	e, _ := lkLocked(t, "")
	writeFiles(t, e.proj, map[string]string{".gym/config.yaml": "lock:\n  frozen: true\n"})
	code, _, errs := e.gym(t, "learn", lkSkillSrc(t, filepath.Join(e.proj, "src"), "other", "x"))
	if code != 1 || !strings.Contains(errs, "lock-frozen") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

// B11: train --frozen and config lock.frozen refuse any upgrade and change
// nothing; with nothing to upgrade they exit 0.
func TestB11TrainFrozen(t *testing.T) {
	for name, args := range map[string][]string{"flag": {"train", "--frozen"}, "config": {"train"}} {
		t.Run(name, func(t *testing.T) {
			cfg := ""
			if name == "config" {
				cfg = "lock:\n  frozen: true\n"
			}
			e, src := lkLocked(t, cfg)
			if code, out, errs := e.gym(t, args...); code != 0 || !strings.Contains(out, "no upgrades available") {
				t.Fatalf("nothing to upgrade: exit %d\n%s\n%s", code, out, errs)
			}
			newer := lkSkillSrc(t, filepath.Join(e.proj, "src2"), "lint", "v2")
			e.index(t,
				registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: src},
				registry.SkillEntry{Name: "lint", Version: "1.1.0", SourceURL: newer})
			lockBefore := lkRead(t, e.lockPath())
			code, _, errs := e.gym(t, args...)
			if code != 1 || !strings.Contains(errs, "lock-frozen: train would modify "+e.lockPath()) {
				t.Fatalf("exit %d\n%s", code, errs)
			}
			if got := lkRead(t, filepath.Join(e.skillDir("lint"), "SKILL.md")); !strings.Contains(got, "body v1") {
				t.Fatalf("frozen train upgraded:\n%s", got)
			}
			if lkRead(t, e.lockPath()) != lockBefore {
				t.Fatal("frozen train changed the lock")
			}
		})
	}
}

// B12: a lock in a cloned-repository-shaped folder cannot make gym install
// from outside it. The target holds a skill whose real digest is in the
// lock, so an install would succeed if the source were read.
func TestB12HostileLockSourceEscapes(t *testing.T) {
	e := newLkEnv(t, "")
	secret := lkSkillSrc(t, scratchDir(t), "lint", "secret")
	digest, err := install.Digest(secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(e.proj, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(e.proj, secret)
	for name, source := range map[string]string{
		"absolute dir": secret,
		"dotdot":       rel,
		"symlink out":  "link",
	} {
		t.Run(name, func(t *testing.T) {
			f := lock.New(lock.ScopeProject)
			f.Set(lock.Entry{Name: "lint", Kind: lock.KindDir, Source: source, Version: "0.0.0", Digest: digest})
			if err := lock.Save(e.lockPath(), f); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"learn"}, {"learn", "lint"}} {
				code, _, errs := e.gym(t, args...)
				if code != 1 || !strings.Contains(errs, "lock-source-escapes: "+source) {
					t.Fatalf("%v: exit %d\n%s", args, code, errs)
				}
				if installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
					t.Fatalf("%v: installed from outside the folder", args)
				}
			}
		})
	}
}

func TestRestoreContinuesPastFailingEntry(t *testing.T) {
	e := newLkEnv(t, "")
	a := lkSkillSrc(t, filepath.Join(e.proj, "src"), "alpha", "a")
	b := lkSkillSrc(t, filepath.Join(e.proj, "src"), "bravo", "b")
	e.mustGym(t, "learn", a, b)
	for _, n := range []string{"alpha", "bravo"} {
		if err := os.RemoveAll(e.skillDir(n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	code, out, errs := e.gym(t, "learn")
	if code != 1 || !strings.Contains(errs, "learn: 1 of 2 locked skills failed") || !strings.Contains(out, "installed bravo") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if !installed(filepath.Join(e.proj, ".gym", "skills"), "bravo") {
		t.Fatal("the healthy entry was not restored")
	}
}

// --digest: the operator's pin, recorded in the lock on success.
func TestLearnDigestOperatorPin(t *testing.T) {
	e := newLkEnv(t, "")
	src := lkSkillSrc(t, filepath.Join(e.proj, "src"), "lint", "v1")
	real, _ := install.Digest(src)

	code, _, errs := e.gym(t, "learn", "--digest", lkBogus, src)
	if code != 1 || !strings.Contains(errs, "digest-mismatch") || !strings.Contains(errs, "operator pin") {
		t.Fatalf("wrong digest: exit %d\n%s", code, errs)
	}
	if installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("wrong --digest installed")
	}
	for _, args := range [][]string{
		{"learn", "--digest", "sha256:zz", src},
		{"learn", "--digest", real},
		{"learn", "--digest", real, src, src},
	} {
		if code, _, errs := e.gym(t, args...); code != 1 || errs == "" {
			t.Fatalf("%v: exit %d\n%s", args, code, errs)
		}
	}

	e.mustGym(t, "learn", "--digest", real, src)
	if got := e.lockEntry(t, "lint"); got.Digest != real {
		t.Fatalf("entry = %+v", got)
	}
}

// list --check reports the drift kinds; exit 1 on missing, digest-drift and
// modified, 0 on untracked alone; JSON form.
func TestListCheck(t *testing.T) {
	e, _ := lkLocked(t, "")
	if code, out, _ := e.gym(t, "list", "--check"); code != 0 || !strings.Contains(out, "lock ok: 1 locked skills match") {
		t.Fatalf("clean: exit %d\n%s", code, out)
	}

	e.mustGym(t, "learn", "--no-lock", lkSkillSrc(t, scratchDir(t), "extra", "x"))
	code, out, _ := e.gym(t, "list", "--check")
	if code != 0 || !strings.Contains(out, "untracked") || !strings.Contains(out, "extra") {
		t.Fatalf("untracked only: exit %d\n%s", code, out)
	}

	p := filepath.Join(e.skillDir("lint"), "SKILL.md")
	if err := os.WriteFile(p, []byte(lkRead(t, p)+"edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := e.gym(t, "list", "--check")
	if code != 1 || !strings.Contains(out, "modified") || !strings.Contains(errs, "lock-drift") {
		t.Fatalf("modified: exit %d\n%s\n%s", code, out, errs)
	}

	code, out, _ = e.gym(t, "list", "--check", "--format", "json")
	var rep struct {
		Scope    string `json:"scope"`
		Lock     string `json:"lock"`
		OK       bool   `json:"ok"`
		Findings []struct{ Name, Status, Detail string }
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, f := range rep.Findings {
		got[f.Name] = f.Status
	}
	if code != 1 || rep.OK || rep.Scope != "project" || rep.Lock != e.lockPath() || got["lint"] != "modified" || got["extra"] != "untracked" {
		t.Fatalf("exit %d report %+v", code, rep)
	}

	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := e.gym(t, "list", "--check"); code != 1 || !strings.Contains(out, "missing") {
		t.Fatalf("missing: exit %d\n%s", code, out)
	}
}

func TestListCheckErrors(t *testing.T) {
	e := newLkEnv(t, "")
	writeFiles(t, e.proj, map[string]string{".gym/installed.json": `{"skills":[]}`})
	if code, _, errs := e.gym(t, "list", "--check"); code != 1 || !strings.Contains(errs, "lock-missing") {
		t.Fatalf("no lock: exit %d\n%s", code, errs)
	}
	if code, _, errs := e.gym(t, "list", "--check", "--effective"); code != 1 || !strings.Contains(errs, "mutually exclusive") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

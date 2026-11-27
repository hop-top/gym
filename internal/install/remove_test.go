package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/state"
)

type removeFixture struct {
	root, skills string
	rec          state.InstalledSkill
	all          []state.InstalledSkill
	saved        [][]state.InstalledSkill
	gone         []string
	r            *Remover
}

func newRemoveFixture(t *testing.T) *removeFixture {
	t.Helper()
	f := &removeFixture{root: filepath.Join(t.TempDir(), "proj")}
	f.skills = filepath.Join(f.root, ".gym", "skills")
	dir := filepath.Join(f.skills, "lint")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	dg, err := Digest(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.rec = state.InstalledSkill{Name: "lint", InstallPath: dir, Digest: dg}
	f.all = []state.InstalledSkill{f.rec}
	f.r = &Remover{
		Scope: "project", ProjectRoot: f.root,
		SaveState:   func(s []state.InstalledSkill) error { f.saved = append(f.saved, s); return nil },
		OnSkillGone: func(name string) error { f.gone = append(f.gone, name); return nil },
	}
	return f
}

func TestRemoveSkill_RemovesDirThenRecord(t *testing.T) {
	f := newRemoveFixture(t)
	var order []string
	f.r.RemoveAll = func(p string) error { order = append(order, "dir"); return os.RemoveAll(p) }
	save := f.r.SaveState
	f.r.SaveState = func(s []state.InstalledSkill) error { order = append(order, "state"); return save(s) }
	left, res := f.r.RemoveSkill("lint", f.all, f.all)
	if len(res.Failures) != 0 || len(left) != 0 || len(res.Removed) != 1 {
		t.Fatalf("left=%v res=%+v", left, res)
	}
	if strings.Join(order, ",") != "dir,state" {
		t.Fatalf("order = %v, want dir before state", order)
	}
	if _, err := os.Lstat(f.rec.InstallPath); !os.IsNotExist(err) {
		t.Fatal("directory not removed")
	}
}

func TestRemoveSkill_UntrustedNeverRemoved(t *testing.T) {
	f := newRemoveFixture(t)
	victim := filepath.Join(filepath.Dir(f.root), "victim", "lint")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	called := false
	f.r.RemoveAll = func(string) error { called = true; return nil }
	rec := f.rec
	rec.InstallPath = victim
	_, res := f.r.RemoveSkill("lint", []state.InstalledSkill{rec}, []state.InstalledSkill{rec})
	if called {
		t.Fatal("RemoveAll reached for an untrusted record")
	}
	if len(res.Failures) != 1 || res.Failures[0].Reason != ReasonInstallPathUntrusted {
		t.Fatalf("res = %+v", res)
	}
	if len(f.saved) != 0 || len(f.gone) != 0 {
		t.Fatal("state or hook touched for a failed record")
	}
}

func TestRemoveSkill_ModifiedNeedsForce(t *testing.T) {
	f := newRemoveFixture(t)
	if err := os.WriteFile(filepath.Join(f.rec.InstallPath, "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	left, res := f.r.RemoveSkill("lint", f.all, f.all)
	if len(res.Failures) != 1 || res.Failures[0].Reason != ReasonSkillModified ||
		!strings.Contains(res.Failures[0].Err.Error(), "differs from recorded "+f.rec.Digest) ||
		!strings.Contains(res.Failures[0].Err.Error(), "--force") {
		t.Fatalf("res = %+v", res)
	}
	if len(left) != 1 || !exists(f.rec.InstallPath) {
		t.Fatal("modified skill removed without --force")
	}
	f.r.Force = true
	left, res = f.r.RemoveSkill("lint", f.all, f.all)
	if len(res.Failures) != 0 || len(left) != 0 || exists(f.rec.InstallPath) {
		t.Fatalf("--force did not remove: left=%v res=%+v", left, res)
	}
}

func TestRemoveSkill_NoRecordedDigestUnverifiable(t *testing.T) {
	f := newRemoveFixture(t)
	rec := f.rec
	rec.Digest = ""
	_, res := f.r.RemoveSkill("lint", []state.InstalledSkill{rec}, []state.InstalledSkill{rec})
	if len(res.Failures) != 1 || res.Failures[0].Reason != ReasonSkillModified ||
		!strings.Contains(res.Failures[0].Err.Error(), "no recorded digest") {
		t.Fatalf("res = %+v", res)
	}
	if !exists(f.rec.InstallPath) {
		t.Fatal("unverifiable skill removed without --force")
	}
}

func TestRemoveSkill_AbsentDirCountsAsRemoved(t *testing.T) {
	f := newRemoveFixture(t)
	if err := os.RemoveAll(f.rec.InstallPath); err != nil {
		t.Fatal(err)
	}
	left, res := f.r.RemoveSkill("lint", f.all, f.all)
	if len(res.Failures) != 0 || len(left) != 0 {
		t.Fatalf("left=%v res=%+v", left, res)
	}
}

func TestRemoveSkill_FailureKeepsRecordAndSkipsHook(t *testing.T) {
	f := newRemoveFixture(t)
	f.r.RemoveAll = func(string) error { return errors.New("boom") }
	left, res := f.r.RemoveSkill("lint", f.all, f.all)
	if len(res.Failures) != 1 || res.Failures[0].Reason != ReasonRemoveFailed {
		t.Fatalf("res = %+v", res)
	}
	if len(left) != 1 || len(f.saved) != 0 || len(f.gone) != 0 {
		t.Fatalf("left=%v saved=%d gone=%v", left, len(f.saved), f.gone)
	}
}

// The lock seam: fires once, with the skill name, only when no record of
// that name remains in the scope.
func TestRemoveSkill_OnSkillGoneOnlyWhenLastRecord(t *testing.T) {
	f := newRemoveFixture(t)
	second := filepath.Join(f.root, "other", "lint")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec2 := state.InstalledSkill{Name: "lint", InstallPath: second, Digest: f.rec.Digest}
	f.r.Parents = []string{filepath.Dir(second)}
	all := []state.InstalledSkill{f.rec, rec2}
	left, res := f.r.RemoveSkill("lint", []state.InstalledSkill{f.rec}, all)
	if len(res.Failures) != 0 || len(left) != 1 || len(f.gone) != 0 {
		t.Fatalf("partial removal: left=%v gone=%v res=%+v", left, f.gone, res)
	}
	left, res = f.r.RemoveSkill("lint", []state.InstalledSkill{rec2}, left)
	if len(res.Failures) != 0 || len(left) != 0 || len(f.gone) != 1 || f.gone[0] != "lint" {
		t.Fatalf("last removal: left=%v gone=%v res=%+v", left, f.gone, res)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

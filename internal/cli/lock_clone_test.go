package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// freshClone is a locked project as a clone sees it: the lock and its
// .gitignore are committed; installed.json and skills/ are not.
func freshClone(t *testing.T) lkEnv {
	t.Helper()
	e, _ := lkLocked(t, "")
	for _, p := range []string{filepath.Join(e.proj, ".gym", "installed.json"), filepath.Join(e.proj, ".gym", "skills")} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// list --check on a fresh clone checks the committed lock, from the
// project folder and from any folder below it.
func TestListCheckFreshClone(t *testing.T) {
	e := freshClone(t)
	sub := filepath.Join(e.proj, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{e.proj, sub} {
		code, out, errs := runGym(t, cwd, "list", "--check")
		if code != 1 || !strings.Contains(out, "missing") ||
			!strings.Contains(errs, "lock-drift: 1 of 1 locked skills differ from "+e.lockPath()) {
			t.Fatalf("from %s: exit %d\n%s\n%s", cwd, code, out, errs)
		}
	}

	code, out, _ := runGym(t, sub, "list", "--check", "--format", "json")
	var rep struct {
		Scope string `json:"scope"`
		Lock  string `json:"lock"`
		OK    bool   `json:"ok"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if code != 1 || rep.OK || rep.Scope != "project" || rep.Lock != e.lockPath() {
		t.Fatalf("exit %d report %+v", code, rep)
	}
}

// With no scope and no lock at or above the folder, list --check names
// the missing scope.
func TestListCheckNoScopeNoLock(t *testing.T) {
	e := newLkEnv(t, "")
	code, _, errs := e.gym(t, "list", "--check")
	if code != 1 || !strings.Contains(errs, "no folder scope at or above "+e.proj) {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

// A lock-only ancestor whose .gym is writable by others is not read.
func TestListCheckSkipsUntrustedLockAncestor(t *testing.T) {
	e := freshClone(t)
	gymDir := filepath.Join(e.proj, ".gym")
	if err := os.Chmod(gymDir, 0o775); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(gymDir, 0o755) })
	sub := filepath.Join(e.proj, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runGym(t, sub, "list", "--check")
	if code != 1 || !strings.Contains(errs, "no folder scope at or above "+sub) ||
		!strings.Contains(errs, "scope: ignoring "+gymDir+" (not owned by you or writable by others)") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

// The nearest scope root wins over a lock-only folder above it.
func TestListCheckScopeRootWinsOverLockAbove(t *testing.T) {
	e := freshClone(t)
	inner := filepath.Join(e.proj, "inner")
	scopeTree(t, inner)
	code, _, errs := runGym(t, inner, "list", "--check")
	if code != 1 || !strings.Contains(errs, "lock-missing") || !strings.Contains(errs, filepath.Join(inner, ".gym", "lock.json")) {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

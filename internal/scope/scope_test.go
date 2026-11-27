package scope

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mkRoot makes dir a scope root (marker file .gym/installed.json).
func mkRoot(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".gym"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gym", "installed.json"), []byte(`{"skills":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// MkdirAll honours the umask; make the mode explicit.
	if err := os.Chmod(filepath.Join(dir, ".gym"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestChainInnermostFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	org := filepath.Join(home, "org")
	unit := filepath.Join(org, "unit")
	proj := filepath.Join(unit, "proj")
	sub := filepath.Join(proj, "sub", "deep")
	for _, d := range []string{org, proj} {
		mkRoot(t, d)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	res := Chain(sub)
	want := []string{proj, org}
	if !reflect.DeepEqual(res.Roots, want) {
		t.Fatalf("Roots = %v, want %v", res.Roots, want)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}
}

func TestChainNoRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := filepath.Join(home, "a", "b")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if res := Chain(d); len(res.Roots) != 0 {
		t.Fatalf("Roots = %v, want none", res.Roots)
	}
}

// A6: a root above $HOME is never read; $HOME itself is.
func TestChainStopsAtHome(t *testing.T) {
	top := t.TempDir()
	home := filepath.Join(top, "home")
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	mkRoot(t, top)  // above HOME: ignored
	mkRoot(t, home) // HOME: inclusive
	mkRoot(t, proj)

	res := Chain(proj)
	want := []string{proj, home}
	if !reflect.DeepEqual(res.Roots, want) {
		t.Fatalf("Roots = %v, want %v (root above HOME must be ignored)", res.Roots, want)
	}
}

// Outside $HOME the walk runs to the filesystem root.
func TestChainOutsideHomeWalksUp(t *testing.T) {
	top := t.TempDir()
	home := filepath.Join(top, "home")
	other := filepath.Join(top, "other")
	deep := filepath.Join(other, "deep")
	for _, d := range []string{home, deep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	mkRoot(t, other)

	res := Chain(deep)
	if want := []string{other}; !reflect.DeepEqual(res.Roots, want) {
		t.Fatalf("Roots = %v, want %v", res.Roots, want)
	}
}

// A5: an ancestor whose .gym is group- or world-writable is skipped with
// the specified warning; the nearer and farther trusted roots remain.
func TestChainSkipsWritableAncestor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	org := filepath.Join(home, "org")
	unit := filepath.Join(org, "unit")
	proj := filepath.Join(unit, "proj")
	for _, d := range []string{org, unit, proj} {
		mkRoot(t, d)
	}
	for _, mode := range []os.FileMode{0o775, 0o757} {
		if err := os.Chmod(filepath.Join(unit, ".gym"), mode); err != nil {
			t.Fatal(err)
		}
		res := Chain(proj)
		if want := []string{proj, org}; !reflect.DeepEqual(res.Roots, want) {
			t.Fatalf("mode %o: Roots = %v, want %v", mode, res.Roots, want)
		}
		wantW := "scope: ignoring " + unit + "/.gym (not owned by you or writable by others)"
		if !reflect.DeepEqual(res.Warnings, []string{wantW}) {
			t.Fatalf("mode %o: Warnings = %q, want %q", mode, res.Warnings, wantW)
		}
	}
}

// A5: an ancestor not owned by the current user is skipped. A foreign-owned
// directory cannot be created without root, so the owner lookup is injected.
func TestChainSkipsForeignOwnedAncestor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	org := filepath.Join(home, "org")
	proj := filepath.Join(org, "proj")
	for _, d := range []string{org, proj} {
		mkRoot(t, d)
	}
	orig := ownedByMe
	t.Cleanup(func() { ownedByMe = orig })
	ownedByMe = func(fi os.FileInfo) bool { return false }

	res := Chain(proj)
	// The starting folder is the operator's own choice and is not tested.
	if want := []string{proj}; !reflect.DeepEqual(res.Roots, want) {
		t.Fatalf("Roots = %v, want %v", res.Roots, want)
	}
	wantW := "scope: ignoring " + org + "/.gym (not owned by you or writable by others)"
	if !reflect.DeepEqual(res.Warnings, []string{wantW}) {
		t.Fatalf("Warnings = %q, want %q", res.Warnings, wantW)
	}
}

// The folder the operator points at is read even when writable.
func TestChainStartFolderNotChecked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(home, "proj")
	mkRoot(t, proj)
	if err := os.Chmod(filepath.Join(proj, ".gym"), 0o777); err != nil {
		t.Fatal(err)
	}
	res := Chain(proj)
	if !reflect.DeepEqual(res.Roots, []string{proj}) || len(res.Warnings) != 0 {
		t.Fatalf("got %+v", res)
	}
}

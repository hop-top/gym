package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/approvals"
	"hop.top/gym/internal/install"
)

func skillDir(t *testing.T, parent, name string) string {
	t.Helper()
	d := filepath.Join(parent, name)
	writeFiles(t, d, map[string]string{
		"SKILL.md": "---\nname: " + name + "\ndescription: d\n---\nbody\n",
	})
	return d
}

func TestGuardApprove_DirectoryRecordsComputedDigest(t *testing.T) {
	home := isolate(t)
	dir := skillDir(t, t.TempDir(), "lint")
	want, err := install.Digest(dir)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs := runGym(t, home, "guard", "approve", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errs)
	}
	st, _ := approvals.Open("")
	if got, err := st.Lookup("lint", want); err != nil || got != approvals.Approved {
		t.Fatalf("lookup = %s, %v", got, err)
	}
	if !strings.Contains(st.Path, filepath.Join(home, ".config", "gym")) {
		t.Errorf("approvals path %s not under isolated user config", st.Path)
	}
}

func TestGuardApprove_NameNeedsDigest(t *testing.T) {
	home := isolate(t)
	if code, _, _ := runGym(t, home, "guard", "approve", "lint"); code == 0 {
		t.Fatal("approve by bare name without --digest succeeded")
	}
	d := "sha256:" + strings.Repeat("a", 64)
	if code, out, errs := runGym(t, home, "guard", "approve", "lint", "--digest", d); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errs)
	}
	st, _ := approvals.Open("")
	if got, _ := st.Lookup("lint", d); got != approvals.Approved {
		t.Fatalf("state = %s", got)
	}
	if code, _, _ := runGym(t, home, "guard", "approve", "lint", "--digest", "nope"); code == 0 {
		t.Fatal("malformed digest accepted")
	}
}

func TestGuardApprove_DigestMismatchWithDirRejected(t *testing.T) {
	home := isolate(t)
	dir := skillDir(t, t.TempDir(), "lint")
	d := "sha256:" + strings.Repeat("b", 64)
	if code, _, _ := runGym(t, home, "guard", "approve", dir, "--digest", d); code == 0 {
		t.Fatal("approve accepted a --digest that differs from the directory digest")
	}
}

func TestGuardRevoke_NameAndDigest(t *testing.T) {
	home := isolate(t)
	d := "sha256:" + strings.Repeat("c", 64)
	if code, _, errs := runGym(t, home, "guard", "approve", "lint", "--digest", d); code != 0 {
		t.Fatal(errs)
	}
	if code, _, errs := runGym(t, home, "guard", "revoke", "lint"); code != 0 {
		t.Fatal(errs)
	}
	st, _ := approvals.Open("")
	if got, _ := st.Lookup("lint", d); got != approvals.Revoked {
		t.Fatalf("state = %s; want revoked", got)
	}
	d2 := "sha256:" + strings.Repeat("d", 64)
	if code, _, errs := runGym(t, home, "guard", "revoke", "other", "--digest", d2); code != 0 {
		t.Fatal(errs)
	}
	if got, _ := st.Lookup("anything", d2); got != approvals.Revoked {
		t.Fatalf("digest revoke state = %s", got)
	}
}

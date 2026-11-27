package approvals_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/gym/internal/approvals"
)

const (
	d1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	d2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func newStore(t *testing.T) *approvals.Store {
	t.Helper()
	s, err := approvals.Open(filepath.Join(t.TempDir(), "gym", "approvals.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func lookup(t *testing.T, s *approvals.Store, skill, digest string) approvals.State {
	t.Helper()
	st, err := s.Lookup(skill, digest)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	return st
}

// Case 22 (store half): none / matching digest / other digest / revoked.
func TestLookup_NoneMatchingOtherRevoked(t *testing.T) {
	s := newStore(t)
	if got := lookup(t, s, "lint", d1); got != approvals.Unreviewed {
		t.Fatalf("none = %s", got)
	}
	if err := s.Approve("lint", "https://x", d1, "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := lookup(t, s, "lint", d1); got != approvals.Approved {
		t.Fatalf("matching = %s", got)
	}
	if got := lookup(t, s, "lint", d2); got != approvals.Unreviewed {
		t.Fatalf("other digest = %s; content change must return to unreviewed", got)
	}
	if got := lookup(t, s, "other", d1); got != approvals.Unreviewed {
		t.Fatalf("other skill = %s", got)
	}
	if err := s.Revoke("lint", "", "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{d1, d2} {
		if got := lookup(t, s, "lint", d); got != approvals.Revoked {
			t.Fatalf("name revoke at %s = %s", d, got)
		}
	}
	if got := lookup(t, s, "other", d1); got != approvals.Unreviewed {
		t.Fatalf("revoking lint revoked other = %s", got)
	}
}

func TestLookup_RevokeByDigest(t *testing.T) {
	s := newStore(t)
	if err := s.Approve("lint", "", d1, "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Approve("lint", "", d2, "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke("lint", d1, "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := lookup(t, s, "lint", d1); got != approvals.Revoked {
		t.Fatalf("revoked digest = %s", got)
	}
	if got := lookup(t, s, "lint", d2); got != approvals.Approved {
		t.Fatalf("other digest = %s", got)
	}
	// digest-level revocation blocks the digest under any name
	if got := lookup(t, s, "renamed", d1); got != approvals.Revoked {
		t.Fatalf("revoked digest under other name = %s", got)
	}
}

func TestApprove_LiftsRevocation(t *testing.T) {
	s := newStore(t)
	if err := s.Revoke("lint", "", "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Approve("lint", "", d1, "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := lookup(t, s, "lint", d1); got != approvals.Approved {
		t.Fatalf("after re-approve = %s", got)
	}
}

func TestStore_RecordShapeAndMode(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	if err := s.Approve("lint", "https://x/lint", d1, "jo", at); err != nil {
		t.Fatal(err)
	}
	recs, err := s.Records()
	if err != nil || len(recs) != 1 {
		t.Fatalf("records = %v, %v", recs, err)
	}
	r := recs[0]
	if r.Skill != "lint" || r.SourceURL != "https://x/lint" || r.Digest != d1 || r.State != approvals.Approved || r.By != "jo" || r.At != "2026-11-27T10:00:00Z" {
		t.Fatalf("record = %+v", r)
	}
	st, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v; want 0600", st.Mode().Perm())
	}
}

func TestLookup_MalformedFileFailsClosed(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup("lint", d1); err == nil || !strings.Contains(err.Error(), "approvals") {
		t.Fatalf("err = %v; want parse error", err)
	}
}

func TestDefaultPath_UnderUserConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	p, err := approvals.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(home, ".config", "gym", "approvals.json") {
		t.Fatalf("path = %s", p)
	}
}

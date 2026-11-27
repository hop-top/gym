package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
)

func writeAdmissionConfig(t *testing.T, home, admission string) {
	t.Helper()
	cfg := "validator:\n  command: [sh, -c, \"true\"]\n  timeout_seconds: 5\nadmission:\n" + admission
	if err := os.WriteFile(filepath.Join(home, ".config", "gym", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// B14 (events half): require_pin demands a pin, the install is rejected with
// reason digest-required once, and the requested payload says why.
func TestB14RequirePinRejectedPublishesDigestRequired(t *testing.T) {
	home := isolateInternal(t)
	writeAdmissionConfig(t, home, "  require_pin: all\n")
	ba := newBusApp(t, home, true)
	src := skillTree(t, "lint", "")
	dest := scratch(t)
	if err := ba.learn(t, src, dest); err == nil {
		t.Fatal("expected a digest-required rejection")
	}
	rej, req := ba.snapshot()
	if len(rej) != 1 || rej[0].Reason != "digest-required" || rej[0].Skill != "lint" {
		t.Fatalf("rejected = %+v", rej)
	}
	if len(req) != 1 || req[0].DigestStatus != install.DigestUnpinnedRequired || req[0].SourceKind != "dir" || req[0].LockPresent {
		t.Fatalf("requested = %+v", req)
	}
	if _, err := os.Stat(filepath.Join(dest, "lint")); !os.IsNotExist(err) {
		t.Fatal("rejected install wrote files")
	}
}

// lock-drift and lock-source-escapes are install attempts: each publishes one
// rejected event with its reason.
func TestLockRejectionsPublishOneEventEach(t *testing.T) {
	home := isolateInternal(t)
	proj := filepath.Join(scratch(t), "proj")
	src := skillTree(t, "lint", "")
	digest, err := install.Digest(src)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(source string) {
		f := lock.New(lock.ScopeProject)
		f.Set(lock.Entry{Name: "lint", Kind: lock.KindDir, Source: source, Version: "0.0.0", Digest: digest})
		if err := lock.Save(lock.ProjectPath(proj), f); err != nil {
			t.Fatal(err)
		}
	}

	// Escaping source: restore refuses.
	mk(src)
	ba := newBusApp(t, proj, true)
	if err := ba.app.runLearnOpts(context.Background(), learnOpts{}); err == nil {
		t.Fatal("expected restore failure")
	}
	time.Sleep(150 * time.Millisecond)
	if rej, _ := ba.snapshot(); len(rej) != 1 || rej[0].Reason != "lock-source-escapes" || rej[0].Skill != "lint" || rej[0].Scope != "project" {
		t.Fatalf("restore rejected = %+v", rej)
	}

	// Missing install under --frozen: lock-drift.
	mk("src")
	ba = newBusApp(t, proj, true)
	err = ba.app.runLearnOpts(context.Background(), learnOpts{Frozen: true})
	time.Sleep(150 * time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "lock-drift") {
		t.Fatalf("frozen err = %v", err)
	}
	if rej, _ := ba.snapshot(); len(rej) != 1 || rej[0].Reason != "lock-drift" || rej[0].Skill != "lint" {
		t.Fatalf("frozen rejected = %+v", rej)
	}
	_ = home
}

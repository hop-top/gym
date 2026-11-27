package cli

import (
	"os"
	"path/filepath"
	"testing"

	"hop.top/gym/internal/install"
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

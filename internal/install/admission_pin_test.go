package install_test

import (
	"strings"
	"testing"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
)

const bogusDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func lockAt(version, digest string) *lock.Entry {
	return &lock.Entry{Name: "lint", Kind: lock.KindRegistry, Source: "s", Version: version, Digest: digest}
}

func TestExpectedDigestOrder(t *testing.T) {
	const reg = "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"
	const lk = "sha256:" + "2222222222222222222222222222222222222222222222222222222222222222"
	cases := []struct {
		name         string
		entry        registry.SkillEntry
		lock         *lock.Entry
		wantDigest   string
		wantSource   string
		wantConflict bool
	}{
		{"none", registry.SkillEntry{Version: "1.0.0"}, nil, "", "", false},
		{"registry only", registry.SkillEntry{Version: "1.0.0", Digest: reg}, nil, reg, "registry", false},
		{"lock only", registry.SkillEntry{Version: "1.0.0"}, lockAt("1.0.0", lk), lk, "lockfile", false},
		{"lock beats registry, same digest", registry.SkillEntry{Version: "1.0.0", Digest: lk}, lockAt("1.0.0", lk), lk, "lockfile", false},
		// B6: same version, different digests.
		{"same version differs", registry.SkillEntry{Version: "1.2.0", Digest: reg}, lockAt("1.2.0", lk), lk, "lockfile", true},
		// B7: the registry moved on to a newer version: not a conflict.
		{"registry newer", registry.SkillEntry{Version: "1.3.0", Digest: reg}, lockAt("1.2.0", lk), lk, "lockfile", false},
		{"registry older", registry.SkillEntry{Version: "1.1.0", Digest: reg}, lockAt("1.2.0", lk), lk, "lockfile", false},
		{"lock without digest falls through", registry.SkillEntry{Version: "1.0.0", Digest: reg}, &lock.Entry{Version: "1.0.0"}, reg, "registry", false},
	}
	for _, c := range cases {
		d, s, conflict := install.ExpectedDigest(c.entry, c.lock)
		if d != c.wantDigest || s != c.wantSource || conflict != c.wantConflict {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", c.name, d, s, conflict, c.wantDigest, c.wantSource, c.wantConflict)
		}
	}
}

// B5: through Admit the order is operator > lock > registry, and the
// digest_source fact names the winner.
func TestB5AdmitPinPrecedence(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	real, err := install.Digest(dir)
	if err != nil {
		t.Fatal(err)
	}
	withEntryDigest := func(d string) func(*install.AdmissionInput) {
		return func(in *install.AdmissionInput) { in.Entry.Digest = d; in.Entry.Version = "1.0.0" }
	}

	// Operator pin matches while lock and registry both name something else.
	res, err := admit(t, dir, func(in *install.AdmissionInput) {
		withEntryDigest(bogusDigest)(in)
		in.Lock = lockAt("1.0.0", bogusDigest)
		in.ExpectDigest = real
	})
	if err != nil || res.DigestSource != "operator" || res.DigestStatus != install.DigestMatch || res.DigestPinConflict {
		t.Fatalf("operator: %+v %v", res, err)
	}

	// Lock matches while the registry names something else: the lock wins.
	res, err = admit(t, dir, func(in *install.AdmissionInput) {
		withEntryDigest(bogusDigest)(in)
		in.Lock = lockAt("1.0.0", real)
	})
	if err != nil || res.DigestSource != "lockfile" || res.DigestStatus != install.DigestMatch {
		t.Fatalf("lock over registry: %+v %v", res, err)
	}

	// Registry alone.
	res, err = admit(t, dir, withEntryDigest(real))
	if err != nil || res.DigestSource != "registry" || res.DigestStatus != install.DigestMatch {
		t.Fatalf("registry: %+v %v", res, err)
	}

	// The operator pin is checked even when the lock would pass.
	_, err = admit(t, dir, func(in *install.AdmissionInput) {
		in.Lock = lockAt("1.0.0", real)
		in.ExpectDigest = bogusDigest
	})
	wantReason(t, err, install.ReasonDigestMismatch)
	if !strings.Contains(err.Error(), "operator pin") {
		t.Fatalf("mismatch must name the operator pin: %v", err)
	}
}

// B6 (case 11): lock and registry disagree about the same version: the lock
// wins and the conflict is a fact, not an error.
func TestB6AdmitLockRegistryConflict(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	real, _ := install.Digest(dir)
	res, err := admit(t, dir, func(in *install.AdmissionInput) {
		in.Entry.Version, in.Entry.Digest = "1.2.0", bogusDigest
		in.Lock = lockAt("1.2.0", real)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DigestPinConflict || res.DigestSource != "lockfile" || res.DigestStatus != install.DigestMatch || !res.LockPresent {
		t.Fatalf("facts = %+v", res)
	}
}

// B7: the registry's digest is for a newer version: no conflict.
func TestB7AdmitRegistryNewerIsNotAConflict(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	real, _ := install.Digest(dir)
	res, err := admit(t, dir, func(in *install.AdmissionInput) {
		in.Entry.Version, in.Entry.Digest = "1.3.0", bogusDigest
		in.Lock = lockAt("1.2.0", real)
	})
	if err != nil || res.DigestPinConflict || res.DigestSource != "lockfile" {
		t.Fatalf("facts = %+v err = %v", res, err)
	}
}

// A lock pin that the content does not match is the hard digest-mismatch.
func TestAdmitLockMismatchIsHard(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	_, err := admit(t, dir, func(in *install.AdmissionInput) { in.Lock = lockAt("1.0.0", bogusDigest) })
	wantReason(t, err, install.ReasonDigestMismatch)
	if !strings.Contains(err.Error(), "lockfile pin") {
		t.Fatalf("mismatch must name the lockfile: %v", err)
	}
}

// B14 (facts half): require_pin decides unpinned vs unpinned-required by
// source kind; any pin satisfies it.
func TestB14AdmitRequirePin(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	real, _ := install.Digest(dir)
	status := func(mode, kind string, mod func(*install.AdmissionInput)) string {
		t.Helper()
		res, err := admit(t, dir, func(in *install.AdmissionInput) {
			in.Admission.RequirePin = mode
			in.SourceKind = kind
			if mod != nil {
				mod(in)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		return res.DigestStatus
	}
	cases := []struct {
		mode, kind, want string
	}{
		{"", "registry", install.DigestUnpinned},
		{config.PinNone, "registry", install.DigestUnpinned},
		{config.PinRemote, "registry", install.DigestUnpinnedRequired},
		{config.PinRemote, "git", install.DigestUnpinnedRequired},
		{config.PinRemote, "archive", install.DigestUnpinnedRequired},
		{config.PinRemote, "dir", install.DigestUnpinned},
		{config.PinRemote, "", install.DigestUnpinned},
		{config.PinAll, "dir", install.DigestUnpinnedRequired},
		{config.PinAll, "registry", install.DigestUnpinnedRequired},
	}
	for _, c := range cases {
		if got := status(c.mode, c.kind, nil); got != c.want {
			t.Errorf("require_pin=%q kind=%q: status %q, want %q", c.mode, c.kind, got, c.want)
		}
	}
	for name, mod := range map[string]func(*install.AdmissionInput){
		"operator": func(in *install.AdmissionInput) { in.ExpectDigest = real },
		"lock":     func(in *install.AdmissionInput) { in.Lock = lockAt("1.0.0", real) },
		"registry": func(in *install.AdmissionInput) { in.Entry.Digest = real },
	} {
		if got := status(config.PinAll, "registry", mod); got != install.DigestMatch {
			t.Errorf("%s pin under require_pin=all: status %q, want match", name, got)
		}
	}
}

func TestAdmitReportsSourceFacts(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	res, err := admit(t, dir, func(in *install.AdmissionInput) {
		in.SourceKind, in.Requested, in.Commit = "git", "v1.0", strings.Repeat("a", 40)
	})
	if err != nil || res.SourceKind != "git" || res.Requested != "v1.0" || res.Resolved != strings.Repeat("a", 40) || res.LockPresent {
		t.Fatalf("facts = %+v err = %v", res, err)
	}
}

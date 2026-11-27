package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLearn_PasswordInSourceURLRejected(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	proj := scratchDir(t)
	code, _, errs := runGym(t, proj, "learn", "https://user:hunter2@127.0.0.1:1/org/repo.git")
	if code == 0 || !strings.Contains(errs, "source-credentials-in-url: use a git credential helper or ssh key") {
		t.Fatalf("exit %d, want rejection:\n%s", code, errs)
	}
	if strings.Contains(errs, "hunter2") {
		t.Fatalf("password echoed:\n%s", errs)
	}
	if exists(filepath.Join(proj, ".gym", "installed.json")) {
		t.Fatal("state written")
	}
}

func TestLearn_UsernameOnlyURLNotRejectedAsCredentials(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	proj := scratchDir(t)
	for _, u := range []string{"git@127.0.0.1:org/repo", "ssh://git@127.0.0.1:1/org/repo.git"} {
		_, _, errs := runGym(t, proj, "learn", u)
		if strings.Contains(errs, "source-credentials-in-url") {
			t.Fatalf("%s rejected as credentials:\n%s", u, errs)
		}
	}
}

func TestLearn_CloneErrorRedactsQueryToken(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	proj := scratchDir(t)
	code, _, errs := runGym(t, proj, "learn", "https://127.0.0.1:1/org/repo.git?token=SEKRET123")
	if code == 0 {
		t.Fatal("clone of unreachable host succeeded")
	}
	if strings.Contains(errs, "SEKRET123") {
		t.Fatalf("token echoed:\n%s", errs)
	}
}

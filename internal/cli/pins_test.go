package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/registry"
)

func skillBody(tag string) string {
	return "---\nname: up\ndescription: d\n---\nbody " + tag + "\n"
}

// B3: name@version installs that exact registry version; a miss is
// pin-unavailable and installs nothing.
func TestB3RegistryPinHitAndMiss(t *testing.T) {
	e := newLkEnv(t, "")
	e.index(t,
		registry.SkillEntry{Name: "lint", Version: "1.2.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "pinned-old")},
		registry.SkillEntry{Name: "lint", Version: "1.3.0", SourceURL: lkSkillSrc(t, scratchDir(t), "lint", "newest")},
	)

	code, out, errs := e.gym(t, "learn", "lint@9.9.9")
	if code != 1 || !strings.Contains(errs, "pin-unavailable: lint@9.9.9 not in index (available: 1.3.0, 1.2.0)") {
		t.Fatalf("miss: exit %d\n%s\n%s", code, out, errs)
	}
	if installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("a pin miss installed something")
	}

	e.mustGym(t, "learn", "lint@1.2.0")
	if got := lkRead(t, filepath.Join(e.skillDir("lint"), "SKILL.md")); !strings.Contains(got, "pinned-old") {
		t.Fatalf("lint@1.2.0 installed the wrong version:\n%s", got)
	}

	// Unpinned takes the best.
	e.mustGym(t, "unlearn", "lint")
	e.mustGym(t, "learn", "lint")
	if got := lkRead(t, filepath.Join(e.skillDir("lint"), "SKILL.md")); !strings.Contains(got, "newest") {
		t.Fatalf("plain learn did not take the best version:\n%s", got)
	}
}

func TestB3PinUnavailableWithoutAnyVersion(t *testing.T) {
	e := newLkEnv(t, "")
	e.index(t)
	code, _, errs := e.gym(t, "learn", "ghost@1.0.0")
	if code != 1 || !strings.Contains(errs, "pin-unavailable: ghost@1.0.0 not in index (available: none)") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

// B4: url#tag and url#<sha> install that ref; a bad ref is ref-not-found.
func TestB4GitRefPins(t *testing.T) {
	lkRequireGit(t)
	e := newLkEnv(t, "")
	repo := filepath.Join(scratchDir(t), "up")
	c1 := lkGitRepo(t, repo, map[string]string{"SKILL.md": skillBody("first")})
	lkGit(t, repo, "tag", "v1.0")
	lkGitCommit(t, repo, map[string]string{"SKILL.md": skillBody("second")})
	url := "file://" + repo

	e.mustGym(t, "learn", url+"#v1.0")
	if got := lkRead(t, filepath.Join(e.skillDir("up"), "SKILL.md")); !strings.Contains(got, "first") {
		t.Fatalf("#v1.0 did not install the tag:\n%s", got)
	}
	e.mustGym(t, "unlearn", "up")

	e.mustGym(t, "learn", url+"#"+c1)
	if got := lkRead(t, filepath.Join(e.skillDir("up"), "SKILL.md")); !strings.Contains(got, "first") {
		t.Fatalf("#<sha> did not install the commit:\n%s", got)
	}
	e.mustGym(t, "unlearn", "up")

	e.mustGym(t, "learn", url)
	if got := lkRead(t, filepath.Join(e.skillDir("up"), "SKILL.md")); !strings.Contains(got, "second") {
		t.Fatalf("no ref must take the default branch:\n%s", got)
	}
	e.mustGym(t, "unlearn", "up")

	for _, bad := range []string{"nope", strings.Repeat("a", 40)} {
		code, _, errs := e.gym(t, "learn", url+"#"+bad)
		if code != 1 || !strings.Contains(errs, "ref-not-found: "+bad+" at "+url) {
			t.Fatalf("#%s: exit %d\n%s", bad, code, errs)
		}
	}
	if installed(filepath.Join(e.proj, ".gym", "skills"), "up") {
		t.Fatal("a bad ref installed something")
	}
}

func TestGitRefRejectsOptionInjectionAndKeepsCredentialCheck(t *testing.T) {
	lkRequireGit(t)
	e := newLkEnv(t, "")
	repo := filepath.Join(scratchDir(t), "up")
	lkGitRepo(t, repo, map[string]string{"SKILL.md": skillBody("x")})
	code, _, errs := e.gym(t, "learn", "file://"+repo+"#--upload-pack=touch-pwned")
	if code != 1 || !strings.Contains(errs, `invalid ref "--upload-pack=touch-pwned"`) {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	code, _, errs = e.gym(t, "learn", "https://user:hunter2@example.invalid/o/r.git#v1")
	if code != 1 || !strings.Contains(errs, "source-credentials-in-url") || strings.Contains(errs, "hunter2") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

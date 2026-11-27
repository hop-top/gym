package install

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/netpolicy"
)

// fakeGit puts a `git` on PATH that logs every invocation (argv, one line
// each, plus the GIT_TERMINAL_PROMPT it saw), delegates `ls-remote` to the
// real git (the local insteadOf lookup) and succeeds at everything else
// without doing anything. It returns the log path.
func fakeGit(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "git.log")
	script := "#!/bin/sh\n" +
		"echo \"PROMPT=${GIT_TERMINAL_PROMPT-unset} ARGS=$*\" >> " + log + "\n" +
		"case \"$*\" in *ls-remote*) exec " + real + " \"$@\";; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	isolateGit(t)
	return log
}

// isolateGit keeps git away from the real user and system configuration.
func isolateGit(t *testing.T) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	return cfg
}

func readLog(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// networkInvocations are the logged git calls other than the local
// `ls-remote --get-url` lookup.
func networkInvocations(lines []string) []string {
	var out []string
	for _, l := range lines {
		if l != "" && !strings.Contains(l, "ls-remote --get-url") {
			out = append(out, l)
		}
	}
	return out
}

const hardened = "-c protocol.allow=user -c protocol.https.allow=always -c protocol.ssh.allow=always -c protocol.file.allow=user -c protocol.ext.allow=never"

// D15: every git call carries the exact hardened protocol set, and
// GIT_TERMINAL_PROMPT=0 off a terminal.
func TestD15GitRunsHardenedWithoutPrompt(t *testing.T) {
	log := fakeGit(t)
	old := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	defer func() { stdinIsTerminal = old }()

	sha := strings.Repeat("a", 40)
	for _, ref := range []string{"", "v1", sha} {
		if _, err := StageSource(context.Background(), "https://git.example/o/r.git", ref); err != nil {
			t.Logf("ref %q: %v", ref, err) // rev-parse prints nothing from the fake
		}
	}
	lines := networkInvocations(readLog(t, log))
	if len(lines) == 0 {
		t.Fatal("git was never invoked")
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "PROMPT=0 ARGS="+hardened+" ") {
			t.Errorf("git call without the hardened set or prompt off:\n%s", l)
		}
	}
	// The three flows all ran: clone, branch clone, init+fetch+checkout.
	all := strings.Join(lines, "\n")
	for _, want := range []string{" clone --depth 1 -- https://git.example/o/r.git ", " clone --depth 1 --branch v1 -- ", " fetch -q --depth 1 origin " + sha, " checkout -q --detach FETCH_HEAD"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing call %q in:\n%s", want, all)
		}
	}
}

// On a terminal git may prompt: the variable is left alone.
func TestD15PromptLeftAloneOnTerminal(t *testing.T) {
	log := fakeGit(t)
	t.Setenv("GIT_TERMINAL_PROMPT", "")
	os.Unsetenv("GIT_TERMINAL_PROMPT")
	old := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	defer func() { stdinIsTerminal = old }()
	_, _ = StageSource(context.Background(), "https://git.example/o/r.git", "")
	for _, l := range networkInvocations(readLog(t, log)) {
		if !strings.HasPrefix(l, "PROMPT=unset ") {
			t.Errorf("prompt forced on a terminal: %s", l)
		}
	}
}

// gym never asks git for submodules, so nested fetches cannot reach the
// file transport.
func TestGitNeverRecursesIntoSubmodules(t *testing.T) {
	log := fakeGit(t)
	sha := strings.Repeat("b", 40)
	for _, ref := range []string{"", "v1", sha} {
		_, _ = StageSource(context.Background(), "https://git.example/o/r.git", ref)
	}
	for _, l := range readLog(t, log) {
		if strings.Contains(l, "recurse") || strings.Contains(l, "--recursive") || strings.Contains(l, "submodule") {
			t.Errorf("submodule recursion requested: %s", l)
		}
	}
}

// An ext:: source is refused by git itself under the hardened set, with the
// real git.
func TestExtSourceRefusedByGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolateGit(t)
	sentinel := filepath.Join(t.TempDir(), "ran")
	// StageSource's own looksLikeURL guard stops an ext:: source first;
	// this is the second layer, git's, reached through the one git() helper.
	if _, err := StageSource(context.Background(), "ext::sh -c 'touch "+sentinel+"'", ""); err == nil {
		t.Fatal("ext:: source accepted by StageSource")
	}
	out, err := git(context.Background(), "", "clone", "--", "ext::sh -c 'touch "+sentinel+"'", filepath.Join(t.TempDir(), "dest"))
	if err == nil || !strings.Contains(string(out), "transport 'ext' not allowed") {
		t.Fatalf("git accepted ext::: %v\n%s", err, out)
	}
	if _, serr := os.Stat(sentinel); serr == nil {
		t.Fatal("ext:: helper command ran")
	}
}

// D6 (git): under allowlist an unlisted host is denied before any git
// command that could reach the network runs.
func TestD6GitDeniedBeforeSpawn(t *testing.T) {
	log := fakeGit(t)
	defer netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeAllowlist, AllowHosts: []string{"git.corp.example"}})()

	for _, src := range []string{"https://github.com/o/r.git", "git@github.com:o/r.git", "ssh://git@github.com/o/r.git"} {
		_, err := StageSource(context.Background(), src, "")
		var ne *netpolicy.Error
		if err == nil || !errors.As(err, &ne) {
			t.Fatalf("%s: err = %v", src, err)
		}
		if want := `network-denied: host "github.com" not in network.allow_hosts (mode allowlist)`; err.Error() != want {
			t.Fatalf("%s: %q", src, err)
		}
	}
	if got := networkInvocations(readLog(t, log)); len(got) != 0 {
		t.Fatalf("git ran before the denial:\n%s", strings.Join(got, "\n"))
	}
	// A listed host proceeds to git.
	_, _ = StageSource(context.Background(), "https://git.corp.example/o/r.git", "")
	if got := networkInvocations(readLog(t, log)); len(got) == 0 {
		t.Fatal("listed host never reached git")
	}
}

// D7: the host checked is the effective URL after insteadOf, not the typed
// one. A public typed URL rewritten to an allowed local mirror proceeds; a
// listed typed URL rewritten to a public host is denied.
func TestD7InsteadOfEffectiveURL(t *testing.T) {
	log := fakeGit(t)
	cfg := os.Getenv("GIT_CONFIG_GLOBAL")
	mirror := t.TempDir()
	conf := "[url \"file://" + mirror + "/\"]\n\tinsteadOf = https://public.example/\n" +
		"[url \"https://evil.example/\"]\n\tinsteadOf = https://git.corp.example/\n"
	if err := os.WriteFile(cfg, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	defer netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeAllowlist, AllowHosts: []string{"git.corp.example"}})()

	// typed host is public and unlisted, effective is a local mirror: allowed.
	if _, err := StageSource(context.Background(), "https://public.example/o/r.git", ""); err != nil {
		t.Fatalf("effective local mirror denied: %v", err)
	}
	if got := networkInvocations(readLog(t, log)); len(got) == 0 {
		t.Fatal("mirror source never reached git")
	}

	// typed host is listed, effective is public: denied, no new git call.
	before := len(networkInvocations(readLog(t, log)))
	_, err := StageSource(context.Background(), "https://git.corp.example/o/r.git", "")
	var ne *netpolicy.Error
	if err == nil || !errors.As(err, &ne) || !strings.Contains(err.Error(), `"evil.example"`) {
		t.Fatalf("err = %v", err)
	}
	if after := len(networkInvocations(readLog(t, log))); after != before {
		t.Fatalf("git ran for a denied effective URL (%d -> %d)", before, after)
	}
}

// Offline: a git URL is denied, a local repository still clones.
func TestOfflineGit(t *testing.T) {
	log := fakeGit(t)
	defer netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeOffline})()
	_, err := StageSource(context.Background(), "https://github.com/o/r.git", "")
	if err == nil || err.Error() != `network-denied: offline mode forbids network access to "github.com"` {
		t.Fatalf("err = %v", err)
	}
	if got := networkInvocations(readLog(t, log)); len(got) != 0 {
		t.Fatalf("git ran offline: %v", got)
	}
	if _, err := StageSource(context.Background(), "file://"+t.TempDir()+"/repo.git", "main"); err != nil {
		t.Fatalf("local file:// repository denied offline: %v", err)
	}
}

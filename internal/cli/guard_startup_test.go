package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// These tests drive the real command path (App.Run -> cobra hook chain)
// with a policy file the guard cannot load, so the guard subscriber
// never starts while the bus does.

func breakUserPolicies(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "gym", "policies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("name: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCLI(home string, args ...string) (int, string) {
	var out, errb bytes.Buffer
	app := NewApp(&out, &errb, home)
	viper.Set("gate-timeout", 100*time.Millisecond)
	code := app.Run(args)
	return code, errb.String()
}

func TestLearn_GuardStartupFailureDeniedUnderStrict(t *testing.T) {
	home := isolateInternal(t)
	breakUserPolicies(t, home)
	dest := filepath.Join(scratch(t), "d")
	code, stderr := runCLI(home, "--offline", "--strict", "learn", "--path", dest, skillTree(t, "lint", ""))
	if code == 0 {
		t.Fatalf("strict learn with dead guard succeeded; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "guard unavailable") || !strings.Contains(stderr, "bad.yaml") {
		t.Fatalf("stderr lacks cause: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dest, "lint")); err == nil {
		t.Fatal("skill installed despite strict deny")
	}
}

func TestLearn_GuardStartupFailureWarnsWithoutStrict(t *testing.T) {
	home := isolateInternal(t)
	breakUserPolicies(t, home)
	dest := filepath.Join(scratch(t), "d")
	code, stderr := runCLI(home, "--offline", "learn", "--path", dest, skillTree(t, "lint", ""))
	if code != 0 {
		t.Fatalf("non-strict learn failed: %s", stderr)
	}
	if !strings.Contains(stderr, "guard failed to start") || !strings.Contains(stderr, "bad.yaml") {
		t.Fatalf("no startup warning naming cause: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dest, "lint")); err != nil {
		t.Fatalf("fail-open install missing: %v", err)
	}
}

func TestTrain_GuardStartupFailureDeniedUnderStrict(t *testing.T) {
	home := isolateInternal(t)
	breakUserPolicies(t, home)
	dest := scratch(t)
	src := skillTree(t, "lint", "")
	old := seedInstalled(t, dest, src, "internal")
	code, stderr := runCLI(home, "--offline", "--strict", "train")
	if code == 0 {
		t.Fatalf("strict train with dead guard succeeded; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "guard unavailable") || !strings.Contains(stderr, "bad.yaml") {
		t.Fatalf("stderr lacks cause: %s", stderr)
	}
	body, _ := os.ReadFile(filepath.Join(old, "SKILL.md"))
	if !strings.Contains(string(body), "OLD") {
		t.Fatalf("installed skill modified: %q", body)
	}
}

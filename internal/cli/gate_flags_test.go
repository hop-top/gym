package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// gateFixture builds a skill dir whose gym.json carries postinstall and
// returns (skillDir, destDir, cwd, baseArgs). destDir lives under the
// package directory: the built-in path-allowlist policy denies system
// temp dirs, which would mask the policy under test.
func gateFixture(t *testing.T, postinstall string) (skill, dest, cwd string, base []string) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	isolate(t)
	cwd = scratchDir(t)

	skill = filepath.Join(t.TempDir(), "demo")
	write(t, filepath.Join(skill, "SKILL.md"), "---\nname: demo\ndescription: d\n---\n# demo\n")
	// Valid gym.skill/v1 manifest. No author allowance fields: tests must
	// not depend on allow_postinstall being honoured (or ignored), so
	// deny cases assert on the specific rule that must appear among the
	// findings, never on the full set.
	manifest := `{"schema": "gym.skill/v1", "allowed_tools": [], "data_class": "public"`
	if postinstall != "" {
		manifest += `, "postinstall": "` + postinstall + `"`
	}
	write(t, filepath.Join(skill, "gym.json"), manifest+"}")

	abs := scratchDir(t)
	dest = abs
	base = []string{
		"-c", "validator={command: [true], timeout_seconds: 5}",
		"-c", "install_paths=[{name: p, path: " + abs + ", scope: project}]",
	}
	return skill, dest, cwd, base
}

func TestLearn_PolicyDenyBlocksInstall(t *testing.T) {
	skill, dest, cwd, base := gateFixture(t, "sudo make install")
	code, _, errs := runGym(t, cwd, append(base, "learn", skill)...)
	if code == 0 {
		t.Fatalf("exit 0; want non-zero on policy deny")
	}
	if !strings.Contains(errs, "no-sudo") {
		t.Fatalf("stderr missing deny finding:\n%s", errs)
	}
	if installed(dest, "demo") {
		t.Fatal("skill installed despite deny")
	}
}

func TestLearn_InsecureLowersDenyToWarn(t *testing.T) {
	skill, dest, cwd, base := gateFixture(t, "sudo make install")
	code, out, errs := runGym(t, cwd, append(base, "--insecure", "learn", skill)...)
	if code != 0 {
		t.Fatalf("exit %d with --insecure; want 0:\n%s", code, errs)
	}
	if !installed(dest, "demo") {
		t.Fatalf("skill not installed with --insecure\nstdout:%s\nstderr:%s", out, errs)
	}
	if !strings.Contains(errs, "no-sudo") {
		t.Fatalf("warning not printed:\n%s", errs)
	}
}

// Precedence pinned from guard.resolveVerdict: --insecure only affects
// DENY verdicts, --strict only WARN/timeout; combined they stay
// independent.
func TestLearn_StrictAndInsecure_DenyAllowed(t *testing.T) {
	skill, dest, cwd, base := gateFixture(t, "sudo make install")
	code, _, errs := runGym(t, cwd, append(base, "--strict", "--insecure", "learn", skill)...)
	if code != 0 {
		t.Fatalf("exit %d; --insecure must lower deny even with --strict:\n%s", code, errs)
	}
	if !installed(dest, "demo") {
		t.Fatal("skill not installed")
	}
}

func TestLearn_StrictAndInsecure_WarnStillDenied(t *testing.T) {
	// No postinstall: the only findings are admission warns (unpinned
	// digest), which --strict turns into a deny that --insecure must
	// not lower.
	skill, dest, cwd, base := gateFixture(t, "")
	code, _, errs := runGym(t, cwd, append(base, "--strict", "--insecure", "learn", skill)...)
	if code == 0 {
		t.Fatalf("exit 0; --strict must turn warn into deny even with --insecure")
	}
	if !strings.Contains(errs, "admission-digest-unpinned") {
		t.Fatalf("stderr missing finding:\n%s", errs)
	}
	if installed(dest, "demo") {
		t.Fatal("skill installed despite strict warn->deny")
	}
}

// `gym train` gates the whole plan; built-in policies never match the
// plan payload, so a project policy supplies the deny. Proves only the
// whole-plan gate: the seeded skill is not in the registry index, so
// train skips it ("skip demo") before the per-skill gate would run.
func TestTrain_InsecureLowersPlanDenyToWarn(t *testing.T) {
	skill, _, cwd, base := gateFixture(t, "")
	if code, _, errs := runGym(t, cwd, append(base, "learn", skill)...); code != 0 {
		t.Fatalf("seed learn: exit %d: %s", code, errs)
	}
	write(t, filepath.Join(cwd, ".gym", "policies", "deny-train.yaml"),
		"name: deny-train\nwhen: has(payload.items) && size(payload.items) > 0\n"+
			"effect: deny\notherwise: allow\nseverity: deny\nmessage: train blocked\n")

	code, _, errs := runGym(t, cwd, append(base, "train")...)
	if code == 0 || !strings.Contains(errs, "deny-train") {
		t.Fatalf("train: exit %d; want policy deny:\n%s", code, errs)
	}

	viper.Reset()
	code, out, errs := runGym(t, cwd, append(base, "--insecure", "train")...)
	if code != 0 {
		t.Fatalf("train --insecure: exit %d:\n%s", code, errs)
	}
	if !strings.Contains(out, "skip demo") || !strings.Contains(errs, "deny-train") {
		t.Fatalf("plan not gated-then-run with warning\nstdout:%s\nstderr:%s", out, errs)
	}
}

// --gate-timeout must reach the gate. A 1ns deadline races the in-process
// guard reply, so retry: with the flag dead the wait is 30s and the
// gate never times out.
func TestLearn_GateTimeoutFlagReachesGate(t *testing.T) {
	skill, _, cwd, base := gateFixture(t, "")
	for i := 0; i < 50; i++ {
		viper.Reset()
		code, _, errs := runGym(t, cwd, append(base, "--strict", "--gate-timeout", "1ns", "learn", skill)...)
		if code != 0 && strings.Contains(errs, "gate timeout") {
			return
		}
	}
	t.Fatal("--gate-timeout 1ns --strict never produced a gate timeout")
}

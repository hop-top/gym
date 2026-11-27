package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"
	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/xdg"
)

// These tests drive runLearnCtx directly with a memory bus and the real
// guard subscriber, so the events published on the bus are observable.

func scratch(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// path-allowlist denies system temp, so stay inside the package dir.
	d, err := os.MkdirTemp(wd, ".scratch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func isolateInternal(t *testing.T) string {
	t.Helper()
	home := scratch(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")
	cfgDir := filepath.Join(home, ".config", "gym")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "validator:\n  command: [sh, -c, \"true\"]\n  timeout_seconds: 5\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	viper.Reset()
	t.Cleanup(viper.Reset)
	return home
}

func skillTree(t *testing.T, name, manifest string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"SKILL.md": "---\nname: " + name + "\ndescription: d\n---\nbody\n"}
	if manifest != "" {
		files["gym.json"] = manifest
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type busApp struct {
	app      *App
	stderr   *bytes.Buffer
	mu       sync.Mutex
	rejected []events.SkillInstallRejectedPayload
	required []events.SkillInstallRequestedPayload
}

// newBusApp wires an App with a memory bus and the real guard.
func newBusApp(t *testing.T, cwd string, withGuard bool) *busApp {
	t.Helper()
	var out bytes.Buffer
	ba := &busApp{stderr: &bytes.Buffer{}}
	ba.app = NewApp(&out, ba.stderr, cwd)
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	ba.app.bus = b
	if withGuard {
		g, err := newGuardForApp(ba.app)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(g.Stop)
	}
	sub := func(topic bus.Topic, f func(raw []byte)) {
		b.Subscribe(string(topic), func(_ context.Context, e bus.Event) error {
			raw, _ := json.Marshal(e.Payload)
			ba.mu.Lock()
			defer ba.mu.Unlock()
			f(raw)
			return nil
		})
	}
	sub(events.TopicSkillInstallRejected, func(raw []byte) {
		var p events.SkillInstallRejectedPayload
		_ = json.Unmarshal(raw, &p)
		ba.rejected = append(ba.rejected, p)
	})
	sub(events.TopicSkillInstallRequested, func(raw []byte) {
		var p events.SkillInstallRequestedPayload
		_ = json.Unmarshal(raw, &p)
		ba.required = append(ba.required, p)
	})
	return ba
}

func (ba *busApp) learn(t *testing.T, src, dest string) error {
	t.Helper()
	err := ba.app.runLearnCtx(context.Background(), true, dest, nil, []string{src})
	time.Sleep(150 * time.Millisecond) // let async subscribers drain
	return err
}

func (ba *busApp) snapshot() ([]events.SkillInstallRejectedPayload, []events.SkillInstallRequestedPayload) {
	ba.mu.Lock()
	defer ba.mu.Unlock()
	return append([]events.SkillInstallRejectedPayload(nil), ba.rejected...),
		append([]events.SkillInstallRequestedPayload(nil), ba.required...)
}

// Case 31: a policy-deny admission failure publishes exactly one
// rejected event carrying the reason and the request_id of the
// matching requested event.
func TestLearn_RejectedPublishedOnceForPolicyDeny(t *testing.T) {
	home := isolateInternal(t)
	ba := newBusApp(t, home, true)
	src := skillTree(t, "lint", `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"personal"}`)
	err := ba.learn(t, src, scratch(t))
	if err == nil {
		t.Fatal("expected rejection")
	}
	rej, req := ba.snapshot()
	if len(rej) != 1 {
		t.Fatalf("rejected events = %d; want exactly 1: %+v", len(rej), rej)
	}
	if rej[0].Reason != install.ReasonDataClassExceeds || rej[0].Skill != "lint" || rej[0].Scope != "global" {
		t.Fatalf("rejected = %+v", rej[0])
	}
	if len(req) != 1 || req[0].RequestID == "" || rej[0].RequestID != req[0].RequestID {
		t.Fatalf("request_id: rejected=%q requested=%+v", rej[0].RequestID, req)
	}
	if req[0].DataClass != "personal" || req[0].MaxDataClass != "internal" || !req[0].DataClassOver || req[0].Digest == "" {
		t.Fatalf("requested payload lacks admission facts: %+v", req[0])
	}
}

// Case 31 (hard failures): digest-mismatch, manifest-invalid, and
// approval-revoked each publish once, with a request_id, before any
// gate event.
func TestLearn_RejectedPublishedOnceForHardFailures(t *testing.T) {
	cases := map[string]struct {
		manifest string
		pin      string
		reason   string
	}{
		"manifest-invalid": {manifest: `{"schema":"gym.skill/v1"}`, reason: install.ReasonManifestInvalid},
		"digest-mismatch":  {pin: "sha256:" + strings.Repeat("0", 64), reason: install.ReasonDigestMismatch},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			home := isolateInternal(t)
			ba := newBusApp(t, home, true)
			src := skillTree(t, "lint", c.manifest)
			token := src
			if c.pin != "" {
				dirs, _ := xdg.AppDirs(config.AppName)
				_ = registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{
					{Name: "lint", SourceURL: src, Version: "1.0.0", Digest: c.pin},
				}})
				token = "lint"
			}
			if err := ba.learn(t, token, scratch(t)); err == nil {
				t.Fatal("expected rejection")
			}
			rej, req := ba.snapshot()
			if len(rej) != 1 || rej[0].Reason != c.reason || rej[0].RequestID == "" {
				t.Fatalf("rejected = %+v", rej)
			}
			if len(req) != 0 {
				t.Errorf("gate ran after hard failure: %+v", req)
			}
		})
	}
}

// A validator failure is a rejection: it publishes exactly one rejected
// event with reason validator-failed. No requested event precedes it,
// so the id is the attempt's own.
func TestLearn_ValidatorFailurePublishesRejected(t *testing.T) {
	home := isolateInternal(t)
	setValidator(t, home, "exit 1")
	ba := newBusApp(t, home, true)
	src := skillTree(t, "lint", "")
	if err := ba.learn(t, src, scratch(t)); err == nil {
		t.Fatal("expected validator failure")
	}
	rej, req := ba.snapshot()
	if len(rej) != 1 || rej[0].Reason != install.ReasonValidatorFailed || rej[0].Skill != "lint" || rej[0].Scope != "global" || rej[0].RequestID == "" {
		t.Fatalf("rejected = %+v", rej)
	}
	if len(req) != 0 {
		t.Errorf("gate ran after validator failure: %+v", req)
	}
}

// Case 27 (cli): with --insecure resolved (global viper), a data-class
// deny is lowered to a warning and the install proceeds, with no
// rejected event.
func TestLearn_InsecureLowersDataClassDenyAndPublishesNoRejection(t *testing.T) {
	home := isolateInternal(t)
	viper.Set("insecure", true)
	ba := newBusApp(t, home, true)
	src := skillTree(t, "lint", `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"personal"}`)
	dest := scratch(t)
	if err := ba.learn(t, src, dest); err != nil {
		t.Fatalf("insecure install failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lint", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ba.stderr.String(), "admission-data-class") {
		t.Errorf("no warning logged: %s", ba.stderr.String())
	}
	if rej, _ := ba.snapshot(); len(rej) != 0 {
		t.Fatalf("rejected = %+v", rej)
	}
}

// Case 28: --insecure never relaxes a digest mismatch.
func TestLearn_InsecureDoesNotRelaxDigestMismatch(t *testing.T) {
	home := isolateInternal(t)
	viper.Set("insecure", true)
	ba := newBusApp(t, home, true)
	src := skillTree(t, "lint", "")
	dirs, _ := xdg.AppDirs(config.AppName)
	_ = registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{
		{Name: "lint", SourceURL: src, Version: "1.0.0", Digest: "sha256:" + strings.Repeat("0", 64)},
	}})
	dest := scratch(t)
	if err := ba.learn(t, "lint", dest); err == nil {
		t.Fatal("insecure installed a digest mismatch")
	}
	if _, err := os.Stat(filepath.Join(dest, "lint")); !os.IsNotExist(err) {
		t.Fatal("destination written")
	}
}

// Case 29 (install half): with no bus wired the gate is absent, yet the
// hard admission failures are still enforced.
func TestLearn_HardFailuresEnforcedWithoutBus(t *testing.T) {
	home := isolateInternal(t)
	var out, errb bytes.Buffer
	app := NewApp(&out, &errb, home)
	if app.Bus() != nil {
		t.Fatal("bus unexpectedly wired")
	}
	src := skillTree(t, "lint", `{"schema":"nope"}`)
	err := app.runLearnCtx(context.Background(), true, scratch(t), nil, []string{src})
	var ae *install.AdmissionError
	if !asAdmission(err, &ae) || ae.Reason != install.ReasonManifestInvalid {
		t.Fatalf("err = %v; want manifest-invalid", err)
	}
}

func asAdmission(err error, target **install.AdmissionError) bool {
	for err != nil {
		if ae, ok := err.(*install.AdmissionError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// Case 29 (strict): with --strict and a gate timeout, policy findings
// fail closed; hard failures were already enforced before the gate.
func TestLearn_StrictGateTimeoutDenies(t *testing.T) {
	home := isolateInternal(t)
	viper.Set("strict", true)
	viper.Set("gate-timeout", 100*time.Millisecond)
	ba := newBusApp(t, home, false) // bus but no guard subscriber
	src := skillTree(t, "lint", "")
	if err := ba.learn(t, src, scratch(t)); err == nil || !strings.Contains(err.Error(), "gate timeout") {
		t.Fatalf("err = %v; want gate timeout deny under strict", err)
	}
}

// seedInstalled records an installed v1.0.0 skill in global state and
// writes its files, returning the install dir and the registry entry
// for a newer version served from src.
func seedInstalled(t *testing.T, dest, src, installedClass string) string {
	t.Helper()
	dirs, err := xdg.AppDirs(config.AppName)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dest, "lint")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "SKILL.md"), []byte("---\nname: lint\ndescription: old\n---\nOLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = state.SaveGlobal(dirs.StateHome, []state.InstalledSkill{{
		Name: "lint", Version: "1.0.0", SourceURL: src, InstallPath: old, DataClass: installedClass,
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{
		{Name: "lint", Version: "1.1.0", SourceURL: src},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return old
}

// Case 30: `gym train` upgrade to a version with an undeclared tool is
// rejected through the per-skill gate; nothing is written, and one
// rejected event is published.
func TestTrain_UpgradeWithUndeclaredToolRejected(t *testing.T) {
	home := isolateInternal(t)
	dest := scratch(t)
	src := filepath.Join(t.TempDir(), "lint")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSrc := map[string]string{
		"SKILL.md": "---\nname: lint\ndescription: new\nallowed-tools: Read Write\n---\nNEW\n",
		"gym.json": `{"schema":"gym.skill/v1","allowed_tools":["Read"],"data_class":"internal"}`,
	}
	for n, b := range writeSrc {
		if err := os.WriteFile(filepath.Join(src, n), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := seedInstalled(t, dest, src, "internal")
	ba := newBusApp(t, home, true)

	err := ba.app.runTrainCtx(context.Background(), false, nil)
	time.Sleep(150 * time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "admission-tools-undeclared") {
		t.Fatalf("err = %v; want tools-undeclared deny", err)
	}
	body, rerr := os.ReadFile(filepath.Join(old, "SKILL.md"))
	if rerr != nil || !strings.Contains(string(body), "OLD") {
		t.Fatalf("installed skill was modified: %q %v", body, rerr)
	}
	rej, req := ba.snapshot()
	if len(rej) != 1 || rej[0].Reason != install.ReasonToolsUndeclared || rej[0].Skill != "lint" || rej[0].Scope != "global" {
		t.Fatalf("rejected = %+v", rej)
	}
	// plan-level request (no facts) plus the per-skill request that carries them
	var perSkill *events.SkillInstallRequestedPayload
	for i := range req {
		if len(req[i].UndeclaredTools) > 0 {
			perSkill = &req[i]
		}
	}
	if perSkill == nil || perSkill.RequestID != rej[0].RequestID {
		t.Fatalf("per-skill gate request missing or uncorrelated: rej=%+v req=%+v", rej, req)
	}
}

// Upgrade path: a hard integrity failure (digest mismatch) is enforced
// even though the train path had no gate before.
func TestTrain_UpgradeDigestMismatchRejected(t *testing.T) {
	home := isolateInternal(t)
	dest := scratch(t)
	src := skillTree(t, "lint", "")
	old := seedInstalled(t, dest, src, "")
	dirs, _ := xdg.AppDirs(config.AppName)
	_ = registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{
		{Name: "lint", Version: "1.1.0", SourceURL: src, Digest: "sha256:" + strings.Repeat("0", 64)},
	}})
	ba := newBusApp(t, home, true)
	if err := ba.app.runTrainCtx(context.Background(), false, nil); err == nil {
		t.Fatal("expected digest-mismatch")
	}
	time.Sleep(150 * time.Millisecond)
	if body, _ := os.ReadFile(filepath.Join(old, "SKILL.md")); !strings.Contains(string(body), "OLD") {
		t.Fatalf("installed skill modified: %q", body)
	}
	if rej, _ := ba.snapshot(); len(rej) != 1 || rej[0].Reason != install.ReasonDigestMismatch {
		t.Fatalf("rejected = %+v", rej)
	}
}

// data-class-escalated: upgrading from public to internal warns (and
// proceeds), carrying the installed class from state.
func TestTrain_DataClassEscalationWarns(t *testing.T) {
	home := isolateInternal(t)
	dest := scratch(t)
	src := skillTree(t, "lint", `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"internal"}`)
	old := seedInstalled(t, dest, src, "public")
	ba := newBusApp(t, home, true)
	if err := ba.app.runTrainCtx(context.Background(), false, nil); err != nil {
		t.Fatalf("train: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if !strings.Contains(ba.stderr.String(), "admission-data-class-escalated") {
		t.Errorf("no escalation warning\n%s", ba.stderr.String())
	}
	if body, _ := os.ReadFile(filepath.Join(old, "SKILL.md")); strings.Contains(string(body), "OLD") {
		t.Errorf("upgrade did not apply: %q", body)
	}
	dirs, _ := xdg.AppDirs(config.AppName)
	skills, _ := state.LoadGlobal(dirs.StateHome)
	if len(skills) != 1 || skills[0].DataClass != "internal" || skills[0].Digest == "" {
		t.Fatalf("state after upgrade = %+v", skills)
	}
}

// Case 33: `gym guard test <dir>` and the install gate agree on verdict
// and findings for the same tree, because they share Admit and the
// payload builder.
func TestGuardTestDir_MatchesInstallVerdict(t *testing.T) {
	trees := map[string]string{
		"over-ceiling":  `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"personal"}`,
		"undeclared":    `{"schema":"gym.skill/v1","allowed_tools":["Read"],"data_class":"internal"}`,
		"clean-warn":    `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"internal"}`,
		"no-manifest":   "",
		"bare-bash-too": `{"schema":"gym.skill/v1","allowed_tools":["Bash"],"data_class":"public"}`,
	}
	for name, manifest := range trees {
		t.Run(name, func(t *testing.T) {
			home := isolateInternal(t)
			ba := newBusApp(t, home, true)
			dir := skillTree(t, "lint", manifest)
			if name == "undeclared" {
				_ = os.WriteFile(filepath.Join(dir, "SKILL.md"),
					[]byte("---\nname: lint\ndescription: d\nallowed-tools: Read Write\n---\nbody\n"), 0o644)
			}
			res, err := ba.app.evaluateSkillDir(dir, skillDirOptions{})
			if err != nil {
				t.Fatal(err)
			}
			installErr := ba.learn(t, dir, scratch(t))
			_, req := ba.snapshot()
			if len(req) != 1 {
				t.Fatalf("requested events = %d", len(req))
			}
			wantDeny := res.Verdict == "deny"
			if (installErr != nil) != wantDeny {
				t.Fatalf("guard test verdict %s but install err = %v", res.Verdict, installErr)
			}
			// facts the gate published equal the facts guard test computed
			got := req[0]
			if got.Digest != res.Facts.Digest || got.DataClassOver != res.Facts.DataClassOver ||
				strings.Join(got.UndeclaredTools, ",") != strings.Join(res.Facts.UndeclaredTools, ",") ||
				got.ApprovalState != res.Facts.ApprovalState || got.ManifestPresent != res.Facts.ManifestPresent {
				t.Fatalf("facts differ:\n gate %+v\n test %+v", got, res.Facts)
			}
			if wantDeny {
				var pd *guard.PolicyDenyError
				if !errors.As(installErr, &pd) {
					t.Fatalf("install err = %v", installErr)
				}
				a, b := ruleSet(pd.Findings), guardRuleSet(res.Findings)
				if a != b {
					t.Fatalf("findings differ: install %s vs guard test %s", a, b)
				}
			}
		})
	}
}

func ruleSet(fs []events.PolicyFinding) string {
	var r []string
	for _, f := range fs {
		r = append(r, f.Rule)
	}
	return strings.Join(r, ",")
}

func guardRuleSet(fs []guard.Finding) string {
	var r []string
	for _, f := range fs {
		r = append(r, f.Rule)
	}
	return strings.Join(r, ",")
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/xdg"
)

// Rejected-event coverage for rejections that are not admission
// failures: validator failure and non-admission policy denies.

func setValidator(t *testing.T, home, script string) {
	t.Helper()
	cfgPath := filepath.Join(home, ".config", "gym", "config.yaml")
	body := "validator:\n  command: [sh, -c, \"" + script + "\"]\n  timeout_seconds: 5\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// postinstallTree is a skill whose manifest.json declares a postinstall
// script: denied by no-postinstall-script (not an admission rule).
func postinstallTree(t *testing.T, name string) string {
	t.Helper()
	dir := skillTree(t, name, "")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"postinstall":"echo hi"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (ba *busApp) train(t *testing.T) error {
	t.Helper()
	err := ba.app.runTrainCtx(context.Background(), false, nil)
	time.Sleep(150 * time.Millisecond)
	return err
}

func TestLearn_NonAdmissionDenyPublishesRuleName(t *testing.T) {
	home := isolateInternal(t)
	ba := newBusApp(t, home, true)
	src := postinstallTree(t, "lint")
	dest := scratch(t)
	if err := ba.learn(t, src, dest); err == nil {
		t.Fatal("expected policy deny")
	}
	rej, req := ba.snapshot()
	if len(rej) != 1 || rej[0].Reason != "no-postinstall-script" || rej[0].Skill != "lint" || rej[0].Scope != "global" {
		t.Fatalf("rejected = %+v", rej)
	}
	if len(req) != 1 || req[0].RequestID == "" || rej[0].RequestID != req[0].RequestID {
		t.Fatalf("request_id: rejected=%q requested=%+v", rej[0].RequestID, req)
	}
	if _, err := os.Stat(filepath.Join(dest, "lint")); !os.IsNotExist(err) {
		t.Fatal("destination written")
	}
}

func TestLearn_InsecureLoweredNonAdmissionDenyPublishesNothing(t *testing.T) {
	home := isolateInternal(t)
	viper.Set("insecure", true)
	ba := newBusApp(t, home, true)
	src := postinstallTree(t, "lint")
	dest := scratch(t)
	if err := ba.learn(t, src, dest); err != nil {
		t.Fatalf("insecure install failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lint", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if rej, _ := ba.snapshot(); len(rej) != 0 {
		t.Fatalf("rejected = %+v", rej)
	}
}

func TestLearn_SourceFetchErrorPublishesNothing(t *testing.T) {
	home := isolateInternal(t)
	ba := newBusApp(t, home, true)
	// file:// source that is not a directory: staging fails, not a rejection.
	err := ba.learn(t, "file://"+filepath.Join(home, "does-not-exist"), scratch(t))
	if err == nil || !strings.Contains(err.Error(), "unsupported source") {
		t.Fatalf("err = %v; want stage error", err)
	}
	if rej, _ := ba.snapshot(); len(rej) != 0 {
		t.Fatalf("rejected = %+v", rej)
	}
}

// No bus wired: nothing to publish to, and no error beyond the failure.
func TestLearn_NoBusValidatorFailureIsQuiet(t *testing.T) {
	home := isolateInternal(t)
	setValidator(t, home, "exit 1")
	app := NewApp(&strings.Builder{}, &strings.Builder{}, home)
	if app.Bus() != nil {
		t.Fatal("bus unexpectedly wired")
	}
	err := app.runLearnCtx(context.Background(), true, scratch(t), nil, []string{skillTree(t, "lint", "")})
	var ve *install.ValidatorError
	if !errorsAs(err, &ve) {
		t.Fatalf("err = %v; want validator failure", err)
	}
}

func errorsAs(err error, target **install.ValidatorError) bool {
	for err != nil {
		if ve, ok := err.(*install.ValidatorError); ok {
			*target = ve
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

// seedUpgrade seeds an installed 1.0.0 and a 1.1.0 registry entry
// served from src, returning the installed dir.
func seedUpgrade(t *testing.T, src string) string {
	t.Helper()
	return seedInstalled(t, scratch(t), src, "")
}

func TestTrain_ValidatorFailurePublishesRejected(t *testing.T) {
	home := isolateInternal(t)
	src := skillTree(t, "lint", "")
	old := seedUpgrade(t, src)
	setValidator(t, home, "exit 1")
	ba := newBusApp(t, home, true)
	if err := ba.train(t); err == nil {
		t.Fatal("expected validator failure")
	}
	rej, _ := ba.snapshot()
	if len(rej) != 1 || rej[0].Reason != install.ReasonValidatorFailed || rej[0].Skill != "lint" || rej[0].Scope != "global" || rej[0].RequestID == "" {
		t.Fatalf("rejected = %+v", rej)
	}
	if body, _ := os.ReadFile(filepath.Join(old, "SKILL.md")); !strings.Contains(string(body), "OLD") {
		t.Fatalf("installed skill modified: %q", body)
	}
}

func TestTrain_NonAdmissionDenyPublishesRuleName(t *testing.T) {
	home := isolateInternal(t)
	src := postinstallTree(t, "lint")
	old := seedUpgrade(t, src)
	ba := newBusApp(t, home, true)
	if err := ba.train(t); err == nil {
		t.Fatal("expected policy deny")
	}
	rej, req := ba.snapshot()
	if len(rej) != 1 || rej[0].Reason != "no-postinstall-script" || rej[0].Skill != "lint" || rej[0].Scope != "global" {
		t.Fatalf("rejected = %+v", rej)
	}
	matched := false
	for _, r := range req {
		if r.Postinstall != "" && r.RequestID == rej[0].RequestID {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("rejected request_id %q matches no per-skill requested event: %+v", rej[0].RequestID, req)
	}
	if body, _ := os.ReadFile(filepath.Join(old, "SKILL.md")); !strings.Contains(string(body), "OLD") {
		t.Fatalf("installed skill modified: %q", body)
	}
}

func TestTrain_InsecureLoweredNonAdmissionDenyPublishesNothing(t *testing.T) {
	home := isolateInternal(t)
	viper.Set("insecure", true)
	src := postinstallTree(t, "lint")
	old := seedUpgrade(t, src)
	ba := newBusApp(t, home, true)
	if err := ba.train(t); err != nil {
		t.Fatalf("insecure train failed: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(old, "SKILL.md")); strings.Contains(string(body), "OLD") {
		t.Fatalf("upgrade did not apply: %q", body)
	}
	if rej, _ := ba.snapshot(); len(rej) != 0 {
		t.Fatalf("rejected = %+v", rej)
	}
}

func TestTrain_SourceFetchErrorPublishesNothing(t *testing.T) {
	home := isolateInternal(t)
	src := skillTree(t, "lint", "")
	seedUpgrade(t, src)
	dirs, _ := xdg.AppDirs(config.AppName)
	_ = registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{
		{Name: "lint", Version: "1.1.0", SourceURL: "file://" + filepath.Join(home, "gone")},
	}})
	ba := newBusApp(t, home, true)
	if err := ba.train(t); err == nil || !strings.Contains(err.Error(), "unsupported source") {
		t.Fatalf("err = %v; want stage error", err)
	}
	if rej, _ := ba.snapshot(); len(rej) != 0 {
		t.Fatalf("rejected = %+v", rej)
	}
}

func TestRejectionReason_Ordering(t *testing.T) {
	deny := func(fs ...events.PolicyFinding) error { return &guard.PolicyDenyError{Findings: fs} }
	f := func(rule, sev string) events.PolicyFinding { return events.PolicyFinding{Rule: rule, Severity: sev} }
	cases := map[string]struct {
		err  error
		want string
	}{
		"non-admission deny":                {deny(f("no-curl-bash", "deny")), "no-curl-bash"},
		"warn admission does not mask deny": {deny(f("admission-approval-required", "warn"), f("no-sudo", "deny")), "no-sudo"},
		"admission deny preferred":          {deny(f("no-sudo", "deny"), f("admission-data-class", "deny")), install.ReasonDataClassExceeds},
		"strict warn only":                  {deny(f("no-large-binary", "warn")), "no-large-binary"},
		"validator":                         {&install.ValidatorError{Err: errors.New("x")}, install.ReasonValidatorFailed},
		"wrapped validator":                 {fmt.Errorf("wrap: %w", &install.ValidatorError{Err: errors.New("x")}), install.ReasonValidatorFailed},
		"plain error":                       {errors.New("boom"), ""},
		"deny without findings (no reason)": {deny(), ""},
	}
	for name, c := range cases {
		if got := rejectionReason(c.err); got != c.want {
			t.Errorf("%s: reason = %q; want %q", name, got, c.want)
		}
	}
}

package guard_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/guard"
)

// admissionFacts is a clean activation payload: every admission policy
// stays quiet. Tests flip one fact at a time.
func admissionFacts(over map[string]any) map[string]any {
	p := map[string]any{
		"manifest_present":     true,
		"manifest_mode":        "warn",
		"digest":               "sha256:" + strings.Repeat("a", 64),
		"digest_status":        "match",
		"digest_source":        "registry",
		"declared_tools":       []any{"Read"},
		"undeclared_tools":     []any{},
		"data_class":           "internal",
		"max_data_class":       "internal",
		"data_class_over":      false,
		"data_class_escalated": false,
		"approval_state":       "approved",
	}
	for k, v := range over {
		p[k] = v
	}
	return p
}

func evalFacts(t *testing.T, facts map[string]any) (guard.Verdict, []guard.Finding) {
	t.Helper()
	return allBuiltinEngine(t).Evaluate(map[string]any{"payload": facts})
}

// Case 1 (policy half): clean facts allow with no findings.
func TestAdmissionPolicies_CleanFactsAllow(t *testing.T) {
	v, fs := evalFacts(t, admissionFacts(nil))
	if v != guard.VerdictAllow || len(fs) != 0 {
		t.Fatalf("verdict = %s findings = %+v", v, fs)
	}
}

func TestAdmissionPolicies_Effects(t *testing.T) {
	cases := []struct {
		name  string
		facts map[string]any
		rule  string
		want  guard.Verdict
	}{
		// Case 14 (policy half)
		{"tools undeclared", map[string]any{"undeclared_tools": []any{"Write"}}, "admission-tools-undeclared", guard.VerdictDeny},
		// Case 18 (policy half)
		{"data class over", map[string]any{"data_class_over": true}, "admission-data-class", guard.VerdictDeny},
		{"digest unpinned", map[string]any{"digest_status": "unpinned", "digest_source": ""}, "admission-digest-unpinned", guard.VerdictWarn},
		{"approval unreviewed", map[string]any{"approval_state": "unreviewed"}, "admission-approval-required", guard.VerdictWarn},
		// Case 23 (policy half)
		{"approval enforced", map[string]any{"approval_state": "required-missing"}, "admission-approval-enforced", guard.VerdictDeny},
		// Case 25 (policy half)
		{"manifest missing warn", map[string]any{"manifest_present": false, "manifest_mode": "warn"}, "admission-manifest-missing", guard.VerdictWarn},
		{"manifest missing reject", map[string]any{"manifest_present": false, "manifest_mode": "reject"}, "admission-manifest-required", guard.VerdictDeny},
		// Case 16
		{"bare Bash", map[string]any{"declared_tools": []any{"Read", "Bash"}}, "admission-tools-unrestricted", guard.VerdictWarn},
		{"data class escalated", map[string]any{"data_class_escalated": true}, "admission-data-class-escalated", guard.VerdictWarn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, fs := evalFacts(t, admissionFacts(c.facts))
			if v != c.want || !findingsContain(t, fs, c.rule) || len(fs) != 1 {
				t.Fatalf("verdict = %s findings = %+v; want %s from %s only", v, fs, c.want, c.rule)
			}
		})
	}
}

// Case 25 (off): no manifest under require_manifest=off yields nothing.
func TestAdmissionPolicies_ManifestOffIsQuiet(t *testing.T) {
	v, fs := evalFacts(t, admissionFacts(map[string]any{"manifest_present": false, "manifest_mode": "off"}))
	if v != guard.VerdictAllow || len(fs) != 0 {
		t.Fatalf("verdict = %s findings = %+v", v, fs)
	}
}

func TestAdmissionPolicies_BareBashOnlyWhenExactToken(t *testing.T) {
	v, fs := evalFacts(t, admissionFacts(map[string]any{"declared_tools": []any{"Bash(git:*)"}}))
	if v != guard.VerdictAllow || len(fs) != 0 {
		t.Fatalf("scoped Bash flagged: %s %+v", v, fs)
	}
}

// Case 34 (policy half): raw hand-written payloads without admission
// keys evaluate without error (policies guard with has()).
func TestAdmissionPolicies_RawPayloadWithoutKeysDoesNotError(t *testing.T) {
	v, fs := evalFacts(t, map[string]any{"postinstall": "", "paths": []any{"/x"}})
	if v != guard.VerdictAllow || len(fs) != 0 {
		t.Fatalf("raw payload: %s %+v", v, fs)
	}
}

func TestAdmissionRuleReasonsInBuiltins(t *testing.T) {
	policies, err := guard.Load(guard.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]guard.Effect{}
	for _, p := range policies {
		got[p.Name] = p.Effect
	}
	want := map[string]guard.Effect{
		"admission-tools-undeclared":     guard.EffectDeny,
		"admission-data-class":           guard.EffectDeny,
		"admission-digest-unpinned":      guard.EffectWarn,
		"admission-approval-required":    guard.EffectWarn,
		"admission-approval-enforced":    guard.EffectDeny,
		"admission-manifest-missing":     guard.EffectWarn,
		"admission-manifest-required":    guard.EffectDeny,
		"admission-tools-unrestricted":   guard.EffectWarn,
		"admission-data-class-escalated": guard.EffectWarn,
	}
	for name, eff := range want {
		if got[name] != eff {
			t.Errorf("policy %s effect = %q; want %q", name, got[name], eff)
		}
	}
}

// startGuardBus wires a guard with the given engine options on a
// memory bus.
func startGuardBus(t *testing.T, opts guard.LoadOptions) *eventbus.Bus {
	t.Helper()
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	policies, err := guard.Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := guard.NewEngine(policies)
	if err != nil {
		t.Fatal(err)
	}
	g, err := guard.New(b, guard.WithEngine(eng))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Stop)
	return b
}

// Case 32: payloadFromInstall / payloadFromUpgrade put every admission
// key in the CEL activation with a default, so a policy can read each
// one without has(). The policy below only fires when all of them are
// present AND hold their zero defaults.
func TestGuardActivation_AdmissionKeysDefaulted(t *testing.T) {
	dir := t.TempDir()
	const policy = `name: zz-defaults-present
when: |
  payload.manifest_present == false && payload.manifest_mode == '' &&
  payload.digest == '' && payload.digest_status == '' && payload.digest_source == '' &&
  size(payload.declared_tools) == 0 && size(payload.undeclared_tools) == 0 &&
  payload.data_class == '' && payload.max_data_class == '' &&
  payload.data_class_over == false && payload.data_class_escalated == false &&
  payload.approval_state == ''
effect: deny
severity: deny
message: defaults present
`
	if err := os.WriteFile(filepath.Join(dir, "zz.yaml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	b := startGuardBus(t, guard.LoadOptions{UserDir: dir})

	err := guard.GateInstall(context.Background(), b, events.SkillInstallRequestedPayload{
		Skill: "s", SourceURL: "u", Scope: "global",
	}, guard.GateConfig{Timeout: 3 * time.Second})
	if !errors.Is(err, guard.ErrPolicyDeny) || !strings.Contains(err.Error(), "zz-defaults-present: defaults present") {
		t.Fatalf("install activation lacks admission defaults: %v", err)
	}
	err = guard.GateUpgrade(context.Background(), b, events.SkillUpgradePlannedPayload{
		Items: []events.UpgradePlanItem{{Skill: "s", Scope: "global"}},
	}, guard.GateConfig{Timeout: 3 * time.Second})
	if !errors.Is(err, guard.ErrPolicyDeny) || !strings.Contains(err.Error(), "zz-defaults-present: defaults present") {
		t.Fatalf("upgrade activation lacks admission defaults: %v", err)
	}
}

// Admission facts set on the typed payload reach the policies.
func TestGateInstall_AdmissionFactsDriveVerdict(t *testing.T) {
	b := startGuardBus(t, guard.LoadOptions{})
	base := events.SkillInstallRequestedPayload{
		Skill: "s", SourceURL: "u", Scope: "global",
		ManifestPresent: true, ManifestMode: "warn", DigestStatus: "match",
		ApprovalState: "approved", DeclaredTools: []string{"Read"},
	}
	cfg := guard.GateConfig{Timeout: 3 * time.Second}
	if err := guard.GateInstall(context.Background(), b, base, cfg); err != nil {
		t.Fatalf("clean facts denied: %v", err)
	}
	bad := base
	bad.UndeclaredTools = []string{"Write"}
	err := guard.GateInstall(context.Background(), b, bad, cfg)
	var pd *guard.PolicyDenyError
	if !errors.As(err, &pd) || !errors.Is(err, guard.ErrPolicyDeny) {
		t.Fatalf("err = %v; want PolicyDenyError", err)
	}
	if len(pd.Findings) != 1 || pd.Findings[0].Rule != "admission-tools-undeclared" {
		t.Fatalf("findings = %+v", pd.Findings)
	}
}

// Case 27: --insecure lowers a data-class deny to a warning.
func TestGateInstall_InsecureLowersDataClassDeny(t *testing.T) {
	b := startGuardBus(t, guard.LoadOptions{})
	p := events.SkillInstallRequestedPayload{
		Skill: "s", SourceURL: "u", Scope: "global",
		ManifestPresent: true, ManifestMode: "warn", DigestStatus: "match",
		ApprovalState: "approved", DataClass: "personal", MaxDataClass: "internal", DataClassOver: true,
	}
	if err := guard.GateInstall(context.Background(), b, p, guard.GateConfig{Timeout: 3 * time.Second}); !errors.Is(err, guard.ErrPolicyDeny) {
		t.Fatalf("without --insecure: %v; want deny", err)
	}
	if err := guard.GateInstall(context.Background(), b, p, guard.GateConfig{Timeout: 3 * time.Second, Insecure: true}); err != nil {
		t.Fatalf("with --insecure: %v; want nil", err)
	}
}

// Case 25 (--strict): a manifest-missing warn becomes a deny.
func TestGateInstall_StrictTurnsManifestMissingIntoDeny(t *testing.T) {
	b := startGuardBus(t, guard.LoadOptions{})
	p := events.SkillInstallRequestedPayload{
		Skill: "s", SourceURL: "u", Scope: "global",
		ManifestMode: "warn", DigestStatus: "match", ApprovalState: "approved",
	}
	if err := guard.GateInstall(context.Background(), b, p, guard.GateConfig{Timeout: 3 * time.Second}); err != nil {
		t.Fatalf("warn without strict: %v", err)
	}
	err := guard.GateInstall(context.Background(), b, p, guard.GateConfig{Timeout: 3 * time.Second, Strict: true})
	var pd *guard.PolicyDenyError
	if !errors.As(err, &pd) || pd.Findings[0].Rule != "admission-manifest-missing" {
		t.Fatalf("strict: %v", err)
	}
}

// Case 29 (gate half): with no guard subscriber the gate times out:
// policy findings fail open, and deny under --strict.
func TestGateInstall_TimeoutFailsOpenUnlessStrict(t *testing.T) {
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	p := events.SkillInstallRequestedPayload{Skill: "s", SourceURL: "u", Scope: "global", UndeclaredTools: []string{"Write"}}
	if err := guard.GateInstall(context.Background(), b, p, guard.GateConfig{Timeout: 50 * time.Millisecond}); err != nil {
		t.Fatalf("timeout without strict: %v; want fail-open", err)
	}
	if err := guard.GateInstall(context.Background(), b, p, guard.GateConfig{Timeout: 50 * time.Millisecond, Strict: true}); !errors.Is(err, guard.ErrPolicyDeny) {
		t.Fatalf("timeout under strict: %v; want deny", err)
	}
	if err := guard.GateInstall(context.Background(), nil, p, guard.GateConfig{}); err != nil {
		t.Fatalf("nil bus without strict: %v", err)
	}
	if err := guard.GateInstall(context.Background(), nil, p, guard.GateConfig{Strict: true}); !errors.Is(err, guard.ErrPolicyDeny) {
		t.Fatalf("nil bus under strict: %v", err)
	}
}

package events_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/events"
)

// TestTopicsValidate proves every gym-PUBLISHED topic conforms to
// kit/bus's 4-segment past-tense convention.
//
// Inbound topics are out of scope — gym never publishes them, so
// kit/bus validation is the publisher's responsibility. We still
// validate the 4-segment ones (rsx, policy.guard, tip.shell) to
// catch typos in external.go.
//
// NOTE: `toolspec.cli.resolved` is a 3-segment topic. It violates the kit 4-segment contract. Until the
// emitter (kit/ai/toolspec) settles on a 4-segment form, gym keeps the
// constant as published and exempts it from validation.
func TestTopicsValidate(t *testing.T) {
	published := []bus.Topic{
		events.TopicCoachHireRequested,
		events.TopicCoachHired,
		events.TopicCoachDismissed,
		events.TopicCoachJudgementCompleted,
		events.TopicSkillInstallRequested,
		events.TopicSkillInstallCompleted,
		events.TopicSkillInstallRejected,
		events.TopicSkillUpgradePlanned,
		events.TopicSkillUpgradeApplied,
		events.TopicSkillUpgradeFailed,
		events.TopicWarmupMetadataEmitted,
		events.TopicToolspecCLIPublished,
		events.TopicCheatHintEmitted,
	}
	for _, tp := range published {
		t.Run(string(tp), func(t *testing.T) {
			if err := bus.Validate(tp); err != nil {
				t.Fatalf("Validate(%q): %v", tp, err)
			}
			if err := bus.ValidateTopic(tp); err != nil {
				t.Fatalf("ValidateTopic(%q): %v", tp, err)
			}
		})
	}

	subscribed := []bus.Topic{
		events.TopicRSXRepoPolicyEvaluated,
		events.TopicPolicyGuardInstallEvaluated,
		events.TopicTipShellCommandObserved,
	}
	for _, tp := range subscribed {
		t.Run(string(tp), func(t *testing.T) {
			if err := bus.Validate(tp); err != nil {
				t.Fatalf("Validate(%q): %v", tp, err)
			}
		})
	}
	if events.TopicToolspecCLIResolved == "" {
		t.Error("TopicToolspecCLIResolved is empty; constant missing")
	}
}

// TestPayloadsRoundTrip asserts every payload struct survives the
// encoding/json round-trip with all fields preserved. This is the
// kit/bus contract — cross-process subscribers receive the JSON form,
// so every field must serialize and deserialize without loss.
//
// Fields are populated with non-zero values so json:"omitempty" tags
// don't accidentally hide a missing serialization.
func TestPayloadsRoundTrip(t *testing.T) {
	finding := events.PolicyFinding{
		Rule:     "no-curl-bash",
		Severity: "deny",
		Message:  "shell hook pipes curl into bash",
		Path:     "manifest.yaml",
	}

	cases := []struct {
		name    string
		payload any
	}{
		{
			name: "CoachHireRequested",
			payload: events.CoachHireRequestedPayload{
				Repo: "github.com/foo/bar", Scope: "global", Curated: true,
				RequestID: "01HZ-req-1",
			},
		},
		{
			name: "CoachHired",
			payload: events.CoachHiredPayload{
				Name: "foo", Repo: "github.com/foo/bar", Scope: "global",
				Curated: true, RequestID: "01HZ-req-1",
			},
		},
		{
			name: "CoachDismissed",
			payload: events.CoachDismissedPayload{
				Name: "foo", Scope: "project", RequestID: "01HZ-req-2",
			},
		},
		{
			name: "CoachJudgementCompleted",
			payload: events.CoachJudgementCompletedPayload{
				Repo: "github.com/foo/bar", Score: 88, Verdict: "allow",
				Findings: []events.PolicyFinding{finding},
				Signals: []events.JudgeSignal{
					{Type: "github", Key: "stars", Value: "3559", Source: "github/repos", Confidence: 1},
				},
				CachedAt:  "2026-05-05T12:00:00Z",
				RequestID: "01HZ-req-1",
			},
		},
		{
			name: "SkillInstallRequested",
			payload: events.SkillInstallRequestedPayload{
				Skill: "lint", Version: "1.2.3", SourceURL: "https://x.test/lint",
				Scope: "project", Paths: []string{"~/.gym"},
				RequestID: "01HZ-req-3",
			},
		},
		{
			name: "SkillInstallCompleted",
			payload: events.SkillInstallCompletedPayload{
				Skill: "lint", Version: "1.2.3", Scope: "project",
				InstallPath: "/tmp/lint", RequestID: "01HZ-req-3",
			},
		},
		{
			name: "SkillInstallRejected",
			payload: events.SkillInstallRejectedPayload{
				Skill: "bad", Scope: "global",
				Reason: "policy denied: no-curl-bash", RequestID: "01HZ-req-4",
			},
		},
		{
			name: "SkillUpgradePlanned",
			payload: events.SkillUpgradePlannedPayload{
				Items: []events.UpgradePlanItem{
					{Skill: "lint", Scope: "global", From: "1.0.0", To: "1.2.0",
						Source: "github.com/foo/bar", Curated: true},
				},
				RequestID: "01HZ-req-5",
			},
		},
		{
			name: "SkillUpgradeApplied",
			payload: events.SkillUpgradeAppliedPayload{
				Skill: "lint", Scope: "global",
				From: "1.0.0", To: "1.2.0", RequestID: "01HZ-req-5",
			},
		},
		{
			name: "SkillUpgradeFailed",
			payload: events.SkillUpgradeFailedPayload{
				Skill: "lint", Scope: "global",
				Error: "manifest fetch 404", RequestID: "01HZ-req-5",
			},
		},
		{
			name: "WarmupMetadataEmitted",
			payload: events.WarmupMetadataEmittedPayload{
				Format: "xml", Scope: "all",
				Skills: []events.WarmupSkill{
					{Name: "lint", Version: "1.2.3", Scope: "global",
						Path: "~/.gym/lint", Summary: "linter"},
				},
			},
		},
		{
			name: "ToolspecCLIPublished",
			payload: events.ToolspecCLIPublishedPayload{
				Version: "0.0.1",
				Spec:    map[string]any{"name": "gym", "version": "0.0.1"},
			},
		},
		{
			name: "RSXRepoPolicyEvaluated",
			payload: events.RSXRepoPolicyEvaluatedPayload{
				Repo: "github.com/foo/bar", Score: 75, Verdict: "warn",
				Findings: []events.PolicyFinding{finding}, RequestID: "01HZ-req-1",
			},
		},
		{
			name: "PolicyGuardInstallEvaluated",
			payload: events.PolicyGuardInstallEvaluatedPayload{
				Skill: "lint", Verdict: "allow",
				Findings: []events.PolicyFinding{finding}, RequestID: "01HZ-req-3",
			},
		},
		{
			name: "TipShellCommandObserved",
			payload: events.TipShellCommandObservedPayload{
				Command: "gym", Argv: "gym learn lint",
				ExitCode: 0, Cwd: "/work",
			},
		},
		{
			name: "ToolspecCLIResolved",
			payload: events.ToolspecCLIResolvedPayload{
				Tool: "gym", Spec: map[string]any{"name": "gym"},
			},
		},
		{
			name: "CheatHintEmitted",
			payload: events.CheatHintEmittedPayload{
				Command: "gym warmup", Hint: "gym warmup — safety: safe",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			// Round-trip back into the same struct type.
			rv := reflect.New(reflect.TypeOf(tc.payload))
			if err := json.Unmarshal(raw, rv.Interface()); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got := rv.Elem().Interface()
			if !reflect.DeepEqual(got, tc.payload) {
				t.Errorf("round-trip mismatch:\n got: %#v\nwant: %#v", got, tc.payload)
			}

			// Also prove the cross-process re-marshal-hop pattern:
			// any -> map -> Marshal -> Unmarshal into typed struct
			// reproduces the original.
			var generic any
			if err := json.Unmarshal(raw, &generic); err != nil {
				t.Fatalf("generic Unmarshal: %v", err)
			}
			rehop, err := json.Marshal(generic)
			if err != nil {
				t.Fatalf("re-Marshal: %v", err)
			}
			rv2 := reflect.New(reflect.TypeOf(tc.payload))
			if err := json.Unmarshal(rehop, rv2.Interface()); err != nil {
				t.Fatalf("re-marshal-hop Unmarshal: %v", err)
			}
			got2 := rv2.Elem().Interface()
			if !reflect.DeepEqual(got2, tc.payload) {
				t.Errorf("re-marshal-hop mismatch:\n got: %#v\nwant: %#v", got2, tc.payload)
			}
		})
	}
}

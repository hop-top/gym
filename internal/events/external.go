package events

import "hop.top/kit/go/runtime/bus"

// External topics gym subscribes to. Constants are exported so
// subscribers (judge, guard, cheat) reference one canonical name.
const (
	// TopicRSXRepoPolicyEvaluated carries a verdict from rsx for a
	// coach repository under consideration. Correlated by RequestID.
	TopicRSXRepoPolicyEvaluated bus.Topic = "rsx.repo.policy.evaluated"

	// TopicPolicyGuardInstallEvaluated carries the CEL guard's
	// verdict on a skill install / upgrade. Correlated by RequestID.
	TopicPolicyGuardInstallEvaluated bus.Topic = "policy.guard.install.evaluated"

	// TopicTipShellCommandObserved is tip's "user just ran X"
	// signal. Subscribed only when GYM_TIP_OBSERVE=1; off by default.
	TopicTipShellCommandObserved bus.Topic = "tip.shell.command.observed"

	// TopicToolspecCLIResolved is emitted by remote spec sources.
	TopicToolspecCLIResolved bus.Topic = "toolspec.cli.resolved"
)

// PolicyFinding is a single signal surfaced by a policy or analyzer.
type PolicyFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message,omitempty"`
	Path     string `json:"path,omitempty"`
}

// RSXRepoPolicyEvaluatedPayload is rsx's verdict on a repo. Verdict is
// one of: "allow", "warn", "deny".
type RSXRepoPolicyEvaluatedPayload struct {
	Repo      string          `json:"repo"`
	Score     int             `json:"score"`
	Verdict   string          `json:"verdict"`
	Findings  []PolicyFinding `json:"findings,omitempty"`
	RequestID string          `json:"request_id"`
}

// PolicyGuardInstallEvaluatedPayload is the CEL guard's verdict on an
// install/upgrade. Verdict is one of: "allow", "warn", "deny".
type PolicyGuardInstallEvaluatedPayload struct {
	Skill     string          `json:"skill"`
	Verdict   string          `json:"verdict"`
	Findings  []PolicyFinding `json:"findings,omitempty"`
	RequestID string          `json:"request_id"`
}

// TipShellCommandObservedPayload is tip's "the user just ran X"
// notification. Shape mirrors the (env-gated) tip publish contract.
type TipShellCommandObservedPayload struct {
	Command  string `json:"command"`
	Argv     string `json:"argv,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
}

// ToolspecCLIResolvedPayload is what a remote toolspec source emits
// when it has resolved a spec on gym's behalf. Spec is `any` for the
// same reason as ToolspecCLIPublishedPayload.Spec.
type ToolspecCLIResolvedPayload struct {
	Tool string `json:"tool"`
	Spec any    `json:"spec,omitempty"`
}

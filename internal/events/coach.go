package events

import "hop.top/kit/go/runtime/bus"

// Coach lifecycle topics.
//
// hire_requested initiates a sync gate awaiting the rsx judgement
// correlated by RequestID. hired/dismissed are post-commit
// fire-and-forget signals. judgement_completed carries gym's own
// rolled-up verdict (rsx analyze + rsx policy test + cache lookup)
// answering a hire_requested correlated by RequestID.
const (
	TopicCoachHireRequested      bus.Topic = "gym.coach.repository.hire_requested"
	TopicCoachHired              bus.Topic = "gym.coach.repository.hired"
	TopicCoachDismissed          bus.Topic = "gym.coach.repository.dismissed"
	TopicCoachJudgementCompleted bus.Topic = "gym.coach.judgement.completed"
)

// CoachHireRequestedPayload is published when the user runs
// `gym coach hire`. Subscribers (e.g. internal/judge) reply
// with rsx.repo.policy.evaluated correlated by RequestID.
type CoachHireRequestedPayload struct {
	Repo      string `json:"repo"`
	Scope     string `json:"scope"`
	Curated   bool   `json:"curated"`
	RequestID string `json:"request_id"`
}

// CoachHiredPayload is published after a coach has been committed to
// the user/project config.
type CoachHiredPayload struct {
	Name      string `json:"name"`
	Repo      string `json:"repo"`
	Scope     string `json:"scope"`
	Curated   bool   `json:"curated"`
	RequestID string `json:"request_id,omitempty"`
}

// CoachDismissedPayload is published after a coach has been removed
// from the user/project config.
type CoachDismissedPayload struct {
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	RequestID string `json:"request_id,omitempty"`
}

// CoachJudgementCompletedPayload is gym's rolled-up verdict on a
// coach repository, derived from rsx analyze + rsx policy test (run
// out-of-process) and surfaced by internal/judge. Correlated by
// RequestID with the originating CoachHireRequestedPayload.
//
// Verdict is one of "allow" | "warn" | "deny". Score is rsx's overall
// score scaled to 0..100 (rsx's native 0..10 multiplied by 10 and
// rounded). Findings carry the policy hits surfaced to the user;
// Signals carry rsx's evidence list (key,value,source) for downstream
// inspection.
type CoachJudgementCompletedPayload struct {
	Repo      string          `json:"repo"`
	Score     int             `json:"score"`
	Verdict   string          `json:"verdict"`
	Findings  []PolicyFinding `json:"findings,omitempty"`
	Signals   []JudgeSignal   `json:"signals,omitempty"`
	CachedAt  string          `json:"cached_at,omitempty"`
	RequestID string          `json:"request_id"`
}

// JudgeSignal mirrors a single rsx evidence entry. Kept here (rather
// than in external.go alongside PolicyFinding) because it is gym's own
// roll-up shape — rsx's wire shape lives behind the subprocess driver
// and is mapped into JudgeSignal before publishing.
type JudgeSignal struct {
	Type       string  `json:"type,omitempty"`
	Key        string  `json:"key"`
	Value      string  `json:"value,omitempty"`
	Source     string  `json:"source,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

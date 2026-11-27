// Package judge is the rsx-backed coach judging subsystem.
//
// # Why behind the bus
//
// gym never calls rsx directly. Instead, gym publishes
// [events.TopicCoachHireRequested] on the kit/runtime/bus and the
// [Judge] subscriber answers with [events.TopicCoachJudgementCompleted]
// after invoking rsx subprocesses. The indirection costs one local
// publish/subscribe round-trip but keeps the call site (gym coach hire)
// completely decoupled from rsx's release cadence and its eventual
// native bus publishing — when rsx grows its own publisher we drop the
// subprocess driver here without touching coach.go.
//
// # Subprocess contract
//
// rsx is shelled out twice in parallel:
//
//   - rsx analyze <repo> --json
//   - rsx policy test <repo> --json --policy balanced
//
// Both calls are bounded by a 30 s [context.WithTimeout] (configurable
// via [Option]). Stdout is captured and decoded into a small subset of
// rsx's JSON schema (see rsx.go). gym's roll-up:
//
//   - score = round(rsx.scores.overall * 10)  // 0..100
//   - verdict = allow|warn|deny derived from rsx.policy.passed plus the
//     score band (≥70 allow, ≥40 warn, else deny). When rsx's policy
//     decision is "fail" but the score band is high we keep the policy
//     verdict and surface a finding describing why.
//   - findings = rsx.policy.violations or rsx.summary.recommendation
//     mapped into gym's [events.PolicyFinding] shape.
//   - signals = rsx.evidence (key,value,source,confidence).
//
// # Cache
//
// Judgements are cached at $XDG_CACHE_HOME/gym/judgements/<sha256>.json
// keyed on the canonicalised coach URL. Cache hits skip the subprocess.
// The persistent --refresh-judgement flag bypasses cache.
//
// The cache key intentionally elides the remote HEAD: gym does not
// shell out to git to read the remote ref before judging (that would
// double the network cost on the hot path). Cache freshness is bounded
// by [judge.DefaultMaxAge] (24 h); older entries are treated as a
// cache miss. --refresh-judgement is the explicit override.
//
// # Idempotency
//
// The subscriber is idempotent on RequestID: receiving the same
// CoachHireRequested twice yields the same publish (subject to
// race-free cache lookup). kit/bus has no built-in dedupe so this is
// the consumer's job.
//
// # Fail-open vs fail-closed
//
// Subprocess failures (non-zero exit, JSON shape change, exec error)
// fail-open by default with a WARN log. When --strict is set
// (eventbus.IsStrict) they fail-closed and the judgement carries
// verdict="deny" so coach hire blocks. The 30 s subprocess timeout
// follows the same rule.
package judge

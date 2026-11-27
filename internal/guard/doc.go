// Package guard is gym's CEL-driven policy gatekeeper for skill
// installs and upgrades. It subscribes to gym.skill.install.requested
// and gym.skill.upgrade.planned, evaluates every applicable policy, and
// publishes policy.guard.install.evaluated correlated by request_id.
//
// # Engine
//
// The engine is a thin wrapper around hop.top/kit/go/runtime/policy/cel
// (the CEL evaluator from kit/runtime/policy). It is *not* the kit
// policy.Engine: kit's engine hard-codes its veto-able topics to
// kit.runtime.* and only supports allow|deny. Gym needs a third
// "warn" verdict and bus topics outside that allow-list, so we keep the
// CEL backend and add our own resolution loop.
//
// Policies are plain YAML files. Built-in defaults live in
// internal/guard/policies/. User overrides live in
// $XDG_CONFIG_HOME/gym/policies/, project overrides in
// <root>/.gym/policies/. Override files can either redefine a policy
// (same name wins) or supply a top-level policies.yaml with disable: /
// relax: declarations.
//
// # Activation shape
//
// Each policy receives a CEL activation map:
//
//	principal: { id, role, source }     -- kit/policy default resolver
//	context:   { scope, source_url, request_id, paths } -- per-request
//	resource:  { skill, version, scope, ... }           -- gym fields
//	payload:   { ...full event payload as a map }       -- raw bus event
//
// When 'when' evaluates to true the policy returns Effect (default
// "deny"); otherwise Otherwise (default "allow"). Severity carries
// through to the final verdict at gym's resolution layer:
//
//   - any policy with effect=DENY at runtime -> overall verdict "deny"
//   - else any policy with effect=WARN at runtime -> overall verdict "warn"
//   - else "allow"
//
// Findings are surfaced for every triggered policy (allow + warn +
// deny) so callers can show the audit trail.
//
// # Verdict semantics on the install/upgrade pipeline
//
// install.go and upgrade/planner.go publish gym.skill.install.requested
// and gym.skill.upgrade.planned, then block on
// policy.guard.install.evaluated via Bus.RequestResponse. The verdict
// is one of "allow" / "warn" / "deny":
//
//   - allow: proceed silently.
//   - warn: surface findings via App.Logger().Warn, proceed.
//   - deny: error out unless --insecure (deny -> warn) is set.
//
// On timeout, eventbus.IsStrict(viper) decides: strict -> fail-closed
// (deny), non-strict -> fail-open with warning.
//
// # Adding a new policy
//
// 1. Drop a YAML file under internal/guard/policies/ with shape:
//
//	name: no-foo
//	when: payload.field == "bad"
//	effect: deny           # or warn
//	otherwise: allow       # default; can omit
//	severity: deny         # default verdict surface; deny|warn|allow
//	message: "human-friendly reason"
//
// 2. Add a per-policy unit test in guard_test.go.
// 3. Document the policy in the guard docs.
//
// User overrides at $XDG_CONFIG_HOME/gym/policies/policies.yaml may
// disable: ["no-foo"] or relax: ["no-foo"] (relax demotes deny -> warn).
package guard

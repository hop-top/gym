// Package events defines gym's event-bus topic catalog and the typed
// payload structs that travel under each topic.
//
// # Naming
//
// Every gym-originated topic follows kit/bus's 4-segment past-tense
// convention: "[Source].[Category].[Object].[Action]". The source
// segment is always "gym". See hop.top/kit/go/runtime/bus.ValidateTopic
// for the precise rules.
//
// # Payload contract
//
// Payloads MUST round-trip cleanly through encoding/json (kit/bus
// rule). In-process subscribers receive the original Go struct value
// the publisher passed in; subscribers in other processes (sqlite or
// network adapter) receive the JSON-decoded form, where objects become
// map[string]any, arrays []any, and numbers float64.
//
// Typed consumers SHOULD re-marshal-hop:
//
//	raw, _ := json.Marshal(e.Payload)
//	var p events.SkillInstallRequestedPayload
//	if err := json.Unmarshal(raw, &p); err != nil { ... }
//
// # Idempotency on request_id
//
// Request/response pairs (e.g. hire_requested -> rsx.repo.policy.evaluated,
// install.requested -> policy.guard.install.evaluated) are correlated
// by RequestID (uuid v7). Subscribers MUST be idempotent on RequestID:
// receiving the same request twice MUST yield the same outcome.
//
// # Adding a new topic
//
//  1. Add a constant in the appropriate file (coach.go / skill.go /
//     warmup.go / toolspec.go for outbound, external.go for inbound).
//  2. Define a typed payload struct with json tags.
//  3. Extend the round-trip test in topics_roundtrip_test.go with a
//     populated fixture for the new payload.
package events

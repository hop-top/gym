package events

import "hop.top/kit/go/runtime/bus"

// gym.toolspec.cli.published is fired once at boot with a snapshot of
// gym's CLI surface. The cheat package populates the Spec field.
const TopicToolspecCLIPublished bus.Topic = "gym.toolspec.cli.published"

// gym.cheat.hint.emitted carries a hint produced by the optional
// tip-bridge subscriber (cheat.MaybeStartBridge) in response to a
// tip.shell.command.observed event. Off by default; gated on
// GYM_TIP_BUS_BRIDGE=1.
const TopicCheatHintEmitted bus.Topic = "gym.cheat.hint.emitted"

// ToolspecCLIPublishedPayload carries the gym CLI tool spec. The Spec
// field is intentionally `any` so this package does not depend on
// kit/ai/toolspec — subscribers re-marshal-hop into their own type.
type ToolspecCLIPublishedPayload struct {
	// Version is the gym binary version that emitted the spec.
	Version string `json:"version,omitempty"`
	// Spec is the kit/ai/toolspec.ToolSpec value, kept as `any` so
	// this package stays toolspec-free and round-trips through JSON
	// per the kit/bus payload contract.
	Spec any `json:"spec,omitempty"`
}

// CheatHintEmittedPayload is published by the optional tip-bridge
// subscriber. Carries a single-line hint that tip can render directly.
type CheatHintEmittedPayload struct {
	Command string `json:"command"`
	Hint    string `json:"hint"`
}

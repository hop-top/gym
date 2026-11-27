// Package cheat builds, merges, and serves cheat-sheet entries for gym
// itself and every installed skill.
//
// # Three responsibilities
//
//  1. Self-spec — at boot, walk gym's cobra command tree and emit a
//     hop.top/kit/go/ai/toolspec.ToolSpec describing it. Published on
//     events.TopicToolspecCLIPublished.
//  2. Skill specs — for each installed skill, derive a lightweight
//     ToolSpec from its SKILL.md frontmatter (commands + examples) and
//     merge into the registry.
//  3. tip adapter — `gym cheat --tip-export` writes a JSON adapter file
//     at <tip-config>/adapters/gym.json modelled on tip's adapter
//     describe/suggest contract (see hop.top/tip docs/adapter-protocol.md).
//
// # Schema version
//
// gym pins ToolSpec.SchemaVersion explicitly to
// [SchemaVersion]. Bump this constant only on a breaking change to the
// emitted shape.
//
// # Bus opt-in
//
// Subscribing to events.TopicTipShellCommandObserved is opt-in. Set
// GYM_TIP_BUS_BRIDGE=1 to enable the cheat-sheet bridge that answers
// tip's "user just ran X" notifications from the merged registry. When
// disabled (the default), the registry is still published on
// gym.toolspec.cli.published at boot — tip can pull the spec via the
// adapter file or the published event without gym intercepting tip's
// shell-command stream.
//
// # Tip adapter file
//
// The exported adapter file mirrors hop.top/tip/adapter.DescribeResponse
// at the top level so an eventual tip "static adapter" loader can
// consume it without code changes. The full ToolSpec is embedded under
// the "spec" key for consumers (e.g. tip's Generic adapter) that want
// the rich data.
package cheat

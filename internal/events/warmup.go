package events

import "hop.top/kit/go/runtime/bus"

// Warmup metadata topic.
const TopicWarmupMetadataEmitted bus.Topic = "gym.warmup.metadata.emitted"

// WarmupSkill is the lightweight per-skill descriptor surfaced when
// `gym warmup` runs. It is intentionally a flat shape so subscribers
// don't need to depend on the warmup package's internal types.
type WarmupSkill struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Path    string `json:"path,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// WarmupMetadataEmittedPayload is published once per `gym warmup`
// invocation. tip's index can subscribe to populate hint context.
type WarmupMetadataEmittedPayload struct {
	Format string        `json:"format"`
	Scope  string        `json:"scope"`
	Skills []WarmupSkill `json:"skills,omitempty"`
}

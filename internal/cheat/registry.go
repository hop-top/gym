package cheat

import (
	"sync"

	"hop.top/kit/go/ai/toolspec"
)

// Registry merges gym's self-spec with skill-specs and exposes
// FindCommand / Lookup helpers. The underlying kit Registry is wrapped
// rather than embedded so tests can inject merged specs directly
// without going through a Source/Cache plumbing dance.
type Registry struct {
	mu    sync.RWMutex
	self  *toolspec.ToolSpec
	skill map[string]toolspec.ToolSpec // name -> spec
}

// NewRegistry constructs an empty registry. Use Update to populate it.
func NewRegistry() *Registry {
	return &Registry{skill: make(map[string]toolspec.ToolSpec)}
}

// Update replaces the registry contents in one swap. Either argument
// may be nil/empty.
func (r *Registry) Update(self *toolspec.ToolSpec, skills []toolspec.ToolSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.self = self
	r.skill = make(map[string]toolspec.ToolSpec, len(skills))
	for _, s := range skills {
		if s.Name == "" {
			continue
		}
		r.skill[s.Name] = s
	}
}

// Self returns the gym self-spec or nil.
func (r *Registry) Self() *toolspec.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.self
}

// Skills returns a snapshot of skill specs sorted by name.
func (r *Registry) Skills() []toolspec.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]toolspec.ToolSpec, 0, len(r.skill))
	for _, s := range r.skill {
		out = append(out, s)
	}
	sortSpecs(out)
	return out
}

// Lookup resolves a topic name (gym subcommand path or skill name)
// into the matching ToolSpec / Command. Returns ok=false when the
// topic is unknown.
//
//   - "gym" or "" -> the self-spec
//   - "warmup", "learn", "coach hire", "coach.hire" -> a Command in the self-spec
//   - "<skill-name>" -> the skill's ToolSpec
func (r *Registry) Lookup(topic string) (LookupResult, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t := normaliseTopic(topic)
	if t == "" || t == ToolName {
		if r.self == nil {
			return LookupResult{}, false
		}
		return LookupResult{Kind: KindSelf, Spec: r.self}, true
	}
	if r.self != nil {
		if cmd := findCommandByPath(r.self.Commands, splitDots(t)); cmd != nil {
			return LookupResult{Kind: KindGymCommand, Spec: r.self, Command: cmd}, true
		}
	}
	if s, ok := r.skill[t]; ok {
		spec := s
		return LookupResult{Kind: KindSkill, Spec: &spec}, true
	}
	return LookupResult{}, false
}

// LookupKind categorises a Lookup hit.
type LookupKind int

const (
	// KindSelf indicates the result is the gym self-spec.
	KindSelf LookupKind = iota
	// KindGymCommand indicates the result is one Command inside the
	// gym self-spec.
	KindGymCommand
	// KindSkill indicates the result is a skill ToolSpec.
	KindSkill
)

// LookupResult bundles the matched spec/command for Render.
type LookupResult struct {
	Kind    LookupKind
	Spec    *toolspec.ToolSpec
	Command *toolspec.Command
}

// findCommandByPath walks a command tree along path segments. Accepts
// space- or dot-separated paths via splitDots/normaliseTopic.
func findCommandByPath(cmds []toolspec.Command, path []string) *toolspec.Command {
	if len(path) == 0 {
		return nil
	}
	for i := range cmds {
		if cmds[i].Name == path[0] {
			if len(path) == 1 {
				return &cmds[i]
			}
			if c := findCommandByPath(cmds[i].Children, path[1:]); c != nil {
				return c
			}
		}
	}
	return nil
}

// normaliseTopic lowercases the topic and rewrites "/", " " to ".".
func normaliseTopic(topic string) string {
	s := topic
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t', '/':
			out = append(out, '.')
		default:
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			out = append(out, c)
		}
	}
	return string(out)
}

func splitDots(s string) []string {
	if s == "" {
		return nil
	}
	parts := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			if i > start {
				parts = append(parts, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

func sortSpecs(out []toolspec.ToolSpec) {
	// Tiny inline insertion sort; specs lists are small (1-100).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
}

package cheat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
)

// BusBridgeEnvVar gates the optional subscriber on
// events.TopicTipShellCommandObserved. Off by default.
const BusBridgeEnvVar = "GYM_TIP_BUS_BRIDGE"

// TipBudget caps how long the bridge spends building a hint before
// publishing back. Mirrors tip's documented 500ms ceiling.
const TipBudget = 500 * time.Millisecond

// MaybeStartBridge subscribes to TopicTipShellCommandObserved when
// GYM_TIP_BUS_BRIDGE=1 is set in the environment. Returns the
// unsubscribe handle so callers (e.g. App.Close) can drop the
// subscription cleanly. When the env var is unset/false, returns nil.
func MaybeStartBridge(b *eventbus.Bus, registry *Registry) bus.Unsubscribe {
	if !envEnabled(BusBridgeEnvVar) {
		return nil
	}
	if b == nil || registry == nil {
		return nil
	}
	return b.Subscribe(string(events.TopicTipShellCommandObserved),
		func(ctx context.Context, e bus.Event) error {
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, cancel := context.WithTimeout(ctx, TipBudget)
			defer cancel()

			var p events.TipShellCommandObservedPayload
			raw, _ := json.Marshal(e.Payload)
			_ = json.Unmarshal(raw, &p)

			hint, ok := answerFromRegistry(registry, p.Command)
			if !ok {
				return nil
			}
			// Publish the hint back on the same bus under a
			// gym-owned 4-segment topic. tip can subscribe; tests can
			// observe.
			return b.Publish(ctx, events.TopicCheatHintEmitted, events.CheatHintEmittedPayload{
				Command: p.Command,
				Hint:    hint,
			})
		})
}

// answerFromRegistry consults the registry for a hint matching cmd.
// Strips any leading "gym " prefix so `gym warmup` matches the warmup
// node in the self-spec.
func answerFromRegistry(r *Registry, cmd string) (string, bool) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", false
	}
	cmd = strings.TrimPrefix(cmd, "gym ")
	res, ok := r.Lookup(cmd)
	if !ok {
		return "", false
	}
	switch res.Kind {
	case KindSelf:
		return fmt.Sprintf("gym — %d top-level commands", len(res.Spec.Commands)), true
	case KindGymCommand:
		if res.Command.Safety != nil {
			return fmt.Sprintf("gym %s — safety: %s", res.Command.Name, res.Command.Safety.Level), true
		}
		return fmt.Sprintf("gym %s", res.Command.Name), true
	case KindSkill:
		return fmt.Sprintf("skill %q (gym-managed)", res.Spec.Name), true
	}
	return "", false
}

// envEnabled reports whether name is set to a truthy value.
func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

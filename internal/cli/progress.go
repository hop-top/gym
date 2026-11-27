// Package cli — progress.go: helpers wrapping kit/console/progress so
// gym's slow paths emit per-phase events.
//
// kit/cli already attaches a progress.Reporter to cmd.Context() based on
// --quiet / --progress-format / --format flags. RunE handlers fish the
// reporter back out via progress.FromContext (the helpers below). When
// no Cobra context is available (interactive TUI commands, tests) Discard
// is returned so calls are a no-op.
package cli

import (
	"context"
	"time"

	"hop.top/kit/go/console/progress"

	"hop.top/gym/internal/install"
)

// progressReporterFromContext returns the active progress.Reporter on
// ctx, or progress.Discard() when none is attached.
func progressReporterFromContext(ctx context.Context) progress.Reporter {
	return progress.FromContext(ctx)
}

// emitPhase emits a single progress event tagged to phase/item.
// Convenience wrapper that fills in a UTC timestamp.
func emitPhase(ctx context.Context, phase, item string) {
	progressReporterFromContext(ctx).Emit(ctx, progress.Event{
		Phase: phase,
		Item:  install.RedactURL(item),
		At:    time.Now().UTC(),
	})
}

// emitPhaseN emits an event with bytes/total counters. Useful for the
// few places where gym knows real progress (registry refresh, install
// step counts).
func emitPhaseN(ctx context.Context, phase, item string, current, total int64) {
	progressReporterFromContext(ctx).Emit(ctx, progress.Event{
		Phase: phase,
		Item:  install.RedactURL(item),
		Bytes: current,
		Total: total,
		At:    time.Now().UTC(),
	})
}

// emitOK emits a "phase done" event with OK=true (success).
func emitOK(ctx context.Context, phase, item string) {
	t := true
	progressReporterFromContext(ctx).Emit(ctx, progress.Event{
		Phase: phase,
		Item:  install.RedactURL(item),
		OK:    &t,
		At:    time.Now().UTC(),
	})
}

// emitFail emits a "phase done" event with OK=false (failure).
func emitFail(ctx context.Context, phase, item string) {
	f := false
	progressReporterFromContext(ctx).Emit(ctx, progress.Event{
		Phase: phase,
		Item:  install.RedactURL(item),
		OK:    &f,
		At:    time.Now().UTC(),
	})
}

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	kitprogress "hop.top/kit/go/console/progress"
)

// TestEmitPhase_Discard verifies emit helpers are no-ops when no
// reporter is attached to the context. Matches kit semantics: progress
// must never block or panic when nobody's listening.
func TestEmitPhase_Discard(t *testing.T) {
	ctx := context.Background()
	emitPhase(ctx, "load", "registry")
	emitPhaseN(ctx, "install", "skill", 1, 3)
	emitOK(ctx, "save", "registry")
	emitFail(ctx, "save", "registry")
	// no panic, no observable side effects
}

// TestEmitPhase_Human checks Human reporter emits a stderr line per event.
func TestEmitPhase_Human(t *testing.T) {
	var buf bytes.Buffer
	ctx := kitprogress.WithReporter(context.Background(), kitprogress.Human(&buf))

	emitPhase(ctx, "load", "registry")
	emitOK(ctx, "load", "registry")

	out := buf.String()
	if !strings.Contains(out, "[load]") {
		t.Fatalf("expected human reporter to emit phase tag; got %q", out)
	}
	if !strings.Contains(out, "registry") {
		t.Fatalf("expected human reporter to include item; got %q", out)
	}
	if !strings.Contains(out, " ok") {
		t.Fatalf("expected ok marker; got %q", out)
	}
}

// TestEmitPhase_JSONL checks JSONL reporter emits one JSON object per
// event, each on its own line, matching the contract: every progress event is a single JSON
// object on its own line.
func TestEmitPhase_JSONL(t *testing.T) {
	var buf bytes.Buffer
	ctx := kitprogress.WithReporter(context.Background(), kitprogress.JSONL(&buf))

	emitPhase(ctx, "load", "registry")
	emitPhaseN(ctx, "install", "skill", 2, 5)
	emitOK(ctx, "save", "registry")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if got := len(lines); got != 3 {
		t.Fatalf("expected 3 JSONL events, got %d: %q", got, buf.String())
	}
	for i, ln := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("line %d not valid JSON: %v (%q)", i, err, ln)
		}
		if _, ok := ev["phase"].(string); !ok {
			t.Fatalf("line %d missing phase: %v", i, ev)
		}
	}
}

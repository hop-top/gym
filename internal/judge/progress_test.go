package judge_test

import (
	"context"
	"sync"
	"testing"

	"hop.top/kit/go/console/progress"

	"hop.top/gym/internal/judge"
)

type recordingReporter struct {
	mu     sync.Mutex
	events []progress.Event
}

func (r *recordingReporter) Emit(_ context.Context, e progress.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingReporter) byPhase(phase string) []progress.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []progress.Event{}
	for _, e := range r.events {
		if e.Phase == phase {
			out = append(out, e)
		}
	}
	return out
}

// TestRSXResolver_EmitsAnalyzeAndPolicyProgress verifies that Resolve
// emits one start + one completion per parallel rsx call when a
// reporter is attached to ctx via progress.WithReporter.
func TestRSXResolver_EmitsAnalyzeAndPolicyProgress(t *testing.T) {
	canned := []byte(`{"repo":{"owner":"foo","name":"bar","url":"https://github.com/foo/bar"},"scores":{"overall":8},"policy":{"profile":"balanced","passed":true}}`)
	stub := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return canned, nil
	}
	rec := &recordingReporter{}
	ctx := progress.WithReporter(context.Background(), rec)

	r := judge.NewResolver(judge.WithRunner(stub))
	if _, err := r.Resolve(ctx, "github.com/foo/bar"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for _, phase := range []string{"judge-analyze", "judge-policy"} {
		evs := rec.byPhase(phase)
		if len(evs) != 2 {
			t.Errorf("%s event count = %d, want 2 (start + done): %+v", phase, len(evs), evs)
			continue
		}
		if evs[0].OK != nil {
			t.Errorf("%s[0].OK = %v, want nil (start)", phase, *evs[0].OK)
		}
		if evs[1].OK == nil || !*evs[1].OK {
			t.Errorf("%s[1].OK = %v, want &true (done)", phase, evs[1].OK)
		}
		if evs[0].Item != "github.com/foo/bar" {
			t.Errorf("%s[0].Item = %q, want repo", phase, evs[0].Item)
		}
	}
}

// TestRSXResolver_NoReporterStaysSilent verifies the default
// (no reporter on ctx) produces no events and resolve still works.
func TestRSXResolver_NoReporterStaysSilent(t *testing.T) {
	canned := []byte(`{"repo":{"owner":"x","name":"y"},"scores":{"overall":5},"policy":{"passed":true}}`)
	stub := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return canned, nil
	}
	r := judge.NewResolver(judge.WithRunner(stub))
	if _, err := r.Resolve(context.Background(), "x/y"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

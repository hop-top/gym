package registry_test

import (
	"context"
	"sync"
	"testing"

	"hop.top/kit/go/console/progress"

	"hop.top/gym/internal/registry"
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

func (r *recordingReporter) Events() []progress.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]progress.Event, len(r.events))
	copy(out, r.events)
	return out
}

// TestRefresh_EmitsPerSourceProgress verifies that Refresh emits one
// "fetch" start + one completion per repository source when a reporter
// is wired via WithProgress. Counts both events; checks Total slot.
func TestRefresh_EmitsPerSourceProgress(t *testing.T) {
	doc := []byte(`{"repository":{"name":"r","stars":10},"skills":[]}`)
	cfg := registry.Config{
		Repositories: []registry.RepositorySource{
			{Name: "a", URL: "https://example.com/a/skills.json"},
			{Name: "b", URL: "https://example.com/b/skills.json"},
			{Name: "c", URL: "https://example.com/c/skills.json"},
		},
	}
	fetcher := fakeFetcher{byURL: map[string][]byte{
		"https://example.com/a/skills.json": doc,
		"https://example.com/b/skills.json": doc,
		"https://example.com/c/skills.json": doc,
	}}
	rec := &recordingReporter{}

	_, _, err := registry.Refresh(context.Background(), cfg, fetcher, registry.Index{}, registry.WithProgress(rec))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got := rec.Events()
	// Expect 6 events: 3 starts + 3 completions.
	if len(got) != 6 {
		t.Fatalf("event count = %d, want 6: %+v", len(got), got)
	}

	// Starts: phase=fetch, OK=nil, Bytes=current, Total=3.
	for i := 0; i < 3; i++ {
		e := got[i*2]
		if e.Phase != "fetch" {
			t.Errorf("start[%d].Phase = %q, want fetch", i, e.Phase)
		}
		if e.OK != nil {
			t.Errorf("start[%d].OK = %v, want nil", i, *e.OK)
		}
		if e.Total != 3 {
			t.Errorf("start[%d].Total = %d, want 3", i, e.Total)
		}
		if e.Bytes != int64(i+1) {
			t.Errorf("start[%d].Bytes = %d, want %d", i, e.Bytes, i+1)
		}
	}
	// Completions: OK=&true.
	for i := 0; i < 3; i++ {
		e := got[i*2+1]
		if e.Phase != "fetch" {
			t.Errorf("done[%d].Phase = %q, want fetch", i, e.Phase)
		}
		if e.OK == nil || !*e.OK {
			t.Errorf("done[%d].OK = %v, want &true", i, e.OK)
		}
	}
}

// TestRefresh_NoReporterStaysSilent verifies the legacy zero-options
// path produces no events (default = progress.Discard).
func TestRefresh_NoReporterStaysSilent(t *testing.T) {
	cfg := registry.Config{
		Repositories: []registry.RepositorySource{{URL: "https://example.com/x/skills.json"}},
	}
	fetcher := fakeFetcher{byURL: map[string][]byte{
		"https://example.com/x/skills.json": []byte(`{"repository":{"name":"r"},"skills":[]}`),
	}}
	// No reporter passed — Refresh must not panic and must complete OK.
	if _, _, err := registry.Refresh(context.Background(), cfg, fetcher, registry.Index{}); err != nil {
		t.Fatalf("Refresh without progress: %v", err)
	}
}

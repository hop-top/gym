package judge_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/judge"
)

// stubResolver implements judge.Resolver with deterministic behavior.
// Tests dial in score, verdict, error, and call-counting via the
// exported fields; the type lives behind judge.WithResolver so we
// never spawn rsx during go test.
type stubResolver struct {
	score   int
	verdict string
	err     error

	calls atomic.Int32
	delay time.Duration
}

func (s *stubResolver) Resolve(ctx context.Context, repo string) (judge.Judgement, error) {
	s.calls.Add(1)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return judge.Judgement{}, ctx.Err()
		}
	}
	if s.err != nil {
		return judge.Judgement{}, s.err
	}
	return judge.Judgement{
		Repo:    repo,
		Score:   s.score,
		Verdict: s.verdict,
		Signals: []events.JudgeSignal{
			{Type: "github", Key: "stars", Value: "42", Source: "github/repos", Confidence: 1},
		},
	}, nil
}

// newTestBus builds an in-memory bus with empty env so the network
// adapter never tries to connect.
func newTestBus(t *testing.T) *eventbus.Bus {
	t.Helper()
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatalf("eventbus.New: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

// TestJudge_PublishVerdict_OnHireRequested wires the Judge to a
// memory bus, fires a hire_requested, and asserts a
// judgement_completed reply lands with the correct verdict and
// request_id.
func TestJudge_PublishVerdict_OnHireRequested(t *testing.T) {

	cases := []struct {
		name        string
		score       int
		verdict     string
		err         error
		wantVerdict string
		wantScore   int
	}{
		{name: "allow", score: 88, verdict: "allow", wantVerdict: "allow", wantScore: 88},
		{name: "warn", score: 55, verdict: "warn", wantVerdict: "warn", wantScore: 55},
		{name: "deny", score: 12, verdict: "deny", wantVerdict: "deny", wantScore: 12},
		{name: "fail-open-on-error", err: errors.New("rsx blew up"), wantVerdict: "warn"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBus(t)
			cache := judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements"))
			r := &stubResolver{score: tc.score, verdict: tc.verdict, err: tc.err}

			j := judge.New(b, nil,
				judge.WithResolver(r),
				judge.WithCache(cache),
			)
			if err := j.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer j.Stop()

			resp, err := b.RequestResponse(
				context.Background(),
				events.TopicCoachHireRequested,
				string(events.TopicCoachJudgementCompleted),
				events.CoachHireRequestedPayload{
					Repo: "github.com/foo/bar", Scope: "global",
				},
				2*time.Second,
			)
			if err != nil {
				t.Fatalf("RequestResponse: %v", err)
			}

			var got events.CoachJudgementCompletedPayload
			if err := resp.Decode(&got); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Verdict != tc.wantVerdict {
				t.Errorf("Verdict = %q; want %q", got.Verdict, tc.wantVerdict)
			}
			if tc.err == nil && got.Score != tc.wantScore {
				t.Errorf("Score = %d; want %d", got.Score, tc.wantScore)
			}
			if got.RequestID != resp.RequestID {
				t.Errorf("RequestID mismatch: got %q want %q", got.RequestID, resp.RequestID)
			}
		})
	}
}

// TestJudge_CacheHitSkipsResolver verifies a cached judgement is
// served without invoking rsx a second time.
func TestJudge_CacheHitSkipsResolver(t *testing.T) {
	b := newTestBus(t)
	cacheDir := filepath.Join(t.TempDir(), "judgements")
	cache := judge.NewCacheAt(cacheDir)
	r := &stubResolver{score: 90, verdict: "allow"}

	j := judge.New(b, nil, judge.WithResolver(r), judge.WithCache(cache))
	if err := j.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer j.Stop()

	hire := func() {
		_, err := b.RequestResponse(
			context.Background(),
			events.TopicCoachHireRequested,
			string(events.TopicCoachJudgementCompleted),
			events.CoachHireRequestedPayload{Repo: "github.com/foo/bar", Scope: "global"},
			2*time.Second,
		)
		if err != nil {
			t.Fatalf("RequestResponse: %v", err)
		}
	}
	hire()
	hire()

	if got := r.calls.Load(); got != 1 {
		t.Errorf("resolver called %d times; want 1 (second was cache hit)", got)
	}
}

// TestJudge_RefreshBypassesCache asserts WithRefreshFunc(true) skips
// the cache and re-runs the resolver.
func TestJudge_RefreshBypassesCache(t *testing.T) {
	b := newTestBus(t)
	cache := judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements"))
	r := &stubResolver{score: 90, verdict: "allow"}

	refresh := atomic.Bool{}
	j := judge.New(b, nil,
		judge.WithResolver(r),
		judge.WithCache(cache),
		judge.WithRefreshFunc(refresh.Load),
	)
	if err := j.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer j.Stop()

	hire := func() {
		_, err := b.RequestResponse(
			context.Background(),
			events.TopicCoachHireRequested,
			string(events.TopicCoachJudgementCompleted),
			events.CoachHireRequestedPayload{Repo: "github.com/foo/bar", Scope: "global"},
			2*time.Second,
		)
		if err != nil {
			t.Fatalf("RequestResponse: %v", err)
		}
	}
	hire()
	refresh.Store(true)
	hire()
	if got := r.calls.Load(); got != 2 {
		t.Errorf("resolver called %d times; want 2 (refresh bypasses cache)", got)
	}
}

// TestJudge_DuplicateRequestIDIsIdempotent fires the same payload
// twice with a fixed request_id and confirms the resolver runs once.
func TestJudge_DuplicateRequestIDIsIdempotent(t *testing.T) {
	b := newTestBus(t)
	r := &stubResolver{score: 80, verdict: "allow", delay: 50 * time.Millisecond}
	j := judge.New(b, nil,
		judge.WithResolver(r),
		judge.WithCache(judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements"))),
	)
	if err := j.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer j.Stop()

	const fixedID = "01HZ-fixed"
	got := make(chan events.CoachJudgementCompletedPayload, 4)
	b.Subscribe(string(events.TopicCoachJudgementCompleted), func(_ context.Context, e bus.Event) error {
		raw, _ := json.Marshal(e.Payload)
		var p events.CoachJudgementCompletedPayload
		_ = json.Unmarshal(raw, &p)
		got <- p
		return nil
	})

	// Two parallel publishes carrying the same request_id.
	for range 2 {
		go func() {
			_ = b.Publish(context.Background(), events.TopicCoachHireRequested,
				events.CoachHireRequestedPayload{
					Repo: "github.com/foo/bar", Scope: "global", RequestID: fixedID,
				})
		}()
	}

	// Drain a couple of replies and assert they're consistent.
	deadline := time.After(2 * time.Second)
	seen := 0
	for seen < 2 {
		select {
		case p := <-got:
			if p.RequestID != fixedID {
				t.Errorf("RequestID = %q; want %q", p.RequestID, fixedID)
			}
			seen++
		case <-deadline:
			t.Fatalf("only saw %d replies", seen)
		}
	}
	if got := r.calls.Load(); got != 1 {
		t.Errorf("resolver called %d times; want 1 (dedup on request_id)", got)
	}
}

// TestJudge_StartIdempotent calls Start twice and ensures only one
// active subscription remains (resolver fires once per publish).
func TestJudge_StartIdempotent(t *testing.T) {
	b := newTestBus(t)
	r := &stubResolver{score: 80, verdict: "allow"}
	j := judge.New(b, nil,
		judge.WithResolver(r),
		judge.WithCache(judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements"))),
	)
	if err := j.Start(context.Background()); err != nil {
		t.Fatalf("Start1: %v", err)
	}
	if err := j.Start(context.Background()); err != nil {
		t.Fatalf("Start2: %v", err)
	}
	defer j.Stop()

	if _, err := b.RequestResponse(
		context.Background(),
		events.TopicCoachHireRequested,
		string(events.TopicCoachJudgementCompleted),
		events.CoachHireRequestedPayload{Repo: "github.com/foo/bar", Scope: "global"},
		2*time.Second,
	); err != nil {
		t.Fatalf("RequestResponse: %v", err)
	}
	if got := r.calls.Load(); got != 1 {
		t.Errorf("resolver called %d times; want 1", got)
	}
}

// TestCache_RoundTrip writes, reads, and overwrites a cache entry.
func TestCache_RoundTrip(t *testing.T) {
	c := judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements"))
	url := "github.com/foo/bar"

	if _, ok, err := c.Get(url); ok || err != nil {
		t.Fatalf("Get on empty cache: ok=%v err=%v; want false,nil", ok, err)
	}

	want := judge.Judgement{Repo: url, Score: 77, Verdict: "warn"}
	if err := c.Put(url, want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok, err := c.Get(url)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Score != want.Score || got.Verdict != want.Verdict {
		t.Errorf("got %+v; want %+v", got, want)
	}
	if got.CachedAt.IsZero() {
		t.Error("CachedAt zero; want set on Put")
	}
}

// TestCache_Stale demonstrates that maxAge=1ns reports a fresh write
// as stale immediately.
func TestCache_Stale(t *testing.T) {
	c := judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements")).WithMaxAge(time.Nanosecond)
	url := "github.com/foo/stale"
	if err := c.Put(url, judge.Judgement{Repo: url, Verdict: "allow"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, ok, err := c.Get(url); ok || err != nil {
		t.Errorf("Get stale: ok=%v err=%v; want false,nil", ok, err)
	}
}

// TestCache_KeyCanonicalisation proves equivalent URL spellings hash
// to the same on-disk file.
func TestCache_KeyCanonicalisation(t *testing.T) {
	c := judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements"))

	a := c.Path("github.com/foo/bar")
	b := c.Path("https://github.com/foo/bar")
	d := c.Path("git@github.com:foo/bar.git")
	if a != b || a != d {
		t.Errorf("paths diverge:\n %q\n %q\n %q", a, b, d)
	}
}

// TestRSXResolver_StubbedRunner exercises the production rsxResolver
// shape (Resolve returning a Judgement with our verdict roll-up
// rules) by stubbing the CommandRunner with canned rsx JSON. The
// real subprocess never runs.
func TestRSXResolver_StubbedRunner(t *testing.T) {
	stub := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		// First arg distinguishes analyze vs policy test.
		if len(args) > 0 && args[0] == "analyze" {
			return []byte(`{
				"repo": {"owner":"foo","name":"bar","url":"https://github.com/foo/bar"},
				"scores": {"overall": 8.4},
				"policy": {"profile":"balanced","passed":true,"exit_code":0},
				"evidence": [{"type":"github","key":"stars","value":"42","source":"github/repos","confidence":1.0}]
			}`), nil
		}
		return []byte(`{
			"repo": {"owner":"foo","name":"bar","url":"https://github.com/foo/bar"},
			"scores": {"overall": 8.4},
			"policy": {"profile":"balanced","passed":true,"exit_code":0},
			"summary": {"recommendation":"Looks good"}
		}`), nil
	}

	r := judge.NewResolver(judge.WithRunner(stub))
	jud, err := r.Resolve(context.Background(), "github.com/foo/bar")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if jud.Score != 84 {
		t.Errorf("Score = %d; want 84", jud.Score)
	}
	if jud.Verdict != "allow" {
		t.Errorf("Verdict = %q; want allow", jud.Verdict)
	}
	if len(jud.Signals) != 1 || jud.Signals[0].Key != "stars" {
		t.Errorf("Signals = %#v; want one signal keyed stars", jud.Signals)
	}
}

// TestRSXResolver_PolicyDeny asserts a failed policy run lands a
// "deny" verdict with a finding describing the recommendation.
func TestRSXResolver_PolicyDeny(t *testing.T) {
	stub := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "analyze" {
			return []byte(`{
				"scores": {"overall": 4.1},
				"policy": {"profile":"balanced","passed":true,"exit_code":0}
			}`), nil
		}
		return []byte(`{
			"scores": {"overall": 4.1},
			"policy": {"profile":"balanced","passed":false,"exit_code":1},
			"summary": {"recommendation":"High-risk candidate. Prefer alternatives unless essential."}
		}`), nil
	}
	r := judge.NewResolver(judge.WithRunner(stub))
	jud, err := r.Resolve(context.Background(), "github.com/foo/bar")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if jud.Verdict != "deny" {
		t.Errorf("Verdict = %q; want deny", jud.Verdict)
	}
	if len(jud.Findings) == 0 || !strings.Contains(jud.Findings[0].Message, "High-risk") {
		t.Errorf("Findings = %#v; want one with 'High-risk' message", jud.Findings)
	}
}

// TestRSXResolver_FailOpenOnRunnerError asserts both subprocess
// failures bubble up to the caller (the Judge subscriber decides
// whether to fail-open at the publish layer).
func TestRSXResolver_FailOpenOnRunnerError(t *testing.T) {
	stub := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, errors.New("rsx not on PATH")
	}
	r := judge.NewResolver(judge.WithRunner(stub))
	_, err := r.Resolve(context.Background(), "github.com/foo/bar")
	if err == nil {
		t.Fatal("Resolve: nil err; want subprocess error to bubble up")
	}
}

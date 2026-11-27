package judge_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"hop.top/gym/internal/events"
	"hop.top/gym/internal/judge"
)

// D10 (judge): under a non-open network mode the rsx subprocess is not
// spawned; the hire proceeds on a warn judgement that says why. A cached
// judgement is still served, and an open gate runs rsx as before.
func TestD10JudgeSkipsRsxWhenNetworkNotOpen(t *testing.T) {
	b := newTestBus(t)
	r := &stubResolver{score: 90, verdict: "allow"}
	cache := judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements"))
	gate := errors.New("network.mode is allowlist")
	j := judge.New(b, nil, judge.WithResolver(r), judge.WithCache(cache),
		judge.WithNetworkGate(func() error { return gate }))
	if err := j.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer j.Stop()

	resp, err := b.RequestResponse(context.Background(), events.TopicCoachHireRequested,
		string(events.TopicCoachJudgementCompleted),
		events.CoachHireRequestedPayload{Repo: "github.com/foo/bar", Scope: "global"}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var got events.CoachJudgementCompletedPayload
	if err := resp.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if r.calls.Load() != 0 {
		t.Fatalf("rsx resolver ran %d times under a closed network", r.calls.Load())
	}
	if got.Verdict != "warn" || len(got.Findings) != 1 || got.Findings[0].Rule != "judge-skipped" {
		t.Fatalf("judgement = %+v", got)
	}
	// nothing was cached for the skipped run
	if _, ok, _ := cache.Get("github.com/foo/bar"); ok {
		t.Fatal("a skipped judgement was cached")
	}
}

func TestD10JudgeRunsRsxWhenGateIsOpen(t *testing.T) {
	b := newTestBus(t)
	r := &stubResolver{score: 90, verdict: "allow"}
	j := judge.New(b, nil, judge.WithResolver(r),
		judge.WithCache(judge.NewCacheAt(filepath.Join(t.TempDir(), "judgements"))),
		judge.WithNetworkGate(func() error { return nil }))
	if err := j.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer j.Stop()
	if _, err := b.RequestResponse(context.Background(), events.TopicCoachHireRequested,
		string(events.TopicCoachJudgementCompleted),
		events.CoachHireRequestedPayload{Repo: "github.com/foo/bar", Scope: "global"}, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if r.calls.Load() != 1 {
		t.Fatalf("calls = %d", r.calls.Load())
	}
}

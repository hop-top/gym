package eventbus_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
)

// TestBusNew_DefaultsToMemory asserts the zero-config bus is in-memory
// and carries Source = "gym" plus a non-empty origin id.
func TestBusNew_DefaultsToMemory(t *testing.T) {
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")

	b, err := eventbus.New(eventbus.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close(context.Background())

	if got := b.Source(); got != eventbus.Source {
		t.Errorf("Source() = %q; want %q", got, eventbus.Source)
	}
	if b.OriginID() == "" {
		t.Error("OriginID() empty")
	}
	if b.AdapterKind() != eventbus.AdapterMemory {
		t.Errorf("AdapterKind() = %q; want %q", b.AdapterKind(), eventbus.AdapterMemory)
	}
}

// TestResolve_PicksAdapterFromEnv proves env-only callers (e.g. gym
// serve --daemon) get the right adapter without an explicit flag.
func TestResolve_PicksAdapterFromEnv(t *testing.T) {
	cases := []struct {
		name    string
		busURL  string
		busPath string
		want    eventbus.Adapter
	}{
		{name: "default-memory", want: eventbus.AdapterMemory},
		{name: "sqlite-via-path", busPath: "/tmp/gym-test.sqlite", want: eventbus.AdapterSQLite},
		{name: "network-via-url", busURL: "ws://localhost:9999/bus", want: eventbus.AdapterNetwork},
		{
			name:    "url-wins-over-path",
			busPath: "/tmp/gym-test.sqlite",
			busURL:  "ws://localhost:9999/bus",
			want:    eventbus.AdapterNetwork,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GYM_BUS_URL", tc.busURL)
			t.Setenv("GYM_BUS_PATH", tc.busPath)
			got := eventbus.Resolve(eventbus.Config{}).Adapter
			if got != tc.want {
				t.Errorf("Adapter = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestPublish_AllGymTopics fans out one publish per gym.* topic and
// asserts a wildcard subscriber on "gym.#" sees every one.
func TestPublish_AllGymTopics(t *testing.T) {
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")

	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close(context.Background())

	want := []bus.Topic{
		events.TopicCoachHireRequested,
		events.TopicCoachHired,
		events.TopicCoachDismissed,
		events.TopicSkillInstallRequested,
		events.TopicSkillInstallCompleted,
		events.TopicSkillInstallRejected,
		events.TopicSkillUpgradePlanned,
		events.TopicSkillUpgradeApplied,
		events.TopicSkillUpgradeFailed,
		events.TopicWarmupMetadataEmitted,
		events.TopicToolspecCLIPublished,
	}

	var (
		mu   sync.Mutex
		seen = make(map[bus.Topic]int)
	)
	unsub := b.Subscribe("gym.#", func(_ context.Context, e bus.Event) error {
		mu.Lock()
		seen[e.Topic]++
		mu.Unlock()
		return nil
	})
	defer unsub()

	ctx := context.Background()
	for _, tp := range want {
		if err := b.Publish(ctx, tp, map[string]any{"k": "v"}); err != nil {
			t.Fatalf("Publish(%q): %v", tp, err)
		}
	}

	for _, tp := range want {
		mu.Lock()
		n := seen[tp]
		mu.Unlock()
		if n != 1 {
			t.Errorf("topic %q delivered %d times; want 1", tp, n)
		}
	}
}

// TestPublish_SetsSource asserts every published event carries
// Source = "gym".
func TestPublish_SetsSource(t *testing.T) {
	b, err := eventbus.New(eventbus.Config{
		Adapter: eventbus.AdapterMemory,
		Source:  "gym",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close(context.Background())

	got := make(chan bus.Event, 1)
	b.Subscribe("gym.#", func(_ context.Context, e bus.Event) error {
		got <- e
		return nil
	})

	if err := b.Publish(context.Background(), events.TopicWarmupMetadataEmitted,
		events.WarmupMetadataEmittedPayload{Format: "xml", Scope: "all"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case e := <-got:
		if e.Source != "gym" {
			t.Errorf("Source = %q; want gym", e.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered within 1s")
	}
}

// TestRequestResponse_RoundTrip wires a fake responder that mirrors
// rsx: subscribes to TopicCoachHireRequested, replies on
// TopicRSXRepoPolicyEvaluated with the same request_id. The caller's
// RequestResponse helper unblocks before timeout and returns the
// matched event.
func TestRequestResponse_RoundTrip(t *testing.T) {
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close(context.Background())

	// Fake rsx subscriber.
	b.Subscribe(string(events.TopicCoachHireRequested), func(ctx context.Context, e bus.Event) error {
		raw, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		var req events.CoachHireRequestedPayload
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
		// Reply on a separate goroutine so we don't recurse under
		// the publish lock.
		go func() {
			_ = b.Publish(ctx, events.TopicRSXRepoPolicyEvaluated,
				events.RSXRepoPolicyEvaluatedPayload{
					Repo: req.Repo, Score: 88, Verdict: "allow",
					RequestID: req.RequestID,
				})
		}()
		return nil
	})

	resp, err := b.RequestResponse(
		context.Background(),
		events.TopicCoachHireRequested,
		string(events.TopicRSXRepoPolicyEvaluated),
		events.CoachHireRequestedPayload{
			Repo: "github.com/foo/bar", Scope: "global", Curated: true,
		},
		2*time.Second,
	)
	if err != nil {
		t.Fatalf("RequestResponse: %v", err)
	}
	if resp.RequestID == "" {
		t.Error("RequestID empty")
	}

	var got events.RSXRepoPolicyEvaluatedPayload
	if err := resp.Decode(&got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.RequestID != resp.RequestID {
		t.Errorf("RequestID mismatch: got %q want %q", got.RequestID, resp.RequestID)
	}
	if got.Verdict != "allow" {
		t.Errorf("Verdict = %q; want allow", got.Verdict)
	}
	if got.Score != 88 {
		t.Errorf("Score = %d; want 88", got.Score)
	}
}

// TestRequestResponse_FiltersByRequestID asserts a response carrying a
// different request_id is ignored — RequestResponse should still time
// out instead of returning the wrong reply.
func TestRequestResponse_FiltersByRequestID(t *testing.T) {
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close(context.Background())

	b.Subscribe(string(events.TopicSkillInstallRequested), func(ctx context.Context, _ bus.Event) error {
		go func() {
			_ = b.Publish(ctx, events.TopicPolicyGuardInstallEvaluated,
				events.PolicyGuardInstallEvaluatedPayload{
					Skill: "lint", Verdict: "allow", RequestID: "wrong-id",
				})
		}()
		return nil
	})

	_, err = b.RequestResponse(
		context.Background(),
		events.TopicSkillInstallRequested,
		string(events.TopicPolicyGuardInstallEvaluated),
		events.SkillInstallRequestedPayload{Skill: "lint", Scope: "global"},
		300*time.Millisecond,
	)
	if !errors.Is(err, eventbus.ErrTimeout) {
		t.Fatalf("err = %v; want ErrTimeout", err)
	}
}

// TestRequestResponse_TimeoutNoSubscriber proves the helper times out
// when nothing is listening.
func TestRequestResponse_TimeoutNoSubscriber(t *testing.T) {
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close(context.Background())

	start := time.Now()
	_, err = b.RequestResponse(
		context.Background(),
		events.TopicSkillInstallRequested,
		string(events.TopicPolicyGuardInstallEvaluated),
		events.SkillInstallRequestedPayload{Skill: "lint", Scope: "global"},
		200*time.Millisecond,
	)
	elapsed := time.Since(start)
	if !errors.Is(err, eventbus.ErrTimeout) {
		t.Fatalf("err = %v; want ErrTimeout", err)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("returned too fast: %s", elapsed)
	}
}

// TestRequestResponse_ReusesRequestID asserts a payload that already
// carries a request_id keeps its identifier rather than getting a
// freshly generated one stamped on.
func TestRequestResponse_ReusesRequestID(t *testing.T) {
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close(context.Background())

	const wantID = "01HZ-test-fixed"

	got := make(chan string, 1)
	b.Subscribe(string(events.TopicCoachHireRequested), func(ctx context.Context, e bus.Event) error {
		raw, _ := json.Marshal(e.Payload)
		var req events.CoachHireRequestedPayload
		_ = json.Unmarshal(raw, &req)
		got <- req.RequestID
		go func() {
			_ = b.Publish(ctx, events.TopicRSXRepoPolicyEvaluated,
				events.RSXRepoPolicyEvaluatedPayload{
					Repo: req.Repo, Verdict: "allow", RequestID: req.RequestID,
				})
		}()
		return nil
	})

	resp, err := b.RequestResponse(
		context.Background(),
		events.TopicCoachHireRequested,
		string(events.TopicRSXRepoPolicyEvaluated),
		events.CoachHireRequestedPayload{
			Repo: "github.com/foo/bar", Scope: "global", RequestID: wantID,
		},
		2*time.Second,
	)
	if err != nil {
		t.Fatalf("RequestResponse: %v", err)
	}
	if resp.RequestID != wantID {
		t.Errorf("response RequestID = %q; want %q", resp.RequestID, wantID)
	}
	select {
	case observed := <-got:
		if observed != wantID {
			t.Errorf("subscriber saw RequestID = %q; want %q", observed, wantID)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber never fired")
	}
}

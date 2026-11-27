package judge

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	charmlog "charm.land/log/v2"
	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/events"
)

// Judge subscribes to [events.TopicCoachHireRequested] and answers
// with [events.TopicCoachJudgementCompleted] after consulting the
// cache or invoking the rsx subprocess driver.
//
// The zero value is not usable; construct one via [New].
type Judge struct {
	bus      gymBus // limited surface used here
	logger   *charmlog.Logger
	resolver Resolver
	cache    *Cache

	// refresh, when true, forces a re-run of rsx even on cache hits.
	// Bound to the persistent --refresh-judgement flag at boot.
	refresh func() bool

	// netGate, when set, is consulted before rsx is spawned; an error
	// skips the subprocess (rsx reads from public hosts).
	netGate func() error

	// dedup is a request_id -> chan to ensure idempotency: a second
	// publish carrying the same request_id reuses the first
	// judgement instead of running rsx twice.
	mu      sync.Mutex
	pending map[string]chan struct{}
	results map[string]Judgement

	unsub bus.Unsubscribe
}

// gymBus is the subset of *eventbus.Bus this package needs. Captured
// as an interface to keep the test harness honest (and to dodge the
// import cycle with eventbus's [Bus] type).
type gymBus interface {
	Publish(ctx context.Context, topic bus.Topic, payload any) error
	Subscribe(pattern string, h bus.Handler) bus.Unsubscribe
}

// New constructs a Judge. The bus and logger are required; everything
// else gets a sensible default. The caller should invoke [Judge.Start]
// once the bus is ready.
func New(b gymBus, logger *charmlog.Logger, opts ...Option) *Judge {
	j := &Judge{
		bus:     b,
		logger:  logger,
		pending: make(map[string]chan struct{}),
		results: make(map[string]Judgement),
		refresh: func() bool { return false },
	}
	for _, opt := range opts {
		opt.applyJudge(j)
	}
	if j.resolver == nil {
		// Build a default resolver passing through any options that
		// also targeted resolver fields.
		rOpts := make([]Option, 0, len(opts))
		rOpts = append(rOpts, opts...)
		j.resolver = NewResolver(rOpts...)
	}
	if j.cache == nil {
		c, err := NewCache()
		if err == nil {
			j.cache = c
		}
	}
	return j
}

// WithResolver overrides the default subprocess Resolver. Tests use
// this to inject a deterministic [Resolver] without spawning rsx.
func WithResolver(r Resolver) Option {
	return funcOption{judge: func(j *Judge) {
		if r != nil {
			j.resolver = r
		}
	}}
}

// WithCache overrides the default file-backed cache.
func WithCache(c *Cache) Option {
	return funcOption{judge: func(j *Judge) { j.cache = c }}
}

// WithNetworkGate makes the judge skip the rsx subprocess, with a warning,
// whenever fn returns an error. Cached judgements are still served.
func WithNetworkGate(fn func() error) Option {
	return funcOption{judge: func(j *Judge) { j.netGate = fn }}
}

// WithRefreshFunc binds a callable that reports whether the cache
// should be bypassed for the current request. Default: never refresh.
// Wired by cobra.go to the persistent --refresh-judgement flag so the
// judge respects the user's intent without coupling to viper.
func WithRefreshFunc(fn func() bool) Option {
	return funcOption{judge: func(j *Judge) {
		if fn != nil {
			j.refresh = fn
		}
	}}
}

// Start subscribes to TopicCoachHireRequested. Returns an error if
// the bus is nil. Idempotent: a second Start replaces the prior
// subscription.
func (j *Judge) Start(_ context.Context) error {
	if j == nil || j.bus == nil {
		return errors.New("judge: nil bus")
	}
	if j.unsub != nil {
		j.unsub()
		j.unsub = nil
	}
	j.unsub = j.bus.Subscribe(string(events.TopicCoachHireRequested),
		func(ctx context.Context, e bus.Event) error {
			j.handle(ctx, e)
			// We never veto; rsx failures fail-open via the bus reply.
			return nil
		})
	return nil
}

// Stop unsubscribes the handler. Safe to call before Start, or twice.
func (j *Judge) Stop() {
	if j == nil {
		return
	}
	if j.unsub != nil {
		j.unsub()
		j.unsub = nil
	}
}

// handle is the subscription callback. It is run synchronously on the
// publisher's goroutine; the actual rsx work and the reply are
// dispatched on a fresh goroutine so we never block kit/bus's
// publish under our own lock.
func (j *Judge) handle(ctx context.Context, e bus.Event) {
	raw, err := json.Marshal(e.Payload)
	if err != nil {
		j.warnf("payload marshal: %v", err)
		return
	}
	var req events.CoachHireRequestedPayload
	if err := json.Unmarshal(raw, &req); err != nil {
		j.warnf("payload unmarshal: %v", err)
		return
	}
	if req.RequestID == "" {
		j.warnf("dropping hire_requested without request_id")
		return
	}

	// Idempotency: short-circuit duplicate request_ids.
	j.mu.Lock()
	if cached, ok := j.results[req.RequestID]; ok {
		j.mu.Unlock()
		j.publishVerdict(ctx, req, cached)
		return
	}
	if waitCh, inflight := j.pending[req.RequestID]; inflight {
		j.mu.Unlock()
		go func() {
			<-waitCh
			j.mu.Lock()
			cached, ok := j.results[req.RequestID]
			j.mu.Unlock()
			if ok {
				j.publishVerdict(ctx, req, cached)
			}
		}()
		return
	}
	done := make(chan struct{})
	j.pending[req.RequestID] = done
	j.mu.Unlock()

	go func() {
		defer func() {
			j.mu.Lock()
			delete(j.pending, req.RequestID)
			j.mu.Unlock()
			close(done)
		}()
		jud := j.resolveCached(ctx, req.Repo)
		j.mu.Lock()
		j.results[req.RequestID] = jud
		j.mu.Unlock()
		j.publishVerdict(ctx, req, jud)
	}()
}

// resolveCached consults the cache (unless refresh is requested),
// falls back to the rsx Resolver, and writes successful results back
// to the cache.
func (j *Judge) resolveCached(ctx context.Context, repo string) Judgement {
	if !j.refresh() && j.cache != nil {
		if cached, ok, err := j.cache.Get(repo); ok && err == nil {
			return cached
		}
	}
	if j.netGate != nil {
		if gerr := j.netGate(); gerr != nil {
			j.warnf("skipping rsx for %s: %v", repo, gerr)
			return Judgement{
				Repo:    repo,
				Verdict: "warn",
				Findings: []events.PolicyFinding{{
					Rule:     "judge-skipped",
					Severity: "warn",
					Message:  "rsx not run: " + gerr.Error(),
				}},
			}
		}
	}
	jud, err := j.resolver.Resolve(ctx, repo)
	if err != nil {
		j.warnf("rsx resolve %s: %v", repo, err)
		// Fail-open: emit a warn-verdict judgement so coach hire can
		// proceed unless --strict / --insecure flips the call site.
		return Judgement{
			Repo:    repo,
			Score:   0,
			Verdict: "warn",
			Findings: []events.PolicyFinding{{
				Rule:     "rsx-unavailable",
				Severity: "warn",
				Message:  err.Error(),
			}},
		}
	}
	if j.cache != nil {
		if err := j.cache.Put(repo, jud); err != nil {
			j.warnf("cache put %s: %v", repo, err)
		}
	}
	return jud
}

func (j *Judge) publishVerdict(ctx context.Context, req events.CoachHireRequestedPayload, jud Judgement) {
	payload := events.CoachJudgementCompletedPayload{
		Repo:      req.Repo,
		Score:     jud.Score,
		Verdict:   jud.Verdict,
		Findings:  jud.Findings,
		Signals:   jud.Signals,
		RequestID: req.RequestID,
	}
	if !jud.CachedAt.IsZero() {
		payload.CachedAt = jud.CachedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if err := j.bus.Publish(ctx, events.TopicCoachJudgementCompleted, payload); err != nil {
		j.warnf("publish judgement: %v", err)
	}
}

func (j *Judge) warnf(format string, args ...any) {
	if j.logger == nil {
		return
	}
	j.logger.Warnf("judge: "+format, args...)
}

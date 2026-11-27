package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	charmlog "charm.land/log/v2"
	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
)

// Guard is the long-lived subscriber. One Guard owns one Bus
// subscription pair (install + upgrade). Stop drains the
// subscriptions cleanly.
type Guard struct {
	bus    *eventbus.Bus
	engine *Engine
	logger *charmlog.Logger

	// dedup tracks request_ids we've already evaluated so subsequent
	// deliveries for the same request return the cached verdict.
	// Subscribers MUST be idempotent on request_id (kit/bus contract).
	mu     sync.Mutex
	dedup  map[string]cachedVerdict
	unsubs []bus.Unsubscribe
}

type cachedVerdict struct {
	Verdict  Verdict
	Findings []Finding
}

// Option mutates Guard at construction.
type Option func(*Guard)

// WithLogger installs a charm logger for findings/timeouts.
func WithLogger(l *charmlog.Logger) Option {
	return func(g *Guard) { g.logger = l }
}

// WithEngine installs a precompiled engine. When unset, New compiles
// the built-in policies via Load with no override directories — fine
// for tests, callers wiring overrides should set it explicitly.
func WithEngine(e *Engine) Option {
	return func(g *Guard) { g.engine = e }
}

// New constructs a Guard around bus. The Engine must be supplied via
// WithEngine; otherwise New builds one from the embedded built-in
// policies only (no user/project overrides).
func New(b *eventbus.Bus, opts ...Option) (*Guard, error) {
	if b == nil {
		return nil, errors.New("guard: nil bus")
	}
	g := &Guard{bus: b, dedup: map[string]cachedVerdict{}}
	for _, opt := range opts {
		opt(g)
	}
	if g.engine == nil {
		policies, err := Load(LoadOptions{})
		if err != nil {
			return nil, err
		}
		eng, err := NewEngine(policies)
		if err != nil {
			return nil, err
		}
		g.engine = eng
	}
	return g, nil
}

// Engine returns the underlying engine. Useful for `gym guard list`
// and `gym guard test`.
func (g *Guard) Engine() *Engine { return g.engine }

// Start subscribes to the install/upgrade request topics. Returns
// immediately; subscriber callbacks run in the bus's goroutine pool.
// Caller MUST invoke Stop to release subscriptions. Safe to call from
// PersistentPreRunE.
func (g *Guard) Start(ctx context.Context) error {
	if g == nil || g.bus == nil {
		return errors.New("guard: nil guard")
	}
	g.unsubs = append(g.unsubs,
		g.bus.Subscribe(string(events.TopicSkillInstallRequested), g.handleInstall),
		g.bus.Subscribe(string(events.TopicSkillUpgradePlanned), g.handleUpgrade),
	)
	_ = ctx
	return nil
}

// Stop unsubscribes the bus handlers. Safe to call multiple times.
func (g *Guard) Stop() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, u := range g.unsubs {
		if u != nil {
			u()
		}
	}
	g.unsubs = nil
}

// handleInstall evaluates an install request and publishes the verdict.
func (g *Guard) handleInstall(ctx context.Context, e bus.Event) error {
	var p events.SkillInstallRequestedPayload
	if err := remarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("guard: install payload: %w", err)
	}
	if p.RequestID == "" {
		// kit/bus has no dedupe; unkeyed events are best-effort.
		return nil
	}
	verdict, findings := g.evaluateOrCache(p.RequestID, payloadFromInstall(p))
	return g.publishVerdict(ctx, p.Skill, p.RequestID, verdict, findings)
}

// handleUpgrade evaluates an upgrade plan. Each plan item is fed into
// the activation map under payload.items[i]; the verdict is the worst
// across all items so a single bad skill blocks the batch.
func (g *Guard) handleUpgrade(ctx context.Context, e bus.Event) error {
	var p events.SkillUpgradePlannedPayload
	if err := remarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("guard: upgrade payload: %w", err)
	}
	if p.RequestID == "" {
		return nil
	}
	verdict, findings := g.evaluateOrCache(p.RequestID, payloadFromUpgrade(p))
	skill := ""
	if len(p.Items) == 1 {
		skill = p.Items[0].Skill
	}
	return g.publishVerdict(ctx, skill, p.RequestID, verdict, findings)
}

func (g *Guard) evaluateOrCache(reqID string, activation map[string]any) (Verdict, []Finding) {
	g.mu.Lock()
	if c, ok := g.dedup[reqID]; ok {
		g.mu.Unlock()
		return c.Verdict, c.Findings
	}
	g.mu.Unlock()

	verdict, findings := g.engine.Evaluate(activation)

	g.mu.Lock()
	g.dedup[reqID] = cachedVerdict{Verdict: verdict, Findings: findings}
	g.mu.Unlock()
	return verdict, findings
}

func (g *Guard) publishVerdict(ctx context.Context, skill, reqID string, verdict Verdict, findings []Finding) error {
	pubFindings := make([]events.PolicyFinding, 0, len(findings))
	for _, f := range findings {
		pubFindings = append(pubFindings, events.PolicyFinding{
			Rule:     f.Rule,
			Severity: f.Severity,
			Message:  f.Message,
		})
	}
	return g.bus.Publish(ctx, events.TopicPolicyGuardInstallEvaluated,
		events.PolicyGuardInstallEvaluatedPayload{
			Skill:     skill,
			Verdict:   string(verdict),
			Findings:  pubFindings,
			RequestID: reqID,
		})
}

// payloadFromInstall builds the CEL activation map for an install
// request. Beyond the core install fields, the optional staging-dir scan
// results (postinstall, shell_hook, scan, largest_binary_bytes) are
// copied into the payload map; they are zero values when the publisher
// did not scan. The "scan" field carries whatever scan text the
// publisher attached. allow_postinstall and large_binary stay in the
// map, always false, so policy expressions referencing them keep
// evaluating; an allowance never comes from the package under
// evaluation.
func payloadFromInstall(p events.SkillInstallRequestedPayload) map[string]any {
	resource := map[string]any{
		"skill":      p.Skill,
		"version":    p.Version,
		"source_url": p.SourceURL,
		"scope":      p.Scope,
	}
	context := map[string]any{
		"scope":      p.Scope,
		"source_url": p.SourceURL,
		"request_id": p.RequestID,
	}
	if len(p.Paths) > 0 {
		context["paths"] = stringsToAny(p.Paths)
	}
	payload := map[string]any{
		"skill":      p.Skill,
		"version":    p.Version,
		"source_url": p.SourceURL,
		"scope":      p.Scope,
		"request_id": p.RequestID,
		// Defaults so policies can call .matches/.exists without has() guards.
		"postinstall":          p.Postinstall,
		"shell_hook":           p.ShellHook,
		"scan":                 p.Scan,
		"largest_binary_bytes": p.LargestBinaryBytes,
		"allow_postinstall":    false,
		"large_binary":         false,
		// Admission facts. Defaults for every key so policies can read
		// them without has() guards.
		"manifest_present":     p.ManifestPresent,
		"manifest_mode":        p.ManifestMode,
		"digest":               p.Digest,
		"digest_status":        p.DigestStatus,
		"digest_source":        p.DigestSource,
		"declared_tools":       stringsToAny(p.DeclaredTools),
		"undeclared_tools":     stringsToAny(p.UndeclaredTools),
		"data_class":           p.DataClass,
		"max_data_class":       p.MaxDataClass,
		"data_class_over":      p.DataClassOver,
		"data_class_escalated": p.DataClassEscalated,
		"approval_state":       p.ApprovalState,
	}
	if len(p.Paths) > 0 {
		payload["paths"] = stringsToAny(p.Paths)
	}
	return map[string]any{
		"principal": map[string]any{},
		"resource":  resource,
		"context":   context,
		"payload":   payload,
	}
}

// InstallActivation builds the CEL activation for an install request.
// Exported so `gym guard test <dir>` evaluates exactly what the install
// gate publishes.
func InstallActivation(p events.SkillInstallRequestedPayload) map[string]any {
	return payloadFromInstall(p)
}

func payloadFromUpgrade(p events.SkillUpgradePlannedPayload) map[string]any {
	items := make([]any, 0, len(p.Items))
	skills := make([]any, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, map[string]any{
			"skill":   it.Skill,
			"scope":   it.Scope,
			"from":    it.From,
			"to":      it.To,
			"source":  it.Source,
			"curated": it.Curated,
		})
		skills = append(skills, it.Skill)
	}
	return map[string]any{
		"principal": map[string]any{},
		"resource": map[string]any{
			"skills": skills,
		},
		"context": map[string]any{
			"request_id": p.RequestID,
			"item_count": len(p.Items),
		},
		"payload": map[string]any{
			"items":      items,
			"skills":     skills,
			"request_id": p.RequestID,
			// Defaults so policies can call .matches/.exists without has() guards.
			"postinstall":          p.Postinstall,
			"shell_hook":           p.ShellHook,
			"scan":                 p.Scan,
			"largest_binary_bytes": p.LargestBinaryBytes,
			"allow_postinstall":    false,
			"large_binary":         false,
			// Admission facts (see payloadFromInstall).
			"manifest_present":     p.ManifestPresent,
			"manifest_mode":        p.ManifestMode,
			"digest":               p.Digest,
			"digest_status":        p.DigestStatus,
			"digest_source":        p.DigestSource,
			"declared_tools":       stringsToAny(p.DeclaredTools),
			"undeclared_tools":     stringsToAny(p.UndeclaredTools),
			"data_class":           p.DataClass,
			"max_data_class":       p.MaxDataClass,
			"data_class_over":      p.DataClassOver,
			"data_class_escalated": p.DataClassEscalated,
			"approval_state":       p.ApprovalState,
		},
	}
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// remarshal copies any payload shape (typed struct in-process,
// map[string]any cross-process) into the target via JSON. Implements
// the kit/bus re-marshal-hop pattern.
func remarshal(payload any, target any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// EvaluatePayload is exposed for the `gym guard test` command and unit
// tests. activation must already be a map[string]any in the standard
// CEL shape (principal/resource/context/payload).
func (g *Guard) EvaluatePayload(activation map[string]any) (Verdict, []Finding) {
	return g.engine.Evaluate(activation)
}

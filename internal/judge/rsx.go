package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"sync"
	"time"

	"hop.top/kit/go/console/progress"

	"hop.top/gym/internal/events"
)

// DefaultSubprocessTimeout bounds each individual rsx call. The two
// calls run in parallel so the worst-case wall time is one timeout.
const DefaultSubprocessTimeout = 30 * time.Second

// Resolver runs the two rsx subcommands (analyze + policy test) and
// rolls the parsed output into a [Judgement]. The interface lets the
// test suite stub it without spawning subprocesses.
type Resolver interface {
	Resolve(ctx context.Context, repo string) (Judgement, error)
}

// CommandRunner executes argv and returns stdout. Stubbable for tests.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// defaultRunner shells out via os/exec.CommandContext and captures
// stdout. stderr is discarded — rsx logs status updates there and the
// JSON we parse lives on stdout.
func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("%s %s: exit %d", name, strings.Join(args, " "), ee.ExitCode())
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// rsxResolver is the production [Resolver]. It shells out to rsx via
// CommandContext, captures stdout, and parses the documented subset of
// the rsx JSON schema.
type rsxResolver struct {
	binary   string
	policy   string
	timeout  time.Duration
	run      CommandRunner
	reporter progress.Reporter
}

// NewResolver returns the default subprocess-based resolver. The
// resulting value is safe to share between goroutines.
func NewResolver(opts ...Option) Resolver {
	r := &rsxResolver{
		binary:   "rsx",
		policy:   "balanced",
		timeout:  DefaultSubprocessTimeout,
		run:      defaultRunner,
		reporter: progress.Discard(),
	}
	for _, opt := range opts {
		opt.applyResolver(r)
	}
	if r.reporter == nil {
		r.reporter = progress.Discard()
	}
	return r
}

// Option configures a Resolver or Judge. Exposed via WithBinary etc.
type Option interface {
	applyResolver(r *rsxResolver)
	applyJudge(j *Judge)
}

type funcOption struct {
	resolver func(*rsxResolver)
	judge    func(*Judge)
}

func (f funcOption) applyResolver(r *rsxResolver) {
	if f.resolver != nil {
		f.resolver(r)
	}
}
func (f funcOption) applyJudge(j *Judge) {
	if f.judge != nil {
		f.judge(j)
	}
}

// WithBinary overrides the rsx binary path/name (default: "rsx" on PATH).
func WithBinary(name string) Option {
	return funcOption{resolver: func(r *rsxResolver) { r.binary = name }}
}

// WithPolicy overrides the --policy flag passed to `rsx policy test`
// (default: "balanced").
func WithPolicy(name string) Option {
	return funcOption{resolver: func(r *rsxResolver) { r.policy = name }}
}

// WithSubprocessTimeout overrides per-call subprocess timeout
// (default: 30s).
func WithSubprocessTimeout(d time.Duration) Option {
	return funcOption{resolver: func(r *rsxResolver) {
		if d > 0 {
			r.timeout = d
		}
	}}
}

// WithRunner overrides the command runner. Used by tests to stub
// rsx out of the loop.
func WithRunner(run CommandRunner) Option {
	return funcOption{resolver: func(r *rsxResolver) {
		if run != nil {
			r.run = run
		}
	}}
}

// WithProgress wires a kit/console/progress reporter so each rsx
// subprocess call (analyze + policy test) emits start + completion
// events. Pass progress.FromContext(ctx) at the call site to hook into
// the global --progress-format selection.
//
// Nil falls back to progress.Discard().
func WithProgress(r progress.Reporter) Option {
	return funcOption{resolver: func(rr *rsxResolver) {
		if r == nil {
			r = progress.Discard()
		}
		rr.reporter = r
	}}
}

// rsxAnalyzeResponse is gym's narrow view of `rsx analyze --json`.
// We parse only what the judgement needs; rsx adds fields freely
// between minor versions (shape was inspected on 2026-05-05
// against rsx vdev).
type rsxAnalyzeResponse struct {
	Repo struct {
		Owner string `json:"owner"`
		Name  string `json:"name"`
		URL   string `json:"url"`
	} `json:"repo"`
	Scores struct {
		Overall float64 `json:"overall"`
	} `json:"scores"`
	Policy struct {
		Profile  string `json:"profile"`
		Passed   bool   `json:"passed"`
		ExitCode int    `json:"exit_code"`
	} `json:"policy"`
	Evidence []struct {
		Type       string  `json:"type"`
		Key        string  `json:"key"`
		Value      string  `json:"value"`
		Source     string  `json:"source"`
		Confidence float64 `json:"confidence"`
	} `json:"evidence"`
	Summary struct {
		Deterministic  string `json:"deterministic"`
		Recommendation string `json:"recommendation"`
	} `json:"summary"`
}

// rsxPolicyTestResponse mirrors `rsx policy test --json`. The
// envelope is identical to analyze; only the policy block differs in
// observed practice. We inspect Policy.Passed and surface
// Summary.Recommendation as a finding when the policy fails.
type rsxPolicyTestResponse = rsxAnalyzeResponse

// Resolve runs both rsx subcommands in parallel and rolls the result
// into a Judgement. Errors from either call are returned to the
// caller — the [Judge] subscriber decides whether to fail-open
// (default) or fail-closed (--strict).
func (r *rsxResolver) Resolve(ctx context.Context, repo string) (Judgement, error) {
	if strings.TrimSpace(repo) == "" {
		return Judgement{}, errors.New("judge: empty repo")
	}

	type result struct {
		resp rsxAnalyzeResponse
		err  error
	}
	analyzeCh := make(chan result, 1)
	policyCh := make(chan result, 1)

	var wg sync.WaitGroup
	wg.Add(2)

	// Resolver-level reporter (constructor opt) wins; otherwise pull
	// from ctx so each command's --progress-format selection naturally
	// flows in without per-call wiring at Judge.New time.
	rep := r.reporter
	if rep == nil || rep == progress.Discard() {
		rep = progress.FromContext(ctx)
	}

	go func() {
		defer wg.Done()
		ctx2, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()
		emitStart(ctx2, rep, "judge-analyze", repo)
		out, err := r.run(ctx2, r.binary, "analyze", repo, "--json")
		var resp rsxAnalyzeResponse
		if err == nil {
			err = parseRSXJSON(out, &resp)
		}
		emitDone(ctx2, rep, "judge-analyze", repo, err == nil)
		analyzeCh <- result{resp: resp, err: err}
	}()

	go func() {
		defer wg.Done()
		ctx2, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()
		emitStart(ctx2, rep, "judge-policy", repo)
		out, err := r.run(ctx2, r.binary, "policy", "test", repo, "--json", "--policy", r.policy)
		var resp rsxPolicyTestResponse
		if err == nil {
			err = parseRSXJSON(out, &resp)
		}
		emitDone(ctx2, rep, "judge-policy", repo, err == nil)
		policyCh <- result{resp: resp, err: err}
	}()

	wg.Wait()
	close(analyzeCh)
	close(policyCh)

	a := <-analyzeCh
	p := <-policyCh

	if a.err != nil && p.err != nil {
		return Judgement{}, fmt.Errorf("rsx analyze + policy test failed: %v; %v", a.err, p.err)
	}

	// Prefer analyze's evidence list (richer) but accept policy's if
	// analyze failed in isolation.
	source := a.resp
	if a.err != nil {
		source = p.resp
	}

	score := scoreFromOverall(source.Scores.Overall)
	verdict := verdictFrom(source, p.resp, p.err == nil)

	signals := make([]events.JudgeSignal, 0, len(source.Evidence))
	for _, ev := range source.Evidence {
		signals = append(signals, events.JudgeSignal{
			Type:       ev.Type,
			Key:        ev.Key,
			Value:      ev.Value,
			Source:     ev.Source,
			Confidence: ev.Confidence,
		})
	}

	findings := findingsFromResponses(p.resp, p.err)

	return Judgement{
		Repo:     repo,
		Score:    score,
		Verdict:  verdict,
		Signals:  signals,
		Findings: findings,
	}, nil
}

// parseRSXJSON tolerates leading whitespace and a trailing newline.
// Empty stdout is treated as a parse error so the caller can
// fail-open with a clear message.
func parseRSXJSON(raw []byte, into any) error {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return errors.New("rsx returned empty stdout")
	}
	return json.Unmarshal(raw, into)
}

// scoreFromOverall scales rsx's 0..10 overall to gym's 0..100 band.
// Out-of-range values are clamped — defensive against rsx schema
// drift introducing a different scale.
func scoreFromOverall(overall float64) int {
	if math.IsNaN(overall) || overall <= 0 {
		return 0
	}
	score := int(math.Round(overall * 10))
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

// verdictFrom blends the policy verdict with the score band:
//
//   - rsx policy passed=true && score >= 70 → allow
//   - rsx policy passed=true && score >= 40 → warn
//   - rsx policy passed=false                → deny
//   - score < 40 (regardless of policy)      → deny
//
// When the policy call failed (policyOK=false) we trust the analyze
// score alone and never deny on score band — denying without a fresh
// policy decision would conflate "rsx errored" with "rsx flagged".
func verdictFrom(analyze rsxAnalyzeResponse, policy rsxPolicyTestResponse, policyOK bool) string {
	score := scoreFromOverall(analyze.Scores.Overall)
	if !policyOK {
		switch {
		case score >= 70:
			return "allow"
		case score >= 40:
			return "warn"
		default:
			return "warn"
		}
	}
	if !policy.Policy.Passed {
		return "deny"
	}
	switch {
	case score >= 70:
		return "allow"
	case score >= 40:
		return "warn"
	default:
		return "deny"
	}
}

// findingsFromResponses converts rsx's policy summary + recommendation
// into gym's [events.PolicyFinding] shape. We don't fabricate findings
// when the policy call errored out — the subscriber surfaces the
// transport failure separately.
func findingsFromResponses(p rsxPolicyTestResponse, perr error) []events.PolicyFinding {
	if perr != nil {
		return []events.PolicyFinding{{
			Rule:     "rsx-policy-unavailable",
			Severity: "warn",
			Message:  perr.Error(),
		}}
	}
	if p.Policy.Passed && strings.TrimSpace(p.Summary.Recommendation) == "" {
		return nil
	}
	severity := "warn"
	if !p.Policy.Passed {
		severity = "deny"
	}
	msg := strings.TrimSpace(p.Summary.Recommendation)
	if msg == "" {
		msg = strings.TrimSpace(p.Summary.Deterministic)
	}
	if msg == "" {
		return nil
	}
	return []events.PolicyFinding{{
		Rule:     "rsx-policy-" + p.Policy.Profile,
		Severity: severity,
		Message:  msg,
	}}
}

// emitStart emits a "phase started" event (no OK pointer).
func emitStart(ctx context.Context, r progress.Reporter, phase, item string) {
	r.Emit(ctx, progress.Event{
		Phase: phase,
		Item:  item,
		At:    time.Now().UTC(),
	})
}

// emitDone emits a "phase done" event (OK pointer set per ok flag).
func emitDone(ctx context.Context, r progress.Reporter, phase, item string, ok bool) {
	flag := ok
	r.Emit(ctx, progress.Event{
		Phase: phase,
		Item:  item,
		OK:    &flag,
		At:    time.Now().UTC(),
	})
}

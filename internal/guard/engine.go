package guard

import (
	"errors"
	"fmt"
	"sort"

	"hop.top/kit/go/runtime/policy/cel"
)

// Severity is the verdict surface a triggered policy contributes to.
type Severity string

// Severity values. Note: a policy whose effect resolves to "allow"
// never contributes to a deny/warn verdict regardless of severity.
const (
	SeverityAllow Severity = "allow"
	SeverityWarn  Severity = "warn"
	SeverityDeny  Severity = "deny"
)

// Effect mirrors kit/runtime/policy.Effect but adds "warn" so a single
// policy file can yield a non-fatal finding without going to the
// override layer.
type Effect string

// Effect values.
const (
	EffectAllow Effect = "allow"
	EffectWarn  Effect = "warn"
	EffectDeny  Effect = "deny"
)

// Policy is one loaded rule. Source is the absolute path to the file
// it came from; useful for `gym guard list`.
type Policy struct {
	Name      string
	When      string
	Effect    Effect // resolution when when() == true
	Otherwise Effect // resolution when when() == false (default Allow)
	Severity  Severity
	Message   string
	Source    string
}

// Verdict is the resolved overall outcome across N policies.
type Verdict string

// Verdict values.
const (
	VerdictAllow Verdict = "allow"
	VerdictWarn  Verdict = "warn"
	VerdictDeny  Verdict = "deny"
)

// Finding is one policy's contribution to the overall verdict.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message,omitempty"`
}

// Engine compiles + evaluates the loaded policies against an
// activation map. Goroutine-safe.
type Engine struct {
	evaluator *cel.Evaluator
	policies  []Policy
}

// NewEngine compiles every policy and returns a ready engine. Compile
// errors surface here, fail-loud per kit/runtime/policy convention.
func NewEngine(policies []Policy) (*Engine, error) {
	ev, err := cel.New()
	if err != nil {
		return nil, fmt.Errorf("guard: cel env: %w", err)
	}
	// Compile in deterministic order so repeat runs report the same
	// "first policy in error" if any.
	sorted := append([]Policy(nil), policies...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, p := range sorted {
		if p.Name == "" {
			return nil, errors.New("guard: policy with empty name")
		}
		if p.When == "" {
			return nil, fmt.Errorf("guard: policy %q: when expression required", p.Name)
		}
		if err := ev.Compile(p.Name, p.When); err != nil {
			return nil, fmt.Errorf("guard: compile %q: %w", p.Name, err)
		}
	}
	return &Engine{evaluator: ev, policies: sorted}, nil
}

// Policies returns the compiled policy list (sorted by name). The
// returned slice is a copy; mutations don't affect the engine.
func (e *Engine) Policies() []Policy {
	out := make([]Policy, len(e.policies))
	copy(out, e.policies)
	return out
}

// Evaluate runs every policy against activation and returns the
// resolved verdict + findings. activation MUST contain principal,
// resource, context, payload keys (cel.New declared all four). Engine
// fills in any missing key as an empty map so callers can omit the
// ones they don't use.
func (e *Engine) Evaluate(activation map[string]any) (Verdict, []Finding) {
	if activation == nil {
		activation = map[string]any{}
	}
	for _, k := range []string{"principal", "resource", "context", "payload"} {
		if _, ok := activation[k]; !ok {
			activation[k] = map[string]any{}
		}
	}

	verdict := VerdictAllow
	findings := make([]Finding, 0)
	for _, p := range e.policies {
		matched, err := e.evaluator.Eval(p.Name, activation)
		if err != nil {
			// Eval errors are deny-fail-closed: a broken policy must
			// not silently allow.
			findings = append(findings, Finding{
				Rule:     p.Name,
				Severity: string(SeverityDeny),
				Message:  fmt.Sprintf("evaluation error: %v", err),
			})
			verdict = VerdictDeny
			continue
		}
		eff := p.Otherwise
		if matched {
			eff = p.Effect
		}
		switch eff {
		case EffectAllow:
			// no-op; allow doesn't contribute a finding.
		case EffectWarn:
			findings = append(findings, Finding{
				Rule:     p.Name,
				Severity: string(SeverityWarn),
				Message:  p.Message,
			})
			if verdict == VerdictAllow {
				verdict = VerdictWarn
			}
		case EffectDeny:
			findings = append(findings, Finding{
				Rule:     p.Name,
				Severity: string(SeverityDeny),
				Message:  p.Message,
			})
			verdict = VerdictDeny
		}
	}
	return verdict, findings
}

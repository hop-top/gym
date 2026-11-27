package guard

import (
	"context"
	"errors"
	"fmt"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/spf13/viper"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
)

// DefaultGateTimeout is the bounded wait for a guard verdict from the
// bus. Defaults to 30 s.
const DefaultGateTimeout = 30 * time.Second

// GateConfig configures a Gate call.
type GateConfig struct {
	// Timeout is the bus request/response deadline. Zero means
	// DefaultGateTimeout.
	Timeout time.Duration
	// Insecure flips deny -> warn (--insecure). Verdicts of
	// VerdictDeny still surface as a logged finding but the install
	// is allowed to proceed.
	Insecure bool
	// Strict flips warn -> deny (--strict). On verdict warn the gate
	// returns ErrPolicyDeny.
	Strict bool
	// Logger is the destination for warn-level findings. May be nil.
	Logger *charmlog.Logger
	// Viper, when supplied, fills Strict/Insecure if they're zero.
	Viper *viper.Viper
}

// ErrPolicyDeny is returned by GateInstall / GateUpgrade when the
// guard's verdict is "deny" (and --insecure isn't set), or when the
// gate times out under --strict.
var ErrPolicyDeny = errors.New("guard: policy deny")

// GateInstall publishes gym.skill.install.requested on bus and blocks
// for a correlated policy.guard.install.evaluated. The verdict drives
// the return:
//
//   - allow: nil
//   - warn: nil (logger.Warn the findings)
//   - deny: ErrPolicyDeny (unless cfg.Insecure: warn + nil)
//   - timeout: nil (warn) or ErrPolicyDeny under cfg.Strict
//
// payload's RequestID, when empty, gets filled in by the bus helper.
func GateInstall(ctx context.Context, b *eventbus.Bus, payload events.SkillInstallRequestedPayload, cfg GateConfig) error {
	cfg = applyViper(cfg)
	if b == nil {
		// No bus → no guard wired. Fail-open by default; fail-closed
		// in strict mode.
		if cfg.Strict {
			return fmt.Errorf("%w: bus unavailable", ErrPolicyDeny)
		}
		return nil
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultGateTimeout
	}
	resp, err := b.RequestResponse(
		ctx,
		events.TopicSkillInstallRequested,
		string(events.TopicPolicyGuardInstallEvaluated),
		payload,
		timeout,
	)
	return resolveVerdict(resp, err, cfg)
}

// GateUpgrade is the upgrade-side counterpart. Same semantics as
// GateInstall but publishes gym.skill.upgrade.planned.
func GateUpgrade(ctx context.Context, b *eventbus.Bus, payload events.SkillUpgradePlannedPayload, cfg GateConfig) error {
	cfg = applyViper(cfg)
	if b == nil {
		if cfg.Strict {
			return fmt.Errorf("%w: bus unavailable", ErrPolicyDeny)
		}
		return nil
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultGateTimeout
	}
	resp, err := b.RequestResponse(
		ctx,
		events.TopicSkillUpgradePlanned,
		string(events.TopicPolicyGuardInstallEvaluated),
		payload,
		timeout,
	)
	return resolveVerdict(resp, err, cfg)
}

// PolicyDenyError is the typed form of ErrPolicyDeny returned for a
// deny verdict (or a warn verdict under --strict). It keeps the
// findings so callers can derive a rejection reason. errors.Is(err,
// ErrPolicyDeny) holds.
type PolicyDenyError struct {
	Findings []events.PolicyFinding
}

func (e *PolicyDenyError) Error() string {
	return ErrPolicyDeny.Error() + ": " + summarizeFindings(e.Findings)
}

// Unwrap lets errors.Is match ErrPolicyDeny.
func (e *PolicyDenyError) Unwrap() error { return ErrPolicyDeny }

func resolveVerdict(resp eventbus.Response, err error, cfg GateConfig) error {
	if err != nil {
		if errors.Is(err, eventbus.ErrTimeout) {
			if cfg.Strict {
				return fmt.Errorf("%w: gate timeout", ErrPolicyDeny)
			}
			if cfg.Logger != nil {
				cfg.Logger.Warnf("guard: gate timeout (request_id=%s); proceeding fail-open", resp.RequestID)
			}
			return nil
		}
		return err
	}
	var p events.PolicyGuardInstallEvaluatedPayload
	if err := resp.Decode(&p); err != nil {
		return fmt.Errorf("guard: decode verdict: %w", err)
	}
	switch Verdict(p.Verdict) {
	case VerdictAllow:
		return nil
	case VerdictWarn:
		if cfg.Strict {
			return &PolicyDenyError{Findings: p.Findings}
		}
		logFindings(cfg.Logger, p.Findings)
		return nil
	case VerdictDeny:
		if cfg.Insecure {
			logFindings(cfg.Logger, p.Findings)
			return nil
		}
		return &PolicyDenyError{Findings: p.Findings}
	default:
		// Unknown verdict — fail-closed under strict, fail-open
		// otherwise (defensive).
		if cfg.Strict {
			return fmt.Errorf("%w: unknown verdict %q", ErrPolicyDeny, p.Verdict)
		}
		if cfg.Logger != nil {
			cfg.Logger.Warnf("guard: unknown verdict %q; proceeding", p.Verdict)
		}
		return nil
	}
}

func applyViper(cfg GateConfig) GateConfig {
	if cfg.Viper != nil {
		if !cfg.Strict {
			cfg.Strict = eventbus.IsStrict(cfg.Viper)
		}
		if !cfg.Insecure && cfg.Viper.IsSet("insecure") {
			cfg.Insecure = cfg.Viper.GetBool("insecure")
		}
		if cfg.Timeout == 0 && cfg.Viper.IsSet("gate-timeout") {
			cfg.Timeout = cfg.Viper.GetDuration("gate-timeout")
		}
	} else {
		// Fall back to global viper for callers that haven't plumbed
		// a per-command vp. Cobra binds --strict / --insecure /
		// --gate-timeout to the global instance via cobra.go.
		v := viper.GetViper()
		if v != nil {
			if !cfg.Strict {
				cfg.Strict = eventbus.IsStrict(v)
			}
			if !cfg.Insecure && v.IsSet("insecure") {
				cfg.Insecure = v.GetBool("insecure")
			}
			if cfg.Timeout == 0 && v.IsSet("gate-timeout") {
				cfg.Timeout = v.GetDuration("gate-timeout")
			}
		}
	}
	return cfg
}

func summarizeFindings(fs []events.PolicyFinding) string {
	if len(fs) == 0 {
		return "policy denied"
	}
	out := ""
	for i, f := range fs {
		if i > 0 {
			out += "; "
		}
		out += f.Rule
		if f.Message != "" {
			out += ": " + f.Message
		}
	}
	return out
}

func logFindings(l *charmlog.Logger, fs []events.PolicyFinding) {
	if l == nil {
		return
	}
	for _, f := range fs {
		l.Warnf("guard: %s [%s] %s", f.Rule, f.Severity, f.Message)
	}
}

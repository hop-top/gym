package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/install"
)

// registerAdmissionFlags adds the persistent --max-data-class flag
// (operator override of admission.max_data_class; may raise or lower).
func (a *App) registerAdmissionFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&a.maxDataClass, "max-data-class", "",
		"data class ceiling for skill admission (overrides admission.max_data_class)")
}

// applyAdmissionFlags layers the command-line ceiling over config. An
// invalid value is rejected by the admission stage before any install.
func (a *App) applyAdmissionFlags(cfg *config.Config) {
	if a.maxDataClass != "" {
		cfg.Admission.MaxDataClass = a.maxDataClass
	}
}

func newRequestID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

// withAdmission copies admission facts onto an install-requested payload.
func withAdmission(p events.SkillInstallRequestedPayload, adm install.AdmissionResult) events.SkillInstallRequestedPayload {
	p.ManifestPresent = adm.ManifestPresent
	p.ManifestMode = adm.ManifestMode
	p.Digest = adm.Digest
	p.DigestStatus = adm.DigestStatus
	p.DigestSource = adm.DigestSource
	p.DeclaredTools = adm.DeclaredTools
	p.UndeclaredTools = adm.UndeclaredTools
	p.DataClass = adm.DataClass
	p.MaxDataClass = adm.MaxDataClass
	p.DataClassOver = adm.DataClassOver
	p.DataClassEscalated = adm.DataClassEscalated
	p.ApprovalState = adm.ApprovalState
	return p
}

// ruleReasons maps admission policy names whose rejection reason is not
// simply the name minus the admission- prefix.
var ruleReasons = map[string]string{
	"admission-data-class":        install.ReasonDataClassExceeds,
	"admission-approval-enforced": install.ReasonApprovalRequired,
	"admission-manifest-required": install.ReasonManifestMissing,
}

// ruleReason returns the stable rejection reason for an admission
// policy rule, or false when the rule is not an admission-* rule.
func ruleReason(rule string) (string, bool) {
	if r, ok := ruleReasons[rule]; ok {
		return r, true
	}
	if rest, ok := strings.CutPrefix(rule, "admission-"); ok && rest != "" {
		return rest, true
	}
	return "", false
}

// rejectionReason derives the admission rejection reason from an
// install error: a hard AdmissionError, or the first admission-* finding
// of a policy deny (deny-severity findings first). Empty when the error is not an admission failure.
func rejectionReason(err error) string {
	var ae *install.AdmissionError
	if errors.As(err, &ae) {
		return ae.Reason
	}
	var pd *guard.PolicyDenyError
	if errors.As(err, &pd) {
		// Deny-severity findings decide the verdict; warn findings only
		// do under --strict (no deny finding exists then).
		for _, sev := range []string{"deny", ""} {
			for _, f := range pd.Findings {
				if sev != "" && f.Severity != sev {
					continue
				}
				if r, ok := ruleReason(f.Rule); ok {
					return r
				}
			}
		}
	}
	return ""
}

// publishInstallRejected publishes gym.skill.install.rejected once for
// an admission failure. Non-admission errors publish nothing.
func (a *App) publishInstallRejected(ctx context.Context, skill, scope, requestID string, err error) {
	if a == nil || a.Bus() == nil || err == nil {
		return
	}
	reason := rejectionReason(err)
	if reason == "" {
		return
	}
	if perr := a.Bus().Publish(ctx, events.TopicSkillInstallRejected, events.SkillInstallRejectedPayload{
		Skill:     skill,
		Scope:     scope,
		Reason:    reason,
		RequestID: requestID,
	}); perr != nil {
		a.Logger().Warnf("publish %s: %v", events.TopicSkillInstallRejected, perr)
	}
}

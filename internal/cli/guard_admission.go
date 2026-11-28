package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/gym/internal/approvals"
	"hop.top/gym/internal/config"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
)

// skillDirTarget reports whether target selects admission mode: a
// directory, or a SKILL.md file (its directory is the skill root). Any
// other file is a raw JSON payload.
func skillDirTarget(target string) (string, bool) {
	st, err := os.Stat(target)
	if err != nil {
		return "", false
	}
	if st.IsDir() {
		return target, true
	}
	if filepath.Base(target) == "SKILL.md" {
		return filepath.Dir(target), true
	}
	return "", false
}

// skillDirOptions simulate operator state for a dry run.
type skillDirOptions struct {
	ApprovalState string
	ExpectDigest  string
}

// skillDirResult is the outcome of the shared admission + policy
// evaluation for one skill directory.
type skillDirResult struct {
	Verdict  guard.Verdict
	Findings []guard.Finding
	Facts    install.AdmissionResult
	// Hard is the admission rejection reason when a hard integrity
	// check failed (policies are not evaluated then).
	Hard string
}

// evaluateSkillDir runs the same admission function and policy engine
// the install path uses, read-only: nothing is copied or installed.
// Install-time inputs that depend on a destination (paths) are absent.
func (a *App) evaluateSkillDir(dir string, opts skillDirOptions) (skillDirResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return skillDirResult{}, err
	}
	cfg, err := config.LoadMergedConfig(a.CWD, a.cfgPaths, a.cfgOverrides)
	if err != nil {
		return skillDirResult{}, err
	}
	a.applyAdmissionFlags(&cfg)
	if opts.ExpectDigest != "" && !install.ValidDigest(opts.ExpectDigest) {
		return skillDirResult{}, fmt.Errorf("invalid --expect-digest %q (want sha256:<64 lowercase hex>)", opts.ExpectDigest)
	}
	if opts.ApprovalState != "" && !approvals.ValidState(opts.ApprovalState) {
		return skillDirResult{}, fmt.Errorf("invalid --approval-state %q (one of: unreviewed, approved, revoked)", opts.ApprovalState)
	}

	entry := registry.SkillEntry{Name: filepath.Base(abs), SourceURL: abs, Version: "0.0.0"}
	adm, err := install.Admit(install.AdmissionInput{
		SkillPath:     abs,
		Entry:         entry,
		Admission:     cfg.Admission,
		SourceKind:    lock.KindDir,
		ApprovalState: opts.ApprovalState,
		ExpectDigest:  opts.ExpectDigest,
	})
	var ae *install.AdmissionError
	if errors.As(err, &ae) {
		return skillDirResult{
			Verdict:  guard.VerdictDeny,
			Findings: []guard.Finding{{Rule: ae.Reason, Severity: string(guard.SeverityDeny), Message: ae.Detail}},
			Facts:    adm,
			Hard:     ae.Reason,
		}, nil
	}
	if err != nil {
		return skillDirResult{}, err
	}

	eng, err := a.loadGuardEngine()
	if err != nil {
		return skillDirResult{}, err
	}
	scan := install.ScanStaging(abs)
	payload := withAdmission(events.SkillInstallRequestedPayload{
		Skill:              entry.Name,
		Version:            entry.Version,
		SourceURL:          install.RedactURL(entry.SourceURL),
		Scope:              "project",
		Postinstall:        scan.Postinstall,
		ShellHook:          scan.ShellHook,
		Scan:               scan.Scan,
		Scripts:            scan.Scripts,
		LargestBinaryBytes: scan.LargestBinaryBytes,
	}, adm)
	verdict, findings := eng.Evaluate(guard.InstallActivation(payload))
	return skillDirResult{Verdict: verdict, Findings: findings, Facts: adm}, nil
}

func (a *App) runGuardTestDir(cmd *cobra.Command, dir string) error {
	opts := skillDirOptions{}
	format := "table"
	if cmd != nil {
		opts.ApprovalState, _ = cmd.Flags().GetString("approval-state")
		opts.ExpectDigest, _ = cmd.Flags().GetString("expect-digest")
		if v, _ := cmd.Flags().GetString("format"); v != "" {
			format = v
		}
	}
	res, err := a.evaluateSkillDir(dir, opts)
	if err != nil {
		return err
	}
	facts := res.Facts
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"verdict":  string(res.Verdict),
			"findings": res.Findings,
			"facts": map[string]any{
				"manifest_present":     facts.ManifestPresent,
				"digest":               facts.Digest,
				"digest_status":        facts.DigestStatus,
				"declared_tools":       facts.DeclaredTools,
				"undeclared_tools":     facts.UndeclaredTools,
				"data_class":           facts.DataClass,
				"max_data_class":       facts.MaxDataClass,
				"data_class_over":      facts.DataClassOver,
				"approval_state":       facts.ApprovalState,
				"data_class_escalated": facts.DataClassEscalated,
			},
		})
	default:
		fmt.Fprintf(a.Stdout, "verdict: %s\n", res.Verdict)
		if len(res.Findings) == 0 {
			fmt.Fprintln(a.Stdout, "no findings")
		}
		for _, f := range res.Findings {
			fmt.Fprintf(a.Stdout, "  %s [%s] %s\n", f.Rule, f.Severity, f.Message)
		}
		fmt.Fprintf(a.Stdout, "digest: %s (%s)\n", orDash(facts.Digest), orDash(facts.DigestStatus))
		fmt.Fprintf(a.Stdout, "manifest: %v\n", facts.ManifestPresent)
		fmt.Fprintf(a.Stdout, "declared_tools: %s\n", orDash(strings.Join(facts.DeclaredTools, " ")))
		fmt.Fprintf(a.Stdout, "undeclared_tools: %s\n", orDash(strings.Join(facts.UndeclaredTools, " ")))
		fmt.Fprintf(a.Stdout, "data_class: %s (ceiling %s)\n", orDash(facts.DataClass), orDash(facts.MaxDataClass))
		fmt.Fprintf(a.Stdout, "approval_state: %s\n", orDash(facts.ApprovalState))
	}
	if res.Verdict == guard.VerdictDeny {
		return fmt.Errorf("policy deny")
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

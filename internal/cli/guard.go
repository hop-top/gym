// Package cli — guard.go: install/upgrade gate plumbing + the
// `gym guard list` / `gym guard test` subcommands.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/scope"
	"hop.top/gym/internal/upgrade"
	"hop.top/gym/internal/xdg"
)

// newGuardForApp builds and starts a guard.Guard subscriber backed by
// the App's bus. Loads built-in policies + user/project overrides via
// the same path loadGuardEngine uses for `gym guard list/test`.
func newGuardForApp(a *App) (*guard.Guard, error) {
	if a == nil || a.Bus() == nil {
		return nil, fmt.Errorf("guard: app/bus not ready")
	}
	eng, err := a.loadGuardEngine()
	if err != nil {
		return nil, err
	}
	return guard.New(a.Bus(), guard.WithEngine(eng), guard.WithLogger(a.Logger()))
}

// gateUnavailable reports, under --strict, that the guard subscriber
// failed to start so no verdict can ever arrive. Fail-closed with the
// cause instead of waiting out the gate timeout. Without --strict it
// returns nil: the gate fails open as before (startup already warned).
func (a *App) gateUnavailable() error {
	if a == nil || a.guardErr == nil || !eventbus.IsStrict(viper.GetViper()) {
		return nil
	}
	return fmt.Errorf("%w: guard unavailable: %v", guard.ErrPolicyDeny, a.guardErr)
}

// installGate returns an install.GateFunc that publishes
// gym.skill.install.requested on the bus and blocks on the guard's
// reply. Returns nil when no bus is wired (tests, --help calls).
//
// The staging-dir scan (postinstall, shell hook, biggest binary) is
// forwarded onto the bus payload so CEL policies that key on
// payload.postinstall / payload.shell_hook / payload.scan /
// payload.largest_binary_bytes fire on real install metadata, and the
// admission facts (digest, tools, data class, approval) ride along for
// the admission-* policies. requestID correlates the requested and
// rejected events of one attempt. folder is the scope root of a project
// scope install ("" for global).
func (a *App) installGate(requestID, folder string) install.GateFunc {
	if a == nil || a.Bus() == nil {
		return nil
	}
	return func(ctx context.Context, entry registry.SkillEntry, scope string, paths []string, scan install.ScanResult, adm install.AdmissionResult) error {
		if err := a.gateUnavailable(); err != nil {
			return err
		}
		payload := withAdmission(events.SkillInstallRequestedPayload{
			RequestID:          requestID,
			Skill:              entry.Name,
			Version:            entry.Version,
			SourceURL:          install.RedactURL(entry.SourceURL),
			Scope:              scope,
			Folder:             folderOf(scope, folder),
			Paths:              paths,
			Postinstall:        scan.Postinstall,
			ShellHook:          scan.ShellHook,
			Scan:               scan.Scan,
			LargestBinaryBytes: scan.LargestBinaryBytes,
		}, adm)
		return guard.GateInstall(ctx, a.Bus(), payload, guard.GateConfig{
			Logger: a.Logger(),
		})
	}
}

// gateUpgradePlan publishes gym.skill.upgrade.planned and blocks on
// the verdict. Caller drives this once for the whole plan.
func (a *App) gateUpgradePlan(ctx context.Context, plan []upgrade.PlanItem) error {
	if a == nil || a.Bus() == nil {
		return nil
	}
	if err := a.gateUnavailable(); err != nil {
		return err
	}
	items := make([]events.UpgradePlanItem, 0, len(plan))
	for _, p := range plan {
		items = append(items, events.UpgradePlanItem{
			Skill:  p.Skill.Name,
			Scope:  p.Scope,
			From:   p.Skill.Version,
			Source: install.RedactURL(p.Skill.SourceURL),
		})
	}
	payload := events.SkillUpgradePlannedPayload{Items: items, Folder: planFolder(plan)}
	return guard.GateUpgrade(ctx, a.Bus(), payload, guard.GateConfig{
		Logger: a.Logger(),
	})
}

// guardCobra builds the `gym guard` subcommand tree.
func (a *App) guardCobra() *cobra.Command {
	g := &cobra.Command{
		Use:   "guard",
		Short: "Inspect and dry-run install/upgrade policy guards",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runGuardList(cmd) },
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List effective policy set",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runGuardList(cmd) },
	}
	test := &cobra.Command{
		Use:   "test <skill-dir|SKILL.md|payload.json>",
		Short: "Dry-run admission and policies against a skill directory, or policies against a JSON payload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runGuardTest(cmd, args[0])
		},
	}
	test.Flags().String("approval-state", "", "simulate approval state for a skill directory: unreviewed|approved|revoked")
	test.Flags().String("expect-digest", "", "simulate a digest pin for a skill directory (sha256:<hex>)")
	g.AddCommand(list, test, a.approveCobra(), a.revokeCobra())
	return g
}

func (a *App) loadGuardEngine() (*guard.Engine, error) {
	dirs, err := xdg.AppDirs(config.AppName)
	userDir := ""
	if err == nil {
		userDir = filepath.Join(dirs.ConfigHome, "policies")
	}
	// Every folder scope on the chain contributes its policies, outermost
	// first. Chain warnings are the config loader's to print.
	roots := scope.Chain(a.startDir()).Roots
	var projectDirs []string
	for i := len(roots) - 1; i >= 0; i-- {
		projectDirs = append(projectDirs, filepath.Join(roots[i], ".gym", "policies"))
	}
	policies, err := guard.Load(guard.LoadOptions{
		UserDir:     userDir,
		Logger:      a.Logger(),
		ProjectDirs: projectDirs,
	})
	if err != nil {
		return nil, err
	}
	return guard.NewEngine(policies)
}

func (a *App) runGuardList(cmd *cobra.Command) error {
	eng, err := a.loadGuardEngine()
	if err != nil {
		return err
	}
	policies := eng.Policies()
	sort.SliceStable(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })

	format := "table"
	if cmd != nil {
		if v, _ := cmd.Flags().GetString("format"); v != "" {
			format = v
		}
	}
	switch strings.ToLower(format) {
	case "json":
		out := make([]map[string]any, 0, len(policies))
		for _, p := range policies {
			out = append(out, map[string]any{
				"name":     p.Name,
				"effect":   string(p.Effect),
				"severity": string(p.Severity),
				"source":   p.Source,
				"message":  p.Message,
			})
		}
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	default:
		fmt.Fprintf(a.Stdout, "%-25s  %-6s  %-6s  %s\n", "POLICY", "EFFECT", "SEV", "SOURCE")
		for _, p := range policies {
			fmt.Fprintf(a.Stdout, "%-25s  %-6s  %-6s  %s\n",
				p.Name, p.Effect, p.Severity, p.Source)
		}
	}
	return nil
}

func (a *App) runGuardTest(cmd *cobra.Command, target string) error {
	if dir, ok := skillDirTarget(target); ok {
		return a.runGuardTestDir(cmd, dir)
	}
	eng, err := a.loadGuardEngine()
	if err != nil {
		return err
	}
	payload, err := loadTestPayload(target)
	if err != nil {
		return err
	}
	verdict, findings := eng.Evaluate(map[string]any{
		"principal": map[string]any{},
		"resource":  payload,
		"context":   map[string]any{},
		"payload":   payload,
	})

	format := "table"
	if cmd != nil {
		if v, _ := cmd.Flags().GetString("format"); v != "" {
			format = v
		}
	}
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"verdict":  string(verdict),
			"findings": findings,
		})
	default:
		fmt.Fprintf(a.Stdout, "verdict: %s\n", verdict)
		if len(findings) == 0 {
			fmt.Fprintln(a.Stdout, "no findings")
		}
		for _, f := range findings {
			fmt.Fprintf(a.Stdout, "  %s [%s] %s\n", f.Rule, f.Severity, f.Message)
		}
	}
	if verdict == guard.VerdictDeny {
		return fmt.Errorf("policy deny")
	}
	return nil
}

// loadTestPayload reads a JSON manifest file (or a directory with a
// gym.json/manifest.json inside) and returns its parsed contents as a
// generic map for CEL activation. The shape matches what install.go
// would publish via gym.skill.install.requested plus any optional
// fields the manifest itself ships (postinstall, paths, scan, etc.).
func loadTestPayload(target string) (map[string]any, error) {
	st, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	path := target
	if st.IsDir() {
		// Try common manifest filenames.
		for _, candidate := range []string{"gym.json", "manifest.json", "skill.json"} {
			full := filepath.Join(target, candidate)
			if _, err := os.Stat(full); err == nil {
				path = full
				break
			}
		}
		if path == target {
			return nil, fmt.Errorf("no manifest found in %s", target)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return p, nil
}

// folderOf is the "folder" payload value for an install: the scope root for
// project scope, empty for global.
func folderOf(scope, root string) string {
	if scope == "project" {
		return root
	}
	return ""
}

// planFolder is the scope root of the project items of a plan. A plan
// covers one scope plus the global mirror, so there is at most one.
func planFolder(plan []upgrade.PlanItem) string {
	for _, p := range plan {
		if p.Scope == "project" {
			return p.Root
		}
	}
	return ""
}

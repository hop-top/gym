package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/state"
)

// Reasons raised by unlearn itself (the others come from install).
const (
	reasonSkillNotInstalled    = "skill-not-installed"
	reasonConfirmationRequired = "confirmation-required"
	unlearnGlobMeta            = "*?["
)

// unlearnRemoveAll deletes a validated install directory. Tests replace it
// to inject a removal failure.
var unlearnRemoveAll = os.RemoveAll

// stdinIsTerminal reports whether r is an interactive terminal. Tests
// replace it to drive the prompt.
var stdinIsTerminal = fileIsTerminal

type unlearnOptions struct {
	Global        bool
	Path          string
	Agents        []string
	DryRun        bool
	Yes           bool
	Force         bool
	IgnoreMissing bool
	Patterns      []string
}

func (a *App) unlearnCobra() *cobra.Command {
	c := &cobra.Command{
		Use:     "unlearn <skill-or-pattern> [skill-or-pattern...]",
		Aliases: []string{"uninstall", "remove", "rm"},
		Short:   "Remove installed skills",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var o unlearnOptions
			o.Global, _ = cmd.Flags().GetBool("global")
			o.Path, _ = cmd.Flags().GetString("path")
			o.Agents, _ = cmd.Flags().GetStringArray("agent")
			o.DryRun, _ = cmd.Flags().GetBool("dry-run")
			o.Yes, _ = cmd.Flags().GetBool("yes")
			o.Force, _ = cmd.Flags().GetBool("force")
			o.IgnoreMissing, _ = cmd.Flags().GetBool("ignore-missing")
			o.Patterns = args
			return a.runUnlearnCtx(cmd.Context(), o)
		},
	}
	c.Flags().BoolP("global", "g", false, "remove from global scope (default: CWD/.gym)")
	c.Flags().String("path", "", "limit to one install path name/path")
	c.Flags().StringArray("agent", nil, "limit install paths by name/path pattern (prefix with ! to exclude)")
	c.Flags().BoolP("dry-run", "n", false, "print what would be removed; change nothing")
	c.Flags().BoolP("yes", "y", false, "skip confirmation for patterns matching several skills")
	c.Flags().Bool("force", false, "remove despite local modification")
	addDirFlag(c)
	c.Flags().Bool("ignore-missing", false, "absent skill is not an error")
	return c
}

// unlearnScopeRoot is the single place the scope root is resolved: --dir,
// else the current directory, for project scope (never an inherited
// ancestor root); empty for global.
func (a *App) unlearnScopeRoot(global bool) string {
	if global {
		return ""
	}
	return a.startDir()
}

func (a *App) runUnlearnCtx(ctx context.Context, o unlearnOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if o.Global && a.scopeDir != "" {
		return errDirGlobal
	}
	rt, err := a.loadScopeAt(a.startDir(), !o.Global)
	if err != nil {
		return err
	}
	scope, root := "project", a.unlearnScopeRoot(o.Global)
	if o.Global {
		scope = "global"
	}
	all, err := a.loadScopeRecords(rt, scope, root)
	if err != nil {
		return err
	}
	cand, err := a.narrowRecords(rt, o, scope, root, all)
	if err != nil {
		return err
	}
	groups, globNames, missing, err := matchSkills(o.Patterns, cand)
	if err != nil {
		return err
	}

	var errs []error
	for _, p := range missing {
		if o.IgnoreMissing {
			a.printlnf("skip %s: not installed", p)
			continue
		}
		errs = append(errs, a.notInstalledError(rt, p, scope, root))
	}
	if len(groups) == 0 {
		return errors.Join(errs...)
	}
	if !o.DryRun && !o.Yes && globNames > 1 {
		if err := a.confirmUnlearn(globNames, scope, root); err != nil {
			return errors.Join(append(errs, err)...)
		}
	}

	rm := &install.Remover{
		Scope: scope, ProjectRoot: root, Force: o.Force,
		Parents:   install.TrustedParents(scope, rt.Config),
		RemoveAll: unlearnRemoveAll,
		SaveState: func(left []state.InstalledSkill) error {
			if scope == "global" {
				return state.SaveGlobal(rt.Dirs.StateHome, left)
			}
			return state.SaveProject(root, left)
		},
	}
	for _, g := range groups {
		if o.DryRun {
			for _, r := range g.recs {
				a.printlnf("would remove %s %s", g.name, r.InstallPath)
			}
			continue
		}
		var res install.RemoveResult
		all, res = a.unlearnSkill(ctx, rm, g, all, scope, root)
		for _, p := range res.Removed {
			a.printlnf("removed %s %s", g.name, p)
		}
		for _, f := range res.Failures {
			errs = append(errs, f.Err)
		}
	}
	return errors.Join(errs...)
}

type skillGroup struct {
	name string
	recs []state.InstalledSkill
}

// unlearnSkill removes one skill's records, bracketed by its uninstall
// events (one request_id per attempt). Events are informational and not
// gated by the guard.
func (a *App) unlearnSkill(ctx context.Context, rm *install.Remover, g skillGroup, all []state.InstalledSkill, scope, root string) ([]state.InstalledSkill, install.RemoveResult) {
	reqID := newRequestID()
	paths := make([]string, 0, len(g.recs))
	for _, r := range g.recs {
		paths = append(paths, r.InstallPath)
	}
	a.publishEvent(ctx, events.TopicSkillUninstallRequested, events.SkillUninstallRequestedPayload{
		Skill: g.name, Scope: scope, Folder: root, Paths: paths, RequestID: reqID,
	})
	left, res := rm.RemoveSkill(g.name, g.recs, all)
	if len(res.Failures) > 0 {
		a.publishEvent(ctx, events.TopicSkillUninstallFailed, events.SkillUninstallFailedPayload{
			Skill: g.name, Scope: scope, Reason: res.Failures[0].Reason, RequestID: reqID,
		})
	} else {
		a.publishEvent(ctx, events.TopicSkillUninstallCompleted, events.SkillUninstallCompletedPayload{
			Skill: g.name, Scope: scope, Folder: root, Paths: res.Removed, RequestID: reqID,
		})
	}
	return left, res
}

func (a *App) publishEvent(ctx context.Context, topic bus.Topic, payload any) {
	if a.Bus() == nil {
		return
	}
	if err := a.Bus().Publish(ctx, topic, payload); err != nil {
		a.Logger().Warnf("publish %s: %v", topic, err)
	}
}

func (a *App) loadScopeRecords(rt runtime, scope, root string) ([]state.InstalledSkill, error) {
	if scope == "global" {
		return state.LoadGlobal(rt.Dirs.StateHome)
	}
	return state.LoadProject(root)
}

// narrowRecords applies --path/--agent exactly as learn resolves them
// (install.ResolveInstallPaths): only records installed into a selected
// install path remain. With neither flag every record stays, so a record
// pointing anywhere is still reached and judged by CheckRecordPath.
func (a *App) narrowRecords(rt runtime, o unlearnOptions, scope, root string, recs []state.InstalledSkill) ([]state.InstalledSkill, error) {
	if o.Path == "" && len(o.Agents) == 0 {
		return recs, nil
	}
	paths, err := install.ResolveInstallPaths(install.Request{
		Scope: scope, PathHint: o.Path, AgentPatterns: o.Agents,
		Config: rt.Config, ProjectRoot: root, CWD: a.CWD,
	})
	if err != nil {
		return nil, err
	}
	var out []state.InstalledSkill
	for _, r := range recs {
		for _, p := range paths {
			if samePath(filepath.Dir(r.InstallPath), p.Path) {
				out = append(out, r)
				break
			}
		}
	}
	return out, nil
}

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := config.ResolveExisting(a)
	rb, errB := config.ResolveExisting(b)
	return errA == nil && errB == nil && ra == rb
}

// matchSkills groups the records matching each pattern by skill name
// (filepath.Match, as train). globNames counts the distinct skills matched
// by pattern arguments (not literal names); missing lists the arguments
// that matched nothing.
func matchSkills(patterns []string, recs []state.InstalledSkill) (groups []skillGroup, globNames int, missing []string, err error) {
	byName := map[string]*skillGroup{}
	glob := map[string]bool{}
	for _, pat := range patterns {
		isGlob := strings.ContainsAny(pat, unlearnGlobMeta)
		hit := false
		for _, r := range recs {
			ok := pat == r.Name
			if !ok {
				if ok, err = filepath.Match(pat, r.Name); err != nil {
					return nil, 0, nil, fmt.Errorf("bad pattern %q: %w", pat, err)
				}
			}
			if !ok {
				continue
			}
			hit = true
			g := byName[r.Name]
			if g == nil {
				g = &skillGroup{name: r.Name}
				byName[r.Name] = g
				groups = append(groups, skillGroup{name: r.Name})
			}
			if isGlob {
				glob[r.Name] = true
			}
			if !hasRecord(g.recs, r) {
				g.recs = append(g.recs, r)
			}
		}
		if !hit {
			missing = append(missing, pat)
		}
	}
	for i := range groups {
		groups[i].recs = byName[groups[i].name].recs
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].name < groups[j].name })
	return groups, len(glob), missing, nil
}

func hasRecord(recs []state.InstalledSkill, r state.InstalledSkill) bool {
	for _, x := range recs {
		if x.Name == r.Name && x.InstallPath == r.InstallPath {
			return true
		}
	}
	return false
}

// notInstalledError is skill-not-installed, hinting at the other scope
// when the skill is recorded there.
func (a *App) notInstalledError(rt runtime, pattern, scope, root string) error {
	where := root
	if scope == "global" {
		where = "global"
	}
	has := func(sc, r string) bool {
		recs, err := a.loadScopeRecords(rt, sc, r)
		if err != nil {
			return false
		}
		gs, _, _, merr := matchSkills([]string{pattern}, recs)
		return merr == nil && len(gs) > 0
	}
	var elsewhere []string
	if scope == "global" {
		if has("project", a.startDir()) {
			elsewhere = append(elsewhere, "project")
		}
	} else {
		for _, anc := range rt.Chain {
			if anc != root && has("project", anc) {
				elsewhere = append(elsewhere, anc)
			}
		}
		if has("global", "") {
			elsewhere = append(elsewhere, "global")
		}
	}
	if len(elsewhere) > 0 {
		return fmt.Errorf("%s: %s in %s (installed in: %s)", reasonSkillNotInstalled, pattern, where, strings.Join(elsewhere, ", "))
	}
	return fmt.Errorf("%s: %s in %s", reasonSkillNotInstalled, pattern, where)
}

// confirmUnlearn guards a pattern that matched several skills: a prompt on
// a terminal, otherwise --yes is required.
func (a *App) confirmUnlearn(n int, scope, root string) error {
	if !stdinIsTerminal(a.Stdin) {
		return fmt.Errorf("%s: pattern matched %d skills; pass --yes", reasonConfirmationRequired, n)
	}
	where := root
	if scope == "global" {
		where = "global scope"
	}
	fmt.Fprintf(a.Stderr, "remove %d skills from %s? [y/N] ", n, where)
	line, _ := bufio.NewReader(a.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errors.New("aborted: nothing removed")
}

// fileIsTerminal is the stdlib character-device test; /dev/null is a
// character device but never interactive.
func fileIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok || f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
}

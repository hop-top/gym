package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	charmlog "charm.land/log/v2"
	"github.com/spf13/viper"
	kitcli "hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"

	"hop.top/gym/internal/cheat"
	"hop.top/gym/internal/config"
	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/ide"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/judge"
	"hop.top/gym/internal/project"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/upgrade"
	"hop.top/gym/internal/xdg"
)

// App handles the CLI and interactive modes.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	CWD    string

	// log is the kit-flavored logger writing to Stderr. Populated lazily
	// by Logger() so unit tests that instantiate App without a viper
	// keep working.
	log *charmlog.Logger

	// theme is the kit/cli theme used by the TUI/REPL views. Populated
	// once the kit root is built so interactive commands can re-use it.
	theme kitcli.Theme

	// noColor mirrors the global --no-color flag at the time the kit
	// root was constructed; used by markdown rendering in the REPL.
	noColor bool

	// bus is the gym event bus, lazily built once per process by
	// PersistentPreRunE. Subsequent commands reuse the same handle.
	bus *eventbus.Bus

	// cfgPaths and cfgOverrides are the extra config files and key=value
	// overrides parsed from -c/--config by PersistentPreRunE.
	cfgPaths     []string
	cfgOverrides map[string]any

	// cheatRegistryHandle is the long-lived registry used by the
	// optional GYM_TIP_BUS_BRIDGE subscriber. Lazy: built once per
	// process inside PersistentPreRunE.
	cheatRegistryHandle *cheat.Registry

	// judge is the rsx-backed coach judging subscriber. Built once
	// per process by PersistentPreRunE; subscribes to
	// gym.coach.repository.hire_requested and answers with
	// gym.coach.judgement.completed.
	judge *judge.Judge

	// guardSub is the CEL policy guard subscriber. Lazy: started
	// once per process inside PersistentPreRunE.
	guardSub *guard.Guard
}

func NewApp(stdout, stderr io.Writer, cwd string) *App {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return &App{Stdout: stdout, Stderr: stderr, Stdin: os.Stdin, CWD: cwd}
}

// Logger returns a kit-themed logger that respects --quiet and --no-color.
// Lazy: viper.Get* on the global instance only after kit/cli has bound
// the persistent flags.
func (a *App) Logger() *charmlog.Logger {
	if a.log == nil {
		l := kitlog.New(viper.GetViper())
		l.SetOutput(a.Stderr)
		a.log = l
	}
	return a.log
}

// Bus returns the gym event bus. Returns nil before PersistentPreRunE
// has had a chance to wire it. Callers that may run pre-PreRun (tests,
// helpers) should check for nil and either fall back to a no-op or
// construct a memory-adapter bus on the fly.
func (a *App) Bus() *eventbus.Bus { return a.bus }

type runtime struct {
	Dirs        xdg.Dirs
	Config      config.Config
	ProjectRoot string
	InProject   bool
}

func (a *App) loadRuntime() (runtime, error) {
	dirs, err := xdg.AppDirs(config.AppName)
	if err != nil {
		return runtime{}, err
	}
	if err := xdg.Ensure(dirs); err != nil {
		return runtime{}, err
	}
	cfg, err := config.LoadMergedConfig(a.CWD, a.cfgPaths, a.cfgOverrides)
	if err != nil {
		return runtime{}, err
	}
	root, ok := project.DetectRoot(a.CWD)
	return runtime{Dirs: dirs, Config: cfg, ProjectRoot: root, InProject: ok}, nil
}

func (a *App) printlnf(format string, args ...any) {
	fmt.Fprintf(a.Stdout, format+"\n", args...)
}

func (a *App) eprintf(format string, args ...any) {
	a.Logger().Errorf(format, args...)
}

// Run runs the gym CLI and returns shell exit code.
func (a *App) Run(args []string) int {
	if err := a.exec(args); err != nil {
		a.Logger().Error(err.Error())
		return 1
	}
	return 0
}

func (a *App) exec(args []string) error {
	return a.execCobra(args)
}

func (a *App) registryConfigFrom(cfg config.Config) registry.Config {
	repos := make([]registry.RepositorySource, 0, len(cfg.Repositories))
	for _, r := range cfg.Repositories {
		repos = append(repos, registry.RepositorySource{Name: r.Name, URL: r.URL, Curated: r.Curated})
	}
	return registry.Config{Repositories: repos, MinStarsForCurated: cfg.Curation.MinStars}
}

// runUpdate refreshes the local registry index. Kept for the legacy
// non-context callers (TUI/REPL exec path); RunE handlers should call
// runUpdateCtx so kit/console/progress events flow.
func (a *App) runUpdate() error {
	return a.runUpdateCtx(context.Background())
}

// runUpdateCtx is the context-aware variant. Emits per-phase progress
// events via kit/console/progress when a reporter is present on ctx.
func (a *App) runUpdateCtx(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}
	emitPhase(ctx, "load", "registry")
	oldIdx, err := registry.LoadIndex(rt.Dirs.DataHome)
	if err != nil {
		emitFail(ctx, "load", "registry")
		return err
	}
	emitOK(ctx, "load", "registry")
	emitPhase(ctx, "refresh", "registry")
	newIdx, report, err := registry.Refresh(
		ctx,
		a.registryConfigFrom(rt.Config),
		registry.HTTPFetcher{},
		oldIdx,
		registry.WithProgress(progressReporterFromContext(ctx)),
	)
	if err != nil {
		emitFail(ctx, "refresh", "registry")
		return err
	}
	emitOK(ctx, "refresh", "registry")
	emitPhase(ctx, "save", "registry")
	if err := registry.SaveIndex(rt.Dirs.DataHome, newIdx); err != nil {
		emitFail(ctx, "save", "registry")
		return err
	}
	emitOK(ctx, "save", "registry")

	a.printlnf("updated registry: %d skills (%d new, %d upgrades)", len(newIdx.Skills), len(report.NewSkills), len(report.Upgrades))
	for _, ns := range report.NewSkills {
		a.printlnf("  new: %s %s (%s from %s)", ns.Name, ns.Version, ns.Repository, ns.OriginURL)
	}
	for _, up := range report.Upgrades {
		a.printlnf("  upgrade available: %s %s -> %s", up.Name, up.From, up.To)
	}
	return nil
}

func isURLLike(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://") || strings.HasPrefix(s, "file://")
}

func inferNameFromToken(token string) string {
	b := filepath.Base(token)
	b = strings.TrimSuffix(b, ".git")
	if b == "" || b == "." || b == string(filepath.Separator) {
		return "skill"
	}
	return b
}

// runLearn is the legacy non-context variant kept for the TUI exec path.
// Cobra RunE handlers should call runLearnCtx so progress events flow.
func (a *App) runLearn(toGlobal bool, pathHint string, agentPatterns []string, tokens []string) error {
	return a.runLearnCtx(context.Background(), toGlobal, pathHint, agentPatterns, tokens)
}

// runLearnCtx installs each token, emitting per-phase progress events
// ("resolve", "install") via kit/console/progress.
func (a *App) runLearnCtx(ctx context.Context, toGlobal bool, pathHint string, agentPatterns []string, tokens []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(tokens) == 0 {
		return errors.New("learn requires at least one skill name or URL")
	}

	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}
	idx, err := registry.LoadIndex(rt.Dirs.DataHome)
	if err != nil {
		return err
	}

	scope := "project"
	if toGlobal {
		scope = "global"
	}

	globalSkills, err := state.LoadGlobal(rt.Dirs.StateHome)
	if err != nil {
		return err
	}
	projectSkills := []state.InstalledSkill(nil)
	projectRoot := a.CWD
	if scope == "project" {
		projectSkills, err = state.LoadProject(projectRoot)
		if err != nil {
			return err
		}
	}

	total := int64(len(tokens))
	for i, tok := range tokens {
		emitPhaseN(ctx, "resolve", tok, int64(i+1), total)
		entry := registry.SkillEntry{}
		if isURLLike(tok) || isExistingDir(tok) {
			entry = registry.SkillEntry{Name: inferNameFromToken(tok), SourceURL: tok, Version: "0.0.0"}
		} else {
			resolved, ok := idx.Find(tok)
			if !ok {
				emitFail(ctx, "resolve", tok)
				return fmt.Errorf("skill %q not found in local registry; run `gym update` or use `gym learn` with a URL", tok)
			}
			entry = resolved
		}
		emitOK(ctx, "resolve", tok)

		emitPhaseN(ctx, "install", tok, int64(i+1), total)
		installedSet, err := install.InstallSkillMulti(ctx, install.Request{
			Entry:         entry,
			Source:        entry.SourceURL,
			Scope:         scope,
			PathHint:      pathHint,
			AgentPatterns: agentPatterns,
			Config:        rt.Config,
			ProjectRoot:   projectRoot,
			CWD:           a.CWD,
			Gate:          a.installGate(),
		})
		if err != nil {
			emitFail(ctx, "install", tok)
			return err
		}
		emitOK(ctx, "install", tok)
		for _, installed := range installedSet {
			a.printlnf("installed %s %s -> %s", installed.Name, installed.Version, installed.InstallPath)
			if scope == "global" {
				globalSkills = state.Upsert(globalSkills, installed)
			} else {
				projectSkills = state.Upsert(projectSkills, installed)
			}
		}

	}

	if scope == "global" {
		return state.SaveGlobal(rt.Dirs.StateHome, globalSkills)
	}
	return state.SaveProject(projectRoot, projectSkills)
}

func isExistingDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// runTrain is the legacy non-context entry point.
func (a *App) runTrain(noGlobal bool, patterns []string) error {
	return a.runTrainCtx(context.Background(), noGlobal, patterns)
}

// runTrainCtx is the context-aware variant. Emits per-skill progress
// events ("plan", "upgrade") via kit/console/progress.
func (a *App) runTrainCtx(ctx context.Context, noGlobal bool, patterns []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}
	idx, err := registry.LoadIndex(rt.Dirs.DataHome)
	if err != nil {
		return err
	}
	globalSkills, err := state.LoadGlobal(rt.Dirs.StateHome)
	if err != nil {
		return err
	}
	projectSkills := []state.InstalledSkill{}
	inProjectWithSkills := false
	projectRoot := rt.ProjectRoot
	if rt.InProject {
		projectSkills, err = state.LoadProject(projectRoot)
		if err != nil {
			return err
		}
		inProjectWithSkills = len(projectSkills) > 0
	}

	emitPhase(ctx, "plan", "train")
	plan := upgrade.Plan(upgrade.Input{
		InProjectWithSkills: inProjectWithSkills,
		MirrorGlobal:        rt.Config.Upgrade.MirrorGlobalFromProject,
		NoGlobal:            noGlobal,
		Patterns:            patterns,
		ProjectSkills:       projectSkills,
		GlobalSkills:        globalSkills,
	})
	emitOK(ctx, "plan", "train")
	if len(plan) == 0 {
		a.printlnf("no matching installed skills to train")
		return nil
	}

	// Gate the whole upgrade plan via the bus before mutating
	// any filesystem. The guard publishes
	// policy.guard.install.evaluated; we block on it.
	if err := a.gateUpgradePlan(ctx, plan); err != nil {
		return err
	}

	total := int64(len(plan))
	changedGlobal := false
	changedProject := false
	upgradedCount := 0
	for i, item := range plan {
		emitPhaseN(ctx, "upgrade", item.Skill.Name, int64(i+1), total)
		entry, ok := idx.Find(item.Skill.Name)
		if !ok {
			emitFail(ctx, "upgrade", item.Skill.Name)
			a.printlnf("skip %s (%s): not found in current registry index", item.Skill.Name, item.Scope)
			continue
		}
		if !registry.NewerThan(entry.Version, item.Skill.Version) {
			emitOK(ctx, "upgrade", item.Skill.Name)
			continue
		}
		pathHint := filepath.Dir(item.Skill.InstallPath)
		installed, err := install.InstallSkill(ctx, install.Request{
			Entry:       entry,
			Source:      entry.SourceURL,
			Scope:       item.Scope,
			PathHint:    pathHint,
			Config:      rt.Config,
			ProjectRoot: projectRoot,
			CWD:         a.CWD,
			// Per-skill gate is skipped here because gateUpgradePlan
			// already evaluated the full batch above.
		})
		if err != nil {
			emitFail(ctx, "upgrade", item.Skill.Name)
			return err
		}
		emitOK(ctx, "upgrade", item.Skill.Name)
		upgradedCount++
		a.printlnf("upgraded %s (%s): %s -> %s", item.Skill.Name, item.Scope, item.Skill.Version, installed.Version)

		if item.Scope == "global" {
			globalSkills = state.Upsert(globalSkills, installed)
			changedGlobal = true
		} else {
			projectSkills = state.Upsert(projectSkills, installed)
			changedProject = true
		}
	}

	if changedGlobal {
		if err := state.SaveGlobal(rt.Dirs.StateHome, globalSkills); err != nil {
			return err
		}
	}
	if changedProject {
		if projectRoot == "" {
			projectRoot = a.CWD
		}
		if err := state.SaveProject(projectRoot, projectSkills); err != nil {
			return err
		}
	}
	if upgradedCount == 0 {
		a.printlnf("no upgrades available")
	}
	return nil
}

func (a *App) runPathsList() error {
	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}
	if len(rt.Config.InstallPaths) > 0 {
		a.printlnf("configured install paths:")
		for _, p := range rt.Config.InstallPaths {
			a.printlnf("  - %s [%s] %s", p.Name, p.Scope, p.Path)
		}
	} else {
		a.printlnf("configured install paths: none")
	}

	home := os.Getenv("HOME")
	existing := ide.DetectExistingGlobalPaths(home)
	a.printlnf("detected IDE/TDE global paths:")
	if len(existing) == 0 {
		a.printlnf("  - none found")
	} else {
		for _, p := range existing {
			a.printlnf("  - %s [%s] %s", p.Name, p.Scope, p.Path)
		}
	}

	a.printlnf("known standard global path options:")
	for _, p := range ide.StandardGlobalPathCandidates(home) {
		a.printlnf("  - %s [%s] %s", p.Name, p.Scope, p.Path)
	}
	return nil
}

func defaultPathName(path, scope string) string {
	b := filepath.Base(filepath.Clean(path))
	if b == "." || b == string(filepath.Separator) || b == "" {
		b = "custom"
	}
	return b + "-" + scope
}

func (a *App) runPathsAdd(scope, pathVal, name, target string) error {
	if scope != "global" && scope != "project" {
		return fmt.Errorf("scope must be global or project")
	}

	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}
	if pathVal == "" {
		if scope == "global" {
			home := os.Getenv("HOME")
			a.printlnf("choose one of these detected/known global paths with --path:")
			for _, p := range ide.DetectExistingGlobalPaths(home) {
				a.printlnf("  - %s", p.Path)
			}
			for _, p := range ide.StandardGlobalPathCandidates(home) {
				a.printlnf("  - %s", p.Path)
			}
		} else {
			root := rt.ProjectRoot
			if root == "" {
				root = a.CWD
			}
			a.printlnf("choose one of these standard project paths with --path:")
			for _, p := range ide.StandardProjectPathCandidates(root) {
				a.printlnf("  - %s", p.Path)
			}
		}
		return nil
	}
	if target == "" {
		if scope == "project" {
			target = "project"
		} else {
			target = "user"
		}
	}
	if name == "" {
		name = defaultPathName(pathVal, scope)
	}
	cfgPath, err := config.AddInstallPath(a.CWD, target, config.InstallPath{
		Name:  name,
		Path:  pathVal,
		Scope: scope,
	})
	if err != nil {
		return err
	}
	a.printlnf("added install path %s [%s] -> %s", name, scope, pathVal)
	a.printlnf("updated config: %s", cfgPath)
	return nil
}

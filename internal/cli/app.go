package cli

import (
	"context"
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
	"hop.top/gym/internal/judge"
	"hop.top/gym/internal/registry"
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
	// guardErr is why guardSub could not start (nil when it did).
	guardErr error

	// maxDataClass is the --max-data-class override.
	maxDataClass string

	// offline mirrors the global --offline flag of the running command.
	offline bool

	// scopeDir is the --dir folder of the running command, "" when unset
	// (see applyDirFlag).
	scopeDir string
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
	// Chain is the folder scope chain for the command's start folder,
	// innermost first (scope.Chain).
	Chain []string
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
		repos = append(repos, registry.RepositorySource{Name: r.Name, URL: r.URL, Curated: r.Curated, Auth: r.Auth})
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
// Cobra RunE handlers should call runLearnOpts so progress events flow.
func (a *App) runLearn(toGlobal bool, pathHint string, agentPatterns []string, tokens []string) error {
	return a.runLearnCtx(context.Background(), toGlobal, pathHint, agentPatterns, tokens)
}

// runLearnCtx installs each token with default lock behaviour.
func (a *App) runLearnCtx(ctx context.Context, toGlobal bool, pathHint string, agentPatterns []string, tokens []string) error {
	return a.runLearnOpts(ctx, learnOpts{Global: toGlobal, PathHint: pathHint, Agents: agentPatterns, Tokens: tokens})
}

func isExistingDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
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

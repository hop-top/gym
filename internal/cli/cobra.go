package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	kitcli "hop.top/kit/go/console/cli"
	kitprogress "hop.top/kit/go/console/progress"
	kitupgrade "hop.top/kit/go/core/upgrade"

	"hop.top/gym/internal/cheat"
	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/judge"
	"hop.top/gym/internal/project"
)

// notifyUpgrade is indirected so tests can stub the kit upgrade notifier.
var notifyUpgrade = func(ctx context.Context, c *kitupgrade.Checker, w interface{ Write(p []byte) (int, error) }) {
	kitupgrade.NotifyIfAvailable(ctx, c, w)
}

// newKitChecker is indirected for tests.
var newKitChecker = func(version string) *kitupgrade.Checker {
	return kitupgrade.New(
		kitupgrade.WithBinary("gym", version),
		kitupgrade.WithGitHub("hop-top/gym"),
	)
}

func (a *App) execCobra(args []string) error {
	if len(args) == 1 && (args[0] == "-v" || args[0] == "--version") {
		args = []string{"version"}
	}
	root := a.newKitRoot()
	root.SetArgs(args)
	root.Cmd.SetOut(a.Stdout)
	root.Cmd.SetErr(a.Stderr)
	// Execute bypasses kit's Root.Execute; apply its help-group handling so
	// --help-all reveals kit's hidden global flags, -c/--config included.
	root.ApplyGroupVisibility()
	return root.Cmd.Execute()
}

// newKitRoot builds the gym root command via kit/cli, registers gym's
// subcommands, and wires viper config + upgrade notifier into the
// PersistentPreRunE chain.
func (a *App) newKitRoot() *kitcli.Root {
	root := kitcli.New(kitcli.Config{
		Name:    "gym",
		Version: resolveVersion(),
		Short:   "Universal skill package manager",
		// A bare directory passed to -c/--config resolves to its project config.
		ProjectMarker: filepath.Join(project.ProjectDirName, project.ProjectConfigFile),
	})

	cmd := root.Cmd
	// Flags kit registered; anything added after this is gym's own.
	kitFlags := map[string]bool{}
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { kitFlags[f.Name] = true })

	// Gym-specific persistent flags layered on top of kit's globals
	// (--format, --quiet, --no-color, --no-hints, --offline, -V, -C,
	// --progress-format).
	cmd.PersistentFlags().String("bus", "", "event bus adapter: memory|sqlite|network (default: env-driven, else memory)")
	cmd.PersistentFlags().Bool(eventbus.StrictKey, false, "fail-closed on policy/judgement timeouts")
	cmd.PersistentFlags().Duration("gate-timeout", 30*time.Second, "max wait for sync gates (rsx judgement, policy guard)")
	cmd.PersistentFlags().Bool("refresh-judgement", false, "bypass coach judgement cache (re-run rsx)")
	cmd.PersistentFlags().Bool("insecure", false, "lower DENY verdicts to WARN (allow risky coaches/skills)")
	a.registerAdmissionFlags(cmd)

	var gymFlags []string
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !kitFlags[f.Name] {
			gymFlags = append(gymFlags, f.Name)
		}
	})

	// Per-command viper instance; kept legacy-compatible with previous flag
	// keys (learn.global, learn.path, learn.agents, train.no_global) so user
	// configs keep working.
	vp := viper.New()

	// Capture theme + no-color on the App so interactive commands can
	// reuse them without re-deriving them from viper.
	a.theme = root.Theme

	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		if err := a.applyDirFlag(c); err != nil {
			return err
		}
		if err := a.initViper(vp); err != nil {
			return err
		}
		paths, overrides, err := root.ConfigArgs()
		if err != nil {
			return err
		}
		a.cfgPaths, a.cfgOverrides = paths, overrides
		// Bind every gym persistent flag (--strict, --insecure,
		// --gate-timeout, ...) to the per-command viper and to the
		// global one, which kit/cli and guard.GateConfig read. Keys
		// are the flag names.
		for _, name := range gymFlags {
			if f := c.Flags().Lookup(name); f != nil {
				_ = vp.BindPFlag(name, f)
				_ = viper.BindPFlag(name, f)
			}
		}
		// Snapshot --no-color so REPL/markdown rendering matches the
		// global flag without re-reading viper inside Bubble Tea.
		// Read off the cobra flag (kit's own viper, not the global).
		a.noColor, _ = c.Flags().GetBool("no-color")
		// Attach a kit/console/progress reporter to the cmd context so
		// RunE handlers can fish it back out via progress.FromContext.
		// kit/cli normally wires this in its built-in PersistentPreRunE
		// chain, but gym overrides that chain here, so we re-do the
		// selection inline. Discard takes over when --quiet is set;
		// JSONL otherwise when --progress-format=json or --format=json;
		// Human is the default.
		c.SetContext(kitprogress.WithReporter(c.Context(), resolveProgressReporter(c)))
		// Lazily build the bus once per process. Subsequent commands
		// in REPL mode reuse the same handle.
		if a.bus == nil {
			busKind, _ := c.Flags().GetString("bus")
			b, err := eventbus.New(eventbus.Config{Adapter: eventbus.Adapter(strings.TrimSpace(busKind))})
			if err != nil {
				return err
			}
			a.bus = b
			// Publish the boot toolspec event with the real self-spec.
			// Walk the cobra tree and emit the toolspec.ToolSpec under
			// the existing payload struct.
			selfSpec := cheat.BuildSelfSpec(c.Root(), resolveVersion())
			_ = a.bus.Publish(c.Context(), events.TopicToolspecCLIPublished,
				events.ToolspecCLIPublishedPayload{
					Version: resolveVersion(),
					Spec:    selfSpec,
				})
			// Optional opt-in tip bridge subscriber. No-op when
			// GYM_TIP_BUS_BRIDGE is unset.
			a.cheatRegistryHandle = cheat.NewRegistry()
			a.cheatRegistryHandle.Update(selfSpec, nil)
			_ = cheat.MaybeStartBridge(a.bus, a.cheatRegistryHandle)
			// Start the rsx-backed coach judging subscriber.
			a.judge = judge.New(a.bus, a.Logger(),
				judge.WithRefreshFunc(func() bool {
					v, _ := c.Root().PersistentFlags().GetBool("refresh-judgement")
					return v
				}))
			_ = a.judge.Start(c.Context())
			// Start the CEL policy guard subscriber. Loads
			// built-in policies + user/project overrides, then
			// listens for install/upgrade requests.
			g, gerr := newGuardForApp(a)
			if gerr == nil {
				gerr = g.Start(c.Context())
			}
			if gerr != nil {
				a.guardErr = gerr
				a.Logger().Warnf("guard failed to start; installs cannot be policy-gated: %v", gerr)
			} else {
				a.guardSub = g
			}
		}
		offline, _ := c.Flags().GetBool("offline")
		if !offline && c.Name() != "version" && c.Name() != "update" {
			notifyUpgrade(c.Context(), newKitChecker(resolveVersion()), a.Stderr)
		}
		return nil
	}

	cmd.AddCommand(
		a.versionCobra(),
		a.updateCobra(),
		a.learnCobra(vp),
		a.trainCobra(vp),
		a.unlearnCobra(),
		a.listCobra(),
		a.coachCobra(),
		a.pathsCobra(),
		a.serveCobra(),
		a.tuiCobra(),
		a.replCobra(),
		a.warmupCobra(),
		a.cheatCobra(),
		a.guardCobra(),
		a.digestCobra(),
	)
	return root
}

func (a *App) versionCobra() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Print version",
		Args:    cobra.NoArgs,
		RunE:    func(_ *cobra.Command, _ []string) error { return a.cmdVersion() },
	}
}

func (a *App) updateCobra() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Refresh local registry index",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runUpdateCtx(cmd.Context()) },
	}
}

func (a *App) learnCobra(vp *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:     "learn <skill-or-url> [skill-or-url...]",
		Aliases: []string{"install"},
		Short:   "Install skills by name, URL, or local path",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			toGlobal, _ := cmd.Flags().GetBool("global")
			if !cmd.Flags().Changed("global") {
				toGlobal = vp.GetBool("learn.global")
			}
			pathHint, _ := cmd.Flags().GetString("path")
			if !cmd.Flags().Changed("path") {
				pathHint = strings.TrimSpace(vp.GetString("learn.path"))
			}
			agents, _ := cmd.Flags().GetStringArray("agent")
			if !cmd.Flags().Changed("agent") {
				agents = vp.GetStringSlice("learn.agents")
			}
			noLock, _ := cmd.Flags().GetBool("no-lock")
			return a.runLearnOpts(cmd.Context(), learnOpts{Global: toGlobal, PathHint: pathHint, Agents: agents, Tokens: args, NoLock: noLock})
		},
	}
	addDirFlag(c)
	c.Flags().BoolP("global", "g", false, "install globally (default: install into CWD/.gym)")
	c.Flags().String("path", "", "install path name/path override")
	c.Flags().StringArray("agent", nil, "limit install paths by name/path pattern (prefix with ! to exclude)")
	c.Flags().Bool("no-lock", false, "install without reading or writing the lock")
	_ = vp.BindPFlag("learn.global", c.Flags().Lookup("global"))
	_ = vp.BindPFlag("learn.path", c.Flags().Lookup("path"))
	_ = vp.BindPFlag("learn.agents", c.Flags().Lookup("agent"))
	return c
}

func (a *App) trainCobra(vp *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:     "train [pattern ...]",
		Aliases: []string{"upgrade"},
		Short:   "Upgrade installed skills",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			noGlobal, _ := cmd.Flags().GetBool("no-global")
			if !cmd.Flags().Changed("no-global") {
				noGlobal = vp.GetBool("train.no_global")
			}
			recursive, _ := cmd.Flags().GetBool("recursive")
			depth, _ := cmd.Flags().GetInt("depth")
			return a.runTrainOpts(cmd.Context(), trainOpts{NoGlobal: noGlobal, Patterns: args, Recursive: recursive, Depth: depth})
		},
	}
	addDirFlag(c)
	c.Flags().BoolP("recursive", "r", false, "also train every folder scope below the scope root")
	c.Flags().Int("depth", defaultTrainDepth, "with -r: folder levels below the scope root to search")
	c.Flags().Bool("no-global", false, "do not mirror global upgrades from project upgrades")
	_ = vp.BindPFlag("train.no_global", c.Flags().Lookup("no-global"))
	return c
}

func (a *App) coachCobra() *cobra.Command {
	coachCmd := &cobra.Command{
		Use:   "coach",
		Short: "Manage coach registry sources",
		Args:  cobra.NoArgs,
		RunE:  func(_ *cobra.Command, _ []string) error { return a.runCoachList() },
	}
	hire := &cobra.Command{
		Use:   "hire [manifest-url-or-owner/repo]",
		Short: "Add a coach source",
		// 0 args = interactive wizard; 1 arg = legacy direct form.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			interactive, _ := cmd.Flags().GetBool("interactive")
			global, _ := cmd.Flags().GetBool("global")
			name, _ := cmd.Flags().GetString("name")
			curated, _ := cmd.Flags().GetBool("curated")
			noUpdate, _ := cmd.Flags().GetBool("no-update")
			force, _ := cmd.Flags().GetBool("force")
			gateTimeout, _ := cmd.Root().PersistentFlags().GetDuration("gate-timeout")
			insecure, _ := cmd.Root().PersistentFlags().GetBool("insecure")

			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			if ref == "" || interactive {
				prompts, err := runCoachHireWizard(cmd.Context(), a)
				if err != nil {
					return err
				}
				ref = prompts.Ref
				if name == "" {
					name = prompts.Name
				}
				if !cmd.Flags().Changed("curated") {
					curated = prompts.Curated
				}
				if !cmd.Flags().Changed("global") {
					global = prompts.Global
				}
			}
			return a.runCoachHire(cmd.Context(), coachHireOptions{
				Global:      global,
				Name:        name,
				Curated:     curated,
				NoUpdate:    noUpdate,
				Force:       force,
				GateTimeout: gateTimeout,
				Insecure:    insecure,
			}, ref)
		},
	}
	hire.Flags().BoolP("global", "g", false, "write to global (user) config")
	hire.Flags().BoolP("interactive", "i", false, "prompt for missing fields via a wizard")
	hire.Flags().String("name", "", "coach name")
	hire.Flags().Bool("curated", false, "mark coach as curated")
	hire.Flags().Bool("no-update", false, "do not run gym update after hiring")
	hire.Flags().Bool("force", false, "hire even if manifest cannot be fetched/validated")

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured coach sources",
		Args:    cobra.NoArgs,
		RunE:    func(_ *cobra.Command, _ []string) error { return a.runCoachList() },
	}
	dismiss := &cobra.Command{
		Use:     "dismiss <name-or-url>",
		Aliases: []string{"rm"},
		Short:   "Remove coach source",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			global, _ := cmd.Flags().GetBool("global")
			noUpdate, _ := cmd.Flags().GetBool("no-update")
			return a.runCoachDismiss(global, noUpdate, args[0])
		},
	}
	dismiss.Flags().BoolP("global", "g", false, "dismiss from global (user) config")
	dismiss.Flags().Bool("no-update", false, "do not run gym update after dismissing")
	coachCmd.AddCommand(hire, list, dismiss)
	return coachCmd
}

func (a *App) pathsCobra() *cobra.Command {
	paths := &cobra.Command{
		Use:   "paths",
		Short: "List or add install paths",
		Args:  cobra.NoArgs,
		RunE:  func(_ *cobra.Command, _ []string) error { return a.runPathsList() },
	}
	add := &cobra.Command{
		Use:   "add",
		Short: "Add an install path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope, _ := cmd.Flags().GetString("scope")
			pathVal, _ := cmd.Flags().GetString("path")
			target, _ := cmd.Flags().GetString("target")
			name, _ := cmd.Flags().GetString("name")
			return a.runPathsAdd(scope, pathVal, name, target)
		},
	}
	add.Flags().String("name", "", "path alias name")
	add.Flags().String("scope", "global", "global|project")
	add.Flags().String("path", "", "filesystem path")
	add.Flags().String("target", "", "config target: user|project")
	paths.AddCommand(add)
	return paths
}

func (a *App) serveCobra() *cobra.Command {
	c := &cobra.Command{
		Use:   "serve",
		Short: "Serve local registry endpoints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, _ := cmd.Flags().GetString("host")
			port, _ := cmd.Flags().GetInt("port")
			daemon, _ := cmd.Flags().GetBool("daemon")
			pidFile, _ := cmd.Flags().GetString("pidfile")
			logFile, _ := cmd.Flags().GetString("logfile")
			name, _ := cmd.Flags().GetString("name")
			update, _ := cmd.Flags().GetBool("update")
			refreshEvery, _ := cmd.Flags().GetDuration("refresh")
			stop, _ := cmd.Flags().GetBool("stop")
			status, _ := cmd.Flags().GetBool("status")
			return a.runServe(serveOptions{
				Host:         host,
				Port:         port,
				Daemon:       daemon,
				PidFile:      pidFile,
				LogFile:      logFile,
				Name:         name,
				Update:       update,
				RefreshEvery: refreshEvery,
				Stop:         stop,
				Status:       status,
			})
		},
	}
	c.Flags().String("host", "127.0.0.1", "bind host")
	c.Flags().Int("port", 4873, "bind port")
	c.Flags().Bool("daemon", false, "run in background")
	c.Flags().String("pidfile", "", "pid file path")
	c.Flags().String("logfile", "", "log file path (daemon)")
	c.Flags().String("name", "gym-registry", "repository.name for /skills.json")
	c.Flags().Bool("update", false, "update registry index on start")
	c.Flags().Duration("refresh", 0, "periodically update registry index (e.g. 10m); implies --update")
	c.Flags().Bool("stop", false, "stop daemon using pidfile")
	c.Flags().Bool("status", false, "show daemon status using pidfile")
	return c
}

func (a *App) tuiCobra() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI",
		Args:  cobra.NoArgs,
		RunE:  func(_ *cobra.Command, _ []string) error { return a.cmdTUI() },
	}
}

func (a *App) replCobra() *cobra.Command {
	return &cobra.Command{
		Use:   "repl",
		Short: "Interactive REPL",
		Args:  cobra.NoArgs,
		RunE:  func(_ *cobra.Command, _ []string) error { return a.cmdREPL() },
	}
}

func (a *App) warmupCobra() *cobra.Command {
	c := &cobra.Command{
		Use:   "warmup",
		Short: "Generate lightweight skill metadata for agents",
		Long:  "Generate skill manifests for progressive disclosure (context-efficient agent startup)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, _ := cmd.Flags().GetString("format")
			scope, _ := cmd.Flags().GetString("scope")
			out, _ := cmd.Flags().GetString("output")
			noInherit, _ := cmd.Flags().GetBool("no-inherit")
			return a.runWarmup(format, scope, out, noInherit)
		},
	}
	addDirFlag(c)
	c.Flags().Bool("no-inherit", false, "only the nearest folder scope, not its ancestors")
	c.Flags().String("format", "xml", "output format: xml, json, agents-md")
	c.Flags().String("scope", "all", "skill scope: all, global, project")
	c.Flags().StringP("output", "o", "", "output file (default: stdout)")
	return c
}

func (a *App) initViper(vp *viper.Viper) error {
	vp.SetEnvPrefix("GYM")
	vp.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	vp.AutomaticEnv()

	vp.SetDefault("learn.global", false)
	vp.SetDefault("learn.path", "")
	vp.SetDefault("learn.agents", []string{})
	vp.SetDefault("train.no_global", false)

	return a.loadCLIConfig(vp)
}

// resolveProgressReporter mirrors kit/cli's selection logic. We re-do it
// here because the kit hook only runs when kit owns PersistentPreRunE,
// and gym's hook chain pre-empts that today (see the PersistentPreRunE wiring above).
//
// Selection rules (highest precedence first):
//  1. --quiet → progress.Discard()
//  2. --progress-format json → progress.JSONL(stderr)
//  3. unset --progress-format and --format json → progress.JSONL(stderr)
//  4. otherwise → progress.Human(stderr)
//
// Output goes to stderr so progress is metadata, not data.
func resolveProgressReporter(c *cobra.Command) kitprogress.Reporter {
	// Read directly off the cobra flag set — kit owns its own viper
	// instance internally, so viper.GetViper() doesn't see the parsed
	// flags. Cobra's flag values are reliable post-parse.
	quiet, _ := c.Flags().GetBool("quiet")
	if quiet {
		return kitprogress.Discard()
	}
	w := c.ErrOrStderr()
	if w == nil {
		w = os.Stderr
	}

	pf := c.Flags().Lookup("progress-format")
	progressJSON := false
	progressUserSet := pf != nil && pf.Changed
	if pf != nil {
		if val, _ := c.Flags().GetString("progress-format"); val == "json" {
			progressJSON = true
		}
	}
	// --format json inherits unless --progress-format was set explicitly.
	if !progressJSON && !progressUserSet {
		if val, _ := c.Flags().GetString("format"); val == "json" {
			progressJSON = true
		}
	}
	if progressJSON {
		return kitprogress.JSONL(w)
	}
	return kitprogress.Human(w)
}

func resolveVersion() string {
	v := strings.TrimSpace(buildVersion)
	if v == "" || v == "dev" {
		fv, _, _, _ := buildInfoFallback()
		if fv != "" {
			return fv
		}
		return "dev"
	}
	return v
}

package cli

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/xdg"
)

func (a *App) execCobra(args []string) error {
	if len(args) == 1 && (args[0] == "-v" || args[0] == "--version") {
		args = []string{"version"}
	}
	root := a.newRootCmd()
	root.SetArgs(args)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	return root.Execute()
}

func (a *App) newRootCmd() *cobra.Command {
	vp := viper.New()
	var configFile string

	root := &cobra.Command{
		Use:           "gym",
		Short:         "Universal skill package manager",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return a.initViper(vp, configFile)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&configFile, "config", "", "path to CLI config file")

	versionCmd := &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Print version",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.cmdVersion()
		},
	}

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh local registry index",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runUpdate()
		},
	}

	learnCmd := &cobra.Command{
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
			return a.runLearn(toGlobal, pathHint, agents, args)
		},
	}
	learnCmd.Flags().BoolP("global", "g", false, "install globally (default: install into CWD/.gym)")
	learnCmd.Flags().String("path", "", "install path name/path override")
	learnCmd.Flags().StringArray("agent", nil, "limit install paths by name/path pattern (prefix with ! to exclude)")
	_ = vp.BindPFlag("learn.global", learnCmd.Flags().Lookup("global"))
	_ = vp.BindPFlag("learn.path", learnCmd.Flags().Lookup("path"))
	_ = vp.BindPFlag("learn.agents", learnCmd.Flags().Lookup("agent"))

	trainCmd := &cobra.Command{
		Use:     "train [pattern ...]",
		Aliases: []string{"upgrade"},
		Short:   "Upgrade installed skills",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			noGlobal, _ := cmd.Flags().GetBool("no-global")
			if !cmd.Flags().Changed("no-global") {
				noGlobal = vp.GetBool("train.no_global")
			}
			return a.runTrain(noGlobal, args)
		},
	}
	trainCmd.Flags().Bool("no-global", false, "do not mirror global upgrades from project upgrades")
	_ = vp.BindPFlag("train.no_global", trainCmd.Flags().Lookup("no-global"))

	coachCmd := &cobra.Command{
		Use:   "coach",
		Short: "Manage coach registry sources",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runCoachList()
		},
	}
	coachHireCmd := &cobra.Command{
		Use:   "hire <manifest-url-or-owner/repo>",
		Short: "Add a coach source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			global, _ := cmd.Flags().GetBool("global")
			name, _ := cmd.Flags().GetString("name")
			curated, _ := cmd.Flags().GetBool("curated")
			noUpdate, _ := cmd.Flags().GetBool("no-update")
			force, _ := cmd.Flags().GetBool("force")
			return a.runCoachHire(global, name, curated, noUpdate, force, args[0])
		},
	}
	coachHireCmd.Flags().BoolP("global", "g", false, "write to global (user) config")
	coachHireCmd.Flags().String("name", "", "coach name")
	coachHireCmd.Flags().Bool("curated", false, "mark coach as curated")
	coachHireCmd.Flags().Bool("no-update", false, "do not run gym update after hiring")
	coachHireCmd.Flags().Bool("force", false, "hire even if manifest cannot be fetched/validated")
	coachListCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured coach sources",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runCoachList()
		},
	}
	coachDismissCmd := &cobra.Command{
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
	coachDismissCmd.Flags().BoolP("global", "g", false, "dismiss from global (user) config")
	coachDismissCmd.Flags().Bool("no-update", false, "do not run gym update after dismissing")
	coachCmd.AddCommand(coachHireCmd, coachListCmd, coachDismissCmd)

	pathsCmd := &cobra.Command{
		Use:   "paths",
		Short: "List or add install paths",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runPathsList()
		},
	}
	pathsAddCmd := &cobra.Command{
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
	pathsAddCmd.Flags().String("name", "", "path alias name")
	pathsAddCmd.Flags().String("scope", "global", "global|project")
	pathsAddCmd.Flags().String("path", "", "filesystem path")
	pathsAddCmd.Flags().String("target", "", "config target: user|project")
	pathsCmd.AddCommand(pathsAddCmd)

	serveCmd := &cobra.Command{
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
	serveCmd.Flags().String("host", "127.0.0.1", "bind host")
	serveCmd.Flags().Int("port", 4873, "bind port")
	serveCmd.Flags().Bool("daemon", false, "run in background")
	serveCmd.Flags().String("pidfile", "", "pid file path")
	serveCmd.Flags().String("logfile", "", "log file path (daemon)")
	serveCmd.Flags().String("name", "gym-registry", "repository.name for /skills.json")
	serveCmd.Flags().Bool("update", false, "update registry index on start")
	serveCmd.Flags().Duration("refresh", 0, "periodically update registry index (e.g. 10m); implies --update")
	serveCmd.Flags().Bool("stop", false, "stop daemon using pidfile")
	serveCmd.Flags().Bool("status", false, "show daemon status using pidfile")

	tuiCmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.cmdTUI()
		},
	}

	replCmd := &cobra.Command{
		Use:   "repl",
		Short: "Interactive REPL",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.cmdREPL()
		},
	}

	warmupCmd := &cobra.Command{
		Use:   "warmup",
		Short: "Generate lightweight skill metadata for agents",
		Long:  "Generate skill manifests for progressive disclosure (context-efficient agent startup)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, _ := cmd.Flags().GetString("format")
			scope, _ := cmd.Flags().GetString("scope")
			output, _ := cmd.Flags().GetString("output")
			return a.runWarmup(format, scope, output)
		},
	}
	warmupCmd.Flags().String("format", "xml", "output format: xml, json, agents-md")
	warmupCmd.Flags().String("scope", "all", "skill scope: all, global, project")
	warmupCmd.Flags().StringP("output", "o", "", "output file (default: stdout)")

	root.AddCommand(
		versionCmd,
		updateCmd,
		learnCmd,
		trainCmd,
		coachCmd,
		pathsCmd,
		serveCmd,
		tuiCmd,
		replCmd,
		warmupCmd,
	)
	return root
}

func (a *App) initViper(vp *viper.Viper, configFile string) error {
	vp.SetEnvPrefix("GYM")
	vp.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	vp.AutomaticEnv()

	vp.SetDefault("learn.global", false)
	vp.SetDefault("learn.path", "")
	vp.SetDefault("learn.agents", []string{})
	vp.SetDefault("train.no_global", false)

	if strings.TrimSpace(configFile) != "" {
		vp.SetConfigFile(configFile)
	} else {
		vp.SetConfigName("cli")
		vp.SetConfigType("yaml")
		vp.AddConfigPath(filepath.Join(a.CWD, ".gym"))
		if dirs, err := xdg.AppDirs(config.AppName); err == nil {
			vp.AddConfigPath(dirs.ConfigHome)
		}
	}

	if err := vp.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return err
	}
	return nil
}

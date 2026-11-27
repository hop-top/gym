package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/project"
	"hop.top/gym/internal/scope"
	"hop.top/gym/internal/xdg"
)

// errDirGlobal is the error for --dir together with -g/--global.
var errDirGlobal = errors.New("--dir and --global are mutually exclusive")

// addDirFlag registers --dir on a command that works on one folder scope.
func addDirFlag(c *cobra.Command) {
	c.Flags().String("dir", "", "scope root folder (default: see command)")
}

// applyDirFlag reads --dir for the command about to run. The folder must
// exist and be a directory, and cannot be combined with an explicit
// -g/--global. The result is kept on the App; commands without --dir
// clear it so a long-lived App (REPL) never carries one over.
func (a *App) applyDirFlag(c *cobra.Command) error {
	a.scopeDir = ""
	f := c.Flags().Lookup("dir")
	if f == nil || !f.Changed {
		return nil
	}
	typed := f.Value.String()
	abs := typed
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(a.CWD, abs)
	}
	abs = filepath.Clean(abs)
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return fmt.Errorf("--dir %s: not a directory", typed)
	}
	if g := c.Flags().Lookup("global"); g != nil && g.Changed && g.Value.String() == "true" {
		return errDirGlobal
	}
	a.scopeDir = abs
	return nil
}

// startDir is the folder scope resolution starts from: --dir, else CWD.
func (a *App) startDir() string {
	if a.scopeDir != "" {
		return a.scopeDir
	}
	return a.CWD
}

// loadRuntime loads the runtime for the command's default scope: the
// --dir folder when given, else the nearest scope root at or above CWD.
func (a *App) loadRuntime() (runtime, error) {
	return a.loadScopeAt(a.startDir(), a.scopeDir != "")
}

// loadScopeAt loads config and the scope chain for start. With exact, the
// scope root is start itself, whether or not it holds a .gym yet (a folder
// scope about to be created, or chosen with --dir). Otherwise it is the
// nearest trusted root at or above start; none means no project scope.
func (a *App) loadScopeAt(start string, exact bool) (runtime, error) {
	dirs, err := xdg.AppDirs(config.AppName)
	if err != nil {
		return runtime{}, err
	}
	if err := xdg.Ensure(dirs); err != nil {
		return runtime{}, err
	}
	res := scope.Chain(start)
	for _, w := range res.Warnings {
		a.Logger().Warn(w)
	}
	root, inProject := "", false
	switch {
	case exact:
		root, inProject = start, true
	case len(res.Roots) > 0:
		root, inProject = res.Roots[0], true
	}
	anchor := root
	if anchor == "" {
		anchor = start
	}
	cfg, warns, err := config.LoadScoped(anchor, res.Roots, a.cfgPaths, a.cfgOverrides)
	if err != nil {
		return runtime{}, err
	}
	for _, w := range warns {
		a.Logger().Warn(w)
	}
	a.applyAdmissionFlags(&cfg)
	a.applyNetwork(&cfg)
	return runtime{Dirs: dirs, Config: cfg, ProjectRoot: root, InProject: inProject, Chain: res.Roots}, nil
}

// noteNewFolderScope tells the operator, on stderr, that `learn` is about
// to create a scope below an existing one. Only for the default root
// (CWD): --dir is a deliberate choice.
func (a *App) noteNewFolderScope(rt runtime) {
	if a.scopeDir != "" || project.IsRoot(a.CWD) || len(rt.Chain) == 0 {
		return
	}
	fmt.Fprintf(a.Stderr, "note: installing into new folder scope %s; nearest existing scope is %s (--dir %s to use it)\n",
		a.CWD, rt.Chain[0], rt.Chain[0])
}

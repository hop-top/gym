package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/ide"
	"hop.top/gym/internal/install"
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
	CWD    string
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
	return &App{Stdout: stdout, Stderr: stderr, CWD: cwd}
}

type runtime struct {
	Dirs        xdg.Dirs
	Config      config.Config
	Source      config.Source
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
	cfg, src, err := config.LoadMergedConfig(a.CWD)
	if err != nil {
		return runtime{}, err
	}
	root, ok := project.DetectRoot(a.CWD)
	return runtime{Dirs: dirs, Config: cfg, Source: src, ProjectRoot: root, InProject: ok}, nil
}

func (a *App) printlnf(format string, args ...any) {
	fmt.Fprintf(a.Stdout, format+"\n", args...)
}

func (a *App) eprintf(format string, args ...any) {
	fmt.Fprintf(a.Stderr, format+"\n", args...)
}

// Run executes the app and returns shell exit code.
func (a *App) Run(args []string) int {
	if err := a.exec(args); err != nil {
		a.eprintf("error: %v", err)
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

func (a *App) runUpdate() error {
	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}
	oldIdx, err := registry.LoadIndex(rt.Dirs.DataHome)
	if err != nil {
		return err
	}
	newIdx, report, err := registry.Refresh(context.Background(), a.registryConfigFrom(rt.Config), registry.HTTPFetcher{}, oldIdx)
	if err != nil {
		return err
	}
	if err := registry.SaveIndex(rt.Dirs.DataHome, newIdx); err != nil {
		return err
	}

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

func (a *App) runLearn(toGlobal bool, pathHint string, agentPatterns []string, tokens []string) error {
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

	for _, tok := range tokens {
		entry := registry.SkillEntry{}
		if isURLLike(tok) || isExistingDir(tok) {
			entry = registry.SkillEntry{Name: inferNameFromToken(tok), SourceURL: tok, Version: "0.0.0"}
		} else {
			resolved, ok := idx.Find(tok)
			if !ok {
				return fmt.Errorf("skill %q not found in local registry; run `gym update` or use `gym learn` with a URL", tok)
			}
			entry = resolved
		}

		installedSet, err := install.InstallSkillMulti(context.Background(), install.Request{
			Entry:         entry,
			Source:        entry.SourceURL,
			Scope:         scope,
			PathHint:      pathHint,
			AgentPatterns: agentPatterns,
			Config:        rt.Config,
			ProjectRoot:   projectRoot,
			CWD:           a.CWD,
		})
		if err != nil {
			return err
		}
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

func (a *App) runTrain(noGlobal bool, patterns []string) error {
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

	plan := upgrade.Plan(upgrade.Input{
		InProjectWithSkills: inProjectWithSkills,
		MirrorGlobal:        rt.Config.Upgrade.MirrorGlobalFromProject,
		NoGlobal:            noGlobal,
		Patterns:            patterns,
		ProjectSkills:       projectSkills,
		GlobalSkills:        globalSkills,
	})
	if len(plan) == 0 {
		a.printlnf("no matching installed skills to train")
		return nil
	}

	changedGlobal := false
	changedProject := false
	upgradedCount := 0
	for _, item := range plan {
		entry, ok := idx.Find(item.Skill.Name)
		if !ok {
			a.printlnf("skip %s (%s): not found in current registry index", item.Skill.Name, item.Scope)
			continue
		}
		if !registry.NewerThan(entry.Version, item.Skill.Version) {
			continue
		}
		pathHint := filepath.Dir(item.Skill.InstallPath)
		installed, err := install.InstallSkill(context.Background(), install.Request{
			Entry:       entry,
			Source:      entry.SourceURL,
			Scope:       item.Scope,
			PathHint:    pathHint,
			Config:      rt.Config,
			ProjectRoot: projectRoot,
			CWD:         a.CWD,
		})
		if err != nil {
			return err
		}
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

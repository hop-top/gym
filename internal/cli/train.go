package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/project"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/upgrade"
)

// defaultTrainDepth is how far below the scope root `train --recursive`
// looks for nested scopes unless --depth says otherwise.
const defaultTrainDepth = 8

// trainOpts are the options of one `gym train` run.
type trainOpts struct {
	NoGlobal bool
	Patterns []string
	// Recursive also trains every scope found below the scope root.
	Recursive bool
	// Depth bounds the recursive walk, in folder levels below the root.
	Depth int
	// Frozen refuses any upgrade (lock-frozen); config lock.frozen does too.
	Frozen bool
}

// runTrain is the legacy non-context entry point.
func (a *App) runTrain(noGlobal bool, patterns []string) error {
	return a.runTrainCtx(context.Background(), noGlobal, patterns)
}

// runTrainCtx is the context-aware variant. Emits per-skill progress
// events ("plan", "upgrade") via kit/console/progress.
func (a *App) runTrainCtx(ctx context.Context, noGlobal bool, patterns []string) error {
	return a.runTrainOpts(ctx, trainOpts{NoGlobal: noGlobal, Patterns: patterns, Depth: defaultTrainDepth})
}

// runTrainOpts trains the command's scope: --dir, else the nearest scope
// root at or above CWD. With Recursive the scopes below that root follow,
// each planned and run on its own in path order; a failing scope does not
// stop the rest and the run fails at the end.
func (a *App) runTrainOpts(ctx context.Context, o trainOpts) error {
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
	if !o.Recursive {
		return a.trainScope(ctx, rt, idx, o)
	}

	base := rt.ProjectRoot
	if !rt.InProject {
		base = a.startDir()
	}
	roots := findScopeRoots(base, o.Depth)
	if rt.InProject {
		roots = append([]string{base}, roots...)
	}
	if len(roots) == 0 {
		// No scope anywhere: train the global state, as without -r.
		return a.trainScope(ctx, rt, idx, o)
	}

	failed := 0
	for _, root := range roots {
		a.printlnf("scope %s", root)
		srt := rt
		if root != base || !rt.InProject {
			if srt, err = a.loadScopeAt(root, true); err != nil {
				a.eprintf("scope %s: %v", root, err)
				failed++
				continue
			}
		}
		if err := a.trainScope(ctx, srt, idx, o); err != nil {
			a.eprintf("scope %s: %v", root, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("train: %d of %d scopes failed", failed, len(roots))
	}
	return nil
}

// findScopeRoots lists, in path order, the folders strictly below base that
// hold a .gym/installed.json, at most depth levels down. It does not follow
// symlinks and skips .git, node_modules and .gym itself.
func findScopeRoots(base string, depth int) []string {
	var out []string
	baseDepth := strings.Count(filepath.Clean(base), string(filepath.Separator))
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", "node_modules", project.ProjectDirName:
			return fs.SkipDir
		}
		if p == base {
			return nil
		}
		if strings.Count(p, string(filepath.Separator))-baseDepth > depth {
			return fs.SkipDir
		}
		if st, err := os.Stat(filepath.Join(p, project.ProjectDirName, project.ProjectStateFile)); err == nil && !st.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// trainScope plans and runs the upgrades of one scope (rt.ProjectRoot) plus
// the global mirror.
func (a *App) trainScope(ctx context.Context, rt runtime, idx registry.Index, o trainOpts) error {
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
		NoGlobal:            o.NoGlobal,
		Patterns:            o.Patterns,
		ProjectRoot:         projectRoot,
		ProjectSkills:       projectSkills,
		GlobalSkills:        globalSkills,
	})
	emitOK(ctx, "plan", "train")
	plan, refusedErr := a.trustedPlan(ctx, plan, rt, projectRoot)
	locks, err := a.loadTrainLocks(rt, planScopes(plan, projectRoot))
	if err != nil {
		return err
	}
	plan, skipped := a.skipPinned(plan, locks)
	if o.Frozen || rt.Config.Lock.Frozen {
		if err := frozenTrainCheck(plan, idx, locks); err != nil {
			return err
		}
	}
	if len(plan) == 0 {
		if refusedErr == nil && skipped == 0 {
			a.printlnf("no matching installed skills to train")
		}
		return refusedErr
	}

	// Gate the whole upgrade plan via the bus before mutating
	// any filesystem. The guard publishes
	// policy.guard.install.evaluated; we block on it.
	if err := a.gateUpgradePlan(ctx, plan, projectRoot); err != nil {
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
		reqID := newRequestID()
		var outcome install.Outcome
		installed, err := install.InstallSkill(ctx, install.Request{
			Outcome:     &outcome,
			Kind:        lock.KindRegistry,
			Entry:       entry,
			Source:      entry.SourceURL,
			Scope:       item.Scope,
			PathHint:    pathHint,
			Config:      rt.Config,
			ProjectRoot: projectRoot,
			CWD:         a.CWD,
			// The plan gate above sees skill/scope/from/source only; the
			// per-skill gate sees the content-derived admission facts.
			Gate:              a.installGateFor(reqID, item.Root, projectRoot),
			PreviousDataClass: item.Skill.DataClass,
		})
		if err != nil {
			emitFail(ctx, "upgrade", item.Skill.Name)
			a.publishInstallRejected(ctx, item.Skill.Name, item.Scope, reqID, err)
			return err
		}
		emitOK(ctx, "upgrade", item.Skill.Name)
		upgradedCount++
		le := a.lockEntryFor(locks.locs[item.Scope],
			learnTarget{Entry: entry, Kind: lock.KindRegistry}, installed, outcome.Commit)
		if a.notLockable(locks.locs[item.Scope], le) {
			locks.drop(item.Scope, le.Name)
		} else {
			locks.replace(item.Scope, le)
		}
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
	if err := locks.save(); err != nil {
		return err
	}
	if upgradedCount == 0 {
		a.printlnf("no upgrades available")
	}
	return refusedErr
}

package cli

import (
	"context"
	"errors"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
)

// learnOpts are the options of one `gym learn` run.
type learnOpts struct {
	Global   bool
	PathHint string
	Agents   []string
	Tokens   []string
	// NoLock installs without reading or writing the lock.
	NoLock bool
}

// runLearnOpts installs each token, emitting per-phase progress events
// ("resolve", "install") via kit/console/progress, then records what was
// installed in the scope's lock.
func (a *App) runLearnOpts(ctx context.Context, o learnOpts) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(o.Tokens) == 0 {
		return errors.New("learn requires at least one skill name or URL")
	}

	if o.Global && a.scopeDir != "" {
		return errDirGlobal
	}
	rt, err := a.loadScopeAt(a.startDir(), !o.Global)
	if err != nil {
		return err
	}
	idx, err := registry.LoadIndex(rt.Dirs.DataHome)
	if err != nil {
		return err
	}

	scope := "project"
	if o.Global {
		scope = "global"
	}

	globalSkills, err := state.LoadGlobal(rt.Dirs.StateHome)
	if err != nil {
		return err
	}
	projectSkills := []state.InstalledSkill(nil)
	projectRoot := a.startDir()
	if scope == "project" {
		a.noteNewFolderScope(rt)
		projectSkills, err = state.LoadProject(projectRoot)
		if err != nil {
			return err
		}
	}
	sl := a.scopeLockOf(rt, scope, projectRoot)
	if !o.NoLock {
		// A corrupt or hostile lock fails before anything is installed.
		if _, err := sl.load(); err != nil {
			return err
		}
	}

	var locked []lock.Entry
	total := int64(len(o.Tokens))
	for i, tok := range o.Tokens {
		emitPhaseN(ctx, "resolve", tok, int64(i+1), total)
		target, err := resolveLearnToken(tok, idx)
		if err != nil {
			emitFail(ctx, "resolve", tok)
			return err
		}
		entry := target.Entry
		emitOK(ctx, "resolve", tok)

		emitPhaseN(ctx, "install", tok, int64(i+1), total)
		reqID := newRequestID()
		var outcome install.Outcome
		installedSet, err := install.InstallSkillMulti(ctx, install.Request{
			Entry:         entry,
			Source:        entry.SourceURL,
			Ref:           target.Ref,
			Scope:         scope,
			PathHint:      o.PathHint,
			AgentPatterns: o.Agents,
			Config:        rt.Config,
			ProjectRoot:   projectRoot,
			CWD:           a.CWD,
			Gate:          a.installGate(reqID, projectRoot),
			Outcome:       &outcome,
		})
		if err != nil {
			emitFail(ctx, "install", tok)
			a.publishInstallRejected(ctx, entry.Name, scope, reqID, err)
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
		if len(installedSet) > 0 {
			locked = append(locked, a.lockEntryFor(sl, target, installedSet[0], outcome.Commit))
		}
	}

	if scope == "global" {
		err = state.SaveGlobal(rt.Dirs.StateHome, globalSkills)
	} else {
		err = state.SaveProject(projectRoot, projectSkills)
	}
	if err != nil {
		return err
	}
	// The lock follows the install dirs and the state: a crash leaves
	// drift that `list --check` reports, never a lock entry for a tree that
	// was not installed.
	return a.recordLock(sl, locked, o.NoLock)
}

package cli

import (
	"context"
	"errors"
	"fmt"

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
	// Frozen verifies the lock instead of changing anything.
	Frozen bool
	// Digest is the operator's own pin for the single token.
	Digest string
}

// learnScope is the scope one learn run works on.
type learnScope struct {
	rt    runtime
	scope string
	// root is the scope root: --dir or the current directory, also for the
	// global scope (it only feeds the install gate there).
	root string
	sl   scopeLock
}

// runLearnOpts is `gym learn`:
//
//	learn <tok>...   install each token, then record it in the scope's lock
//	learn            restore every lock entry that is missing or drifted
//	learn --frozen   verify the lock; change nothing
func (a *App) runLearnOpts(ctx context.Context, o learnOpts) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if o.Global && a.scopeDir != "" {
		return errDirGlobal
	}
	if o.Digest != "" {
		if !install.ValidDigest(o.Digest) {
			return fmt.Errorf("invalid --digest %q (want sha256:<64 lowercase hex>)", o.Digest)
		}
		if len(o.Tokens) != 1 {
			return errors.New("--digest pins one skill: pass exactly one skill or URL")
		}
	}
	if len(o.Tokens) == 0 && o.NoLock {
		return errors.New("learn --no-lock needs a skill or URL to install")
	}
	rt, err := a.loadScopeAt(a.startDir(), !o.Global)
	if err != nil {
		return err
	}
	scope := "project"
	if o.Global {
		scope = "global"
	}
	ls := learnScope{rt: rt, scope: scope, root: a.startDir()}
	ls.sl = a.scopeLockOf(rt, scope, ls.root)

	frozen := o.Frozen || rt.Config.Lock.Frozen
	if frozen && (o.NoLock || o.Digest != "") {
		return fmt.Errorf("%s: learn would modify %s", reasonLockFrozen, ls.sl.Path)
	}
	if len(o.Tokens) == 0 {
		if frozen {
			return a.learnVerify(ctx, ls)
		}
		return a.learnRestore(ctx, o, ls)
	}
	return a.learnTokens(ctx, o, ls, frozen)
}

// learnTokens installs each token, emitting per-phase progress events
// ("resolve", "install") via kit/console/progress, then records what was
// installed in the scope's lock.
func (a *App) learnTokens(ctx context.Context, o learnOpts, ls learnScope, frozen bool) error {
	rt, scope, projectRoot, sl := ls.rt, ls.scope, ls.root, ls.sl
	idx, err := registry.LoadIndex(rt.Dirs.DataHome)
	if err != nil {
		return err
	}
	var lf *lock.File
	if !o.NoLock {
		// A corrupt or hostile lock fails before anything is installed.
		if lf, err = sl.load(); err != nil {
			return err
		}
	}
	if frozen {
		// A frozen lock never changes: only entries it already holds may be
		// (re)installed, by bare name.
		for _, tok := range o.Tokens {
			if _, ok := lf.Find(tok); ok && isBareName(tok) {
				continue
			}
			if outOfTreeDir(sl, scope, tok) {
				// Never locked, so no lock change is attempted, unless an
				// entry of that name would have to go.
				if _, ok := lf.Find(inferNameFromToken(tok)); !ok {
					continue
				}
			}
			return fmt.Errorf("%s: learn %s would modify %s", reasonLockFrozen, tok, sl.Path)
		}
	}

	globalSkills, err := state.LoadGlobal(rt.Dirs.StateHome)
	if err != nil {
		return err
	}
	projectSkills := []state.InstalledSkill(nil)
	if scope == "project" {
		a.noteNewFolderScope(rt)
		projectSkills, err = state.LoadProject(projectRoot)
		if err != nil {
			return err
		}
	}

	var locked []lock.Entry
	total := int64(len(o.Tokens))
	for i, tok := range o.Tokens {
		emitPhaseN(ctx, "resolve", tok, int64(i+1), total)
		target, honoured, err := a.resolveToken(ls, idx, lf, tok, o.Digest != "")
		if err != nil {
			emitFail(ctx, "resolve", tok)
			a.publishInstallRejected(ctx, target.Entry.Name, scope, newRequestID(), err)
			return err
		}
		entry := target.Entry
		emitOK(ctx, "resolve", tok)
		if honoured != nil {
			a.printlnf("using locked %s (gym train to upgrade)", honoured.Version)
		}

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
			Kind:          target.Kind,
			Requested:     target.Requested,
			Lock:          honoured,
			ExpectDigest:  o.Digest,
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
		if len(installedSet) > 0 && honoured == nil {
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

// resolveToken turns one token into an install target. A bare skill name
// the lock holds resolves to that entry (honoured, non-nil): the locked
// source at the locked commit, expecting the locked digest. Anything else
// is a new request resolved from the registry or the source; with an
// operator --digest a lock entry is not consulted.
func (a *App) resolveToken(ls learnScope, idx registry.Index, lf *lock.File, tok string, operatorPin bool) (learnTarget, *lock.Entry, error) {
	if lf != nil && !operatorPin && isBareName(tok) {
		if e, ok := lf.Find(tok); ok {
			t, err := lockedTarget(ls, idx, e)
			if err != nil {
				return learnTarget{Entry: registry.SkillEntry{Name: e.Name}}, nil, err
			}
			return t, &e, nil
		}
	}
	t, err := resolveLearnToken(tok, idx)
	return t, nil, err
}

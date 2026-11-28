package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/project"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/scope"
	"hop.top/gym/internal/state"
)

// reasonLockFrozen is the error of a command that would change a frozen
// lock. It is a command error: nothing is published for it.
const reasonLockFrozen = "lock-frozen"

// lockedTarget turns a lock entry into an install target: the locked
// source (resolved against the scope root, never outside it) at the locked
// commit. For a registry entry the index's digest for the same version, if
// the index holds one, rides along so a disagreement with the lock is
// reported (the lock still wins).
func lockedTarget(ls learnScope, idx registry.Index, e lock.Entry) (learnTarget, error) {
	src, err := lock.ResolveSource(ls.sl.Base, ls.scope, e.Source)
	if err != nil {
		return learnTarget{}, err
	}
	entry := registry.SkillEntry{Name: e.Name, Version: e.Version, SourceURL: src, Path: e.Path, OriginURL: e.Registry}
	if e.Kind == lock.KindRegistry {
		if rv, ok := idx.FindVersion(e.Name, e.Version); ok {
			entry.Digest = rv.Digest
		}
	}
	t := learnTarget{Entry: entry, Requested: e.Requested, Kind: e.Kind}
	if e.Kind != lock.KindDir {
		t.Ref = e.Commit
	}
	return t, nil
}

// scopeRecords are the installed records of a scope whose install_path is
// trusted (see trustedRecordsAt); the lock checks only hash those.
func (a *App) scopeRecords(ls learnScope) ([]state.InstalledSkill, error) {
	var recs []state.InstalledSkill
	var err error
	root := ls.root
	if ls.scope == "global" {
		root = ""
		recs, err = state.LoadGlobal(ls.rt.Dirs.StateHome)
	} else {
		recs, err = state.LoadProject(root)
	}
	if err != nil {
		return nil, err
	}
	return a.trustedRecordsAt(ls.rt, recs, ls.scope, root), nil
}

// lockDrift loads the scope's lock (lock-missing when absent) and compares
// it with what is installed.
func (a *App) lockDrift(ls learnScope) (*lock.File, []install.Drift, error) {
	f, err := lock.Load(ls.sl.Path, ls.sl.Scope)
	if err != nil {
		return nil, nil, err
	}
	recs, err := a.scopeRecords(ls)
	if err != nil {
		return nil, nil, err
	}
	return f, install.LockDrift(f, recs), nil
}

func countFailing(ds []install.Drift) int {
	n := 0
	for _, d := range ds {
		if d.Fails() {
			n++
		}
	}
	return n
}

func driftError(ls learnScope, f *lock.File, ds []install.Drift) error {
	return &install.RefusedError{
		Reason: install.ReasonLockDrift,
		Detail: fmt.Sprintf("%d of %d locked skills differ from %s", countFailing(ds), len(f.Skills), ls.sl.Path),
	}
}

// learnVerify is `learn --frozen` (and a frozen config): nothing is
// installed or written; any locked skill that is missing, drifted or
// modified fails with lock-drift, one rejection event each. An untracked
// skill is only a warning.
func (a *App) learnVerify(ctx context.Context, ls learnScope) error {
	f, ds, err := a.lockDrift(ls)
	if err != nil {
		return err
	}
	for _, d := range ds {
		if !d.Fails() {
			a.Logger().Warnf("%s %s: %s", d.Kind, d.Name, d.Detail)
			continue
		}
		a.printlnf("%s %s: %s", d.Kind, d.Name, d.Detail)
		a.publishInstallRejected(ctx, d.Name, ls.scope, newRequestID(),
			&install.RefusedError{Reason: install.ReasonLockDrift, Detail: d.Kind + " " + d.Name})
	}
	if countFailing(ds) > 0 {
		return driftError(ls, f, ds)
	}
	a.printlnf("lock ok: %d locked skills match %s", len(f.Skills), ls.sl.Path)
	return nil
}

// learnRestore is `learn` with no arguments: install every lock entry that
// is missing or drifted, from the lock's own source and commit, never
// reading the registry index. One failing entry does not stop the rest; the
// run fails at the end. The lock is not rewritten: its entries are what was
// asked for.
func (a *App) learnRestore(ctx context.Context, o learnOpts, ls learnScope) error {
	f, ds, err := a.lockDrift(ls)
	if err != nil {
		return err
	}
	need := map[string]bool{}
	for _, d := range ds {
		if d.Fails() {
			need[d.Name] = true
		}
	}
	if len(need) == 0 {
		a.printlnf("nothing to restore: %d locked skills match %s", len(f.Skills), ls.sl.Path)
		return nil
	}

	var globalSkills, projectSkills []state.InstalledSkill
	if ls.scope == "global" {
		globalSkills, err = state.LoadGlobal(ls.rt.Dirs.StateHome)
	} else {
		projectSkills, err = state.LoadProject(ls.root)
	}
	if err != nil {
		return err
	}

	failed, done := 0, 0
	total := int64(len(need))
	for _, e := range f.Skills {
		if !need[e.Name] {
			continue
		}
		done++
		emitPhaseN(ctx, "install", e.Name, int64(done), total)
		e := e
		reqID := newRequestID()
		fail := func(err error) {
			emitFail(ctx, "install", e.Name)
			a.eprintf("%s: %v", e.Name, err)
			a.publishInstallRejected(ctx, e.Name, ls.scope, reqID, err)
			failed++
		}
		target, err := lockedTarget(ls, registry.Index{}, e)
		if err != nil {
			fail(err)
			continue
		}
		var outcome install.Outcome
		set, err := install.InstallSkillMulti(ctx, install.Request{
			Entry:         target.Entry,
			Source:        target.Entry.SourceURL,
			Ref:           target.Ref,
			Scope:         ls.scope,
			PathHint:      o.PathHint,
			AgentPatterns: o.Agents,
			Config:        ls.rt.Config,
			ProjectRoot:   ls.root,
			CWD:           a.CWD,
			Gate:          a.installGate(reqID, ls.root),
			Outcome:       &outcome,
			Kind:          target.Kind,
			Requested:     target.Requested,
			Lock:          &e,
		})
		if err != nil {
			fail(err)
			continue
		}
		emitOK(ctx, "install", e.Name)
		for _, installed := range set {
			a.printlnf("installed %s %s -> %s", installed.Name, installed.Version, installed.InstallPath)
			if ls.scope == "global" {
				globalSkills = state.Upsert(globalSkills, installed)
			} else {
				projectSkills = state.Upsert(projectSkills, installed)
			}
		}
	}
	if ls.scope == "global" {
		err = state.SaveGlobal(ls.rt.Dirs.StateHome, globalSkills)
	} else {
		err = state.SaveProject(ls.root, projectSkills)
	}
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("learn: %d of %d locked skills failed", failed, len(need))
	}
	return nil
}

// lockOnlyRoot is the nearest folder at or above the start folder whose
// .gym holds a lock and no scope marker: a fresh clone, where installed.json
// and skills/ are gitignored. Same walk, boundary and trust rules as the
// scope chain; folders with a marker were already weighed by that chain.
func (a *App) lockOnlyRoot() (string, bool) {
	res := scope.ChainOf(a.startDir(), func(dir string) bool {
		if project.IsRoot(dir) {
			return false
		}
		fi, err := os.Stat(lock.ProjectPath(dir))
		return err == nil && fi.Mode().IsRegular()
	})
	for _, w := range res.Warnings {
		a.Logger().Warn(w)
	}
	if len(res.Roots) == 0 {
		return "", false
	}
	return res.Roots[0], true
}

// runListCheck is `gym list --check`: the drift report of the scope
// against its lock, exit 1 on missing, digest-drift or modified.
func (a *App) runListCheck(global bool, format string) error {
	if global && a.scopeDir != "" {
		return errDirGlobal
	}
	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}
	ls := learnScope{rt: rt, scope: "project", root: rt.ProjectRoot}
	if global {
		ls.scope, ls.root = "global", a.startDir()
	} else if !rt.InProject {
		root, ok := a.lockOnlyRoot()
		if !ok {
			return fmt.Errorf("no folder scope at or above %s; use --dir or -g", a.startDir())
		}
		ls.root = root
	}
	ls.sl = a.scopeLockOf(rt, ls.scope, ls.root)
	f, ds, err := a.lockDrift(ls)
	if err != nil {
		return err
	}
	if ds == nil {
		ds = []install.Drift{}
	}
	failing := countFailing(ds)
	if strings.EqualFold(format, "json") {
		type finding struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		}
		out := struct {
			Scope    string    `json:"scope"`
			Lock     string    `json:"lock"`
			OK       bool      `json:"ok"`
			Findings []finding `json:"findings"`
		}{Scope: ls.scope, Lock: ls.sl.Path, OK: failing == 0, Findings: []finding{}}
		for _, d := range ds {
			out.Findings = append(out.Findings, finding{d.Name, d.Kind, d.Detail})
		}
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else if len(ds) == 0 {
		a.printlnf("lock ok: %d locked skills match %s", len(f.Skills), ls.sl.Path)
	} else {
		tw := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "STATUS\tNAME\tDETAIL")
		for _, d := range ds {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", d.Kind, d.Name, d.Detail)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if failing > 0 {
		return driftError(ls, f, ds)
	}
	return nil
}

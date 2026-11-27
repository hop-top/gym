package cli

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/upgrade"
)

// scopeLock locates the lock of one scope. Project and folder scopes keep
// it at <root>/.gym/lock.json; the global scope keeps it in the user config
// dir. Base is the folder a local source is stored relative to.
type scopeLock struct {
	Scope string
	Path  string
	Base  string
}

func (a *App) scopeLockOf(rt runtime, scope, root string) scopeLock {
	if scope == lock.ScopeGlobal {
		return scopeLock{Scope: scope, Path: lock.GlobalPath(rt.Dirs.ConfigHome), Base: rt.Dirs.ConfigHome}
	}
	return scopeLock{Scope: scope, Path: lock.ProjectPath(root), Base: root}
}

// load reads the lock; a missing file is an empty lock.
func (l scopeLock) load() (*lock.File, error) { return lock.LoadOrNew(l.Path, l.Scope) }

// save writes f, and keeps machine-local files out of version control for
// a project lock.
func (l scopeLock) save(f *lock.File) error {
	if l.Scope == lock.ScopeProject {
		if err := lock.EnsureGitignore(l.Base); err != nil {
			return err
		}
	}
	return lock.Save(l.Path, f)
}

// lockEntryFor is the lock entry describing a completed install. A local
// source that cannot be stored relative to the scope root is stored
// absolute with the finding lock-source-not-portable.
func (a *App) lockEntryFor(sl scopeLock, t learnTarget, installed state.InstalledSkill, commit string) lock.Entry {
	src, _ := lock.SourceForLock(sl.Base, install.RedactURL(t.Entry.SourceURL))
	e := lock.Entry{
		Name:      installed.Name,
		Kind:      t.Kind,
		Source:    src,
		Requested: t.Requested,
		Version:   installed.Version,
		Commit:    commit,
		Path:      t.Entry.Path,
		Digest:    installed.Digest,
	}
	if t.Kind == lock.KindRegistry {
		e.Registry = install.RedactURL(t.Entry.OriginURL)
	}
	return e
}

// recordLock adds or replaces entries in the scope's lock. With noLock the
// lock is left alone and an existing entry that now differs is said so on
// stderr.
func (a *App) recordLock(sl scopeLock, entries []lock.Entry, noLock bool) error {
	if len(entries) == 0 {
		return nil
	}
	f, err := sl.load()
	if err != nil {
		if noLock {
			return nil
		}
		return err
	}
	if noLock {
		for _, e := range entries {
			if _, ok := f.Find(e.Name); ok {
				fmt.Fprintf(a.Stderr, "note: --no-lock: left the %s entry in %s as is; it may no longer match what is installed\n", e.Name, sl.Path)
			}
		}
		return nil
	}
	for _, e := range entries {
		a.warnNotPortable(sl, e)
		f.Set(e)
	}
	return sl.save(f)
}

// warnNotPortable reports lock-source-not-portable for an entry whose
// local source is stored as an absolute path.
func (a *App) warnNotPortable(sl scopeLock, e lock.Entry) {
	if lock.IsLocalSource(e.Source) && filepath.IsAbs(e.Source) {
		a.Logger().Warnf("%s: %s is outside %s; the lock stores an absolute path that a project lock will not restore from", lock.FindingNotPortable, e.Source, sl.Base)
	}
}

// trainLocks holds the locks of the scopes one train run touches.
type trainLocks struct {
	locs  map[string]scopeLock
	files map[string]*lock.File
	dirty map[string]bool
}

func (a *App) loadTrainLocks(rt runtime, scopes map[string]string) (*trainLocks, error) {
	t := &trainLocks{locs: map[string]scopeLock{}, files: map[string]*lock.File{}, dirty: map[string]bool{}}
	keys := make([]string, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, scope := range keys {
		sl := a.scopeLockOf(rt, scope, scopes[scope])
		f, err := sl.load()
		if err != nil {
			return nil, err
		}
		t.locs[scope], t.files[scope] = sl, f
	}
	return t, nil
}

// pinned reports the lock entry holding skill name of scope to an explicit
// request (`learn name@version`, `url#ref`): train never moves it.
func (t *trainLocks) pinned(scope, name string) (lock.Entry, bool) {
	f := t.files[scope]
	if f == nil {
		return lock.Entry{}, false
	}
	e, ok := f.Find(name)
	return e, ok && e.Requested != ""
}

// replace swaps in the upgraded entry; a skill the lock does not track
// stays untracked.
func (t *trainLocks) replace(scope string, e lock.Entry) {
	f := t.files[scope]
	if f == nil {
		return
	}
	if _, ok := f.Find(e.Name); ok {
		f.Set(e)
		t.dirty[scope] = true
	}
}

func (t *trainLocks) save() error {
	for scope, f := range t.files {
		if t.dirty[scope] {
			if err := t.locs[scope].save(f); err != nil {
				return err
			}
		}
	}
	return nil
}

// planScopes maps each scope a plan touches to its root ("" for global),
// the locks train must read.
func planScopes(plan []upgrade.PlanItem, projectRoot string) map[string]string {
	out := map[string]string{}
	for _, it := range plan {
		if it.Scope == lock.ScopeGlobal {
			out[it.Scope] = ""
		} else {
			out[it.Scope] = projectRoot
		}
	}
	return out
}

// skipPinned drops the plan items whose lock entry was requested
// explicitly and says so; it returns the rest and how many it dropped.
func (a *App) skipPinned(plan []upgrade.PlanItem, locks *trainLocks) ([]upgrade.PlanItem, int) {
	kept := make([]upgrade.PlanItem, 0, len(plan))
	skipped := 0
	for _, it := range plan {
		if e, ok := locks.pinned(it.Scope, it.Skill.Name); ok {
			change := it.Skill.Name + "@<new>"
			if e.Kind != lock.KindRegistry {
				change = e.Source + "#<new>"
			}
			a.printlnf("skip %s: pinned to %s (gym learn %s to change)", it.Skill.Name, e.Requested, change)
			skipped++
			continue
		}
		kept = append(kept, it)
	}
	return kept, skipped
}

// lastEntry returns the lock entry of name in scope (after replace).
func (t *trainLocks) lastEntry(scope, name string) lock.Entry {
	if f := t.files[scope]; f != nil {
		e, _ := f.Find(name)
		return e
	}
	return lock.Entry{}
}

// frozenTrainCheck fails with lock-frozen when train would upgrade
// anything: a frozen lock never changes, so train is a check that exits 0
// only if nothing would move.
func frozenTrainCheck(plan []upgrade.PlanItem, idx registry.Index, locks *trainLocks) error {
	for _, it := range plan {
		if e, ok := idx.Find(it.Skill.Name); ok && registry.NewerThan(e.Version, it.Skill.Version) {
			return fmt.Errorf("%s: train would modify %s (%s %s -> %s)", reasonLockFrozen,
				locks.locs[it.Scope].Path, it.Skill.Name, it.Skill.Version, e.Version)
		}
	}
	return nil
}

// addLearnLockFlags registers the lock-related flags of `learn`.
func addLearnLockFlags(c *cobra.Command) {
	c.Flags().Bool("no-lock", false, "install without reading or writing the lock")
	c.Flags().Bool("frozen", false, "verify the lock and change nothing; fail with lock-drift on a difference")
	c.Flags().String("digest", "", "pin the skill to this digest (sha256:<hex>); outranks the lock and the registry")
}

// learnFlags collects the learn options from the command's flags.
func learnFlags(cmd *cobra.Command, global bool, pathHint string, agents, tokens []string) learnOpts {
	o := learnOpts{Global: global, PathHint: pathHint, Agents: agents, Tokens: tokens}
	o.NoLock, _ = cmd.Flags().GetBool("no-lock")
	o.Frozen, _ = cmd.Flags().GetBool("frozen")
	o.Digest, _ = cmd.Flags().GetString("digest")
	return o
}

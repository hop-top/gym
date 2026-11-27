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
	changed := false
	for _, e := range entries {
		if a.notLockable(sl, e) {
			// The lock no longer describes what is installed under this name.
			changed = f.Remove(e.Name) || changed
			continue
		}
		f.Set(e)
		changed = true
	}
	if !changed {
		return nil
	}
	return sl.save(f)
}

// notLockable reports whether e's stored source is a local path outside the
// scope root of a project or folder lock: a directory, an archive file, or
// a registry entry whose source_url is a local path. Such an entry would be
// refused on read (lock-source-escapes), so it is never written; the skill
// stays untracked by the lock. Says so on stderr. In-tree local sources
// are stored relative and are lockable.
func (a *App) notLockable(sl scopeLock, e lock.Entry) bool {
	if sl.Scope != lock.ScopeProject || !lock.IsLocalSource(e.Source) || !filepath.IsAbs(e.Source) {
		return false
	}
	a.Logger().Warnf("%s: %s is outside %s; not written to the lock", lock.FindingNotPortable, e.Source, sl.Base)
	return true
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

// drop removes a skill's entry: the upgraded install no longer matches what
// the lock recorded and cannot be recorded (see notLockable).
func (t *trainLocks) drop(scope, name string) {
	if f := t.files[scope]; f != nil && f.Remove(name) {
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

// lockRemover is the Remover.OnSkillGone hook that drops a skill's lock
// entry; nil with keep. A missing lock or entry is not an error; a corrupt
// lock is, and the file is left as it is.
func (a *App) lockRemover(sl scopeLock, keep bool) func(string) error {
	if keep {
		return nil
	}
	return func(name string) error {
		f, err := sl.load()
		if err != nil {
			return err
		}
		if !f.Remove(name) {
			return nil
		}
		return sl.save(f)
	}
}

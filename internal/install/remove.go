package install

import (
	"fmt"
	"os"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/state"
)

// ResolveInstallPaths returns the install paths req selects (scope,
// PathHint, AgentPatterns), exactly as learn resolves them. Uninstall
// uses it to narrow records with the same --path/--agent semantics.
func ResolveInstallPaths(req Request) ([]config.InstallPath, error) {
	return resolveInstallPaths(req)
}

// Reasons raised while removing an installed skill.
const (
	// ReasonSkillModified: the install directory differs from the digest
	// recorded at install time (or cannot be verified); --force removes it.
	ReasonSkillModified = "skill-modified"
	// ReasonRemoveFailed: an I/O failure removing the directory or
	// rewriting the state file.
	ReasonRemoveFailed = "remove-failed"
)

// Remover uninstalls skills of one scope. Every directory it deletes has
// first passed CheckRecordPath: there is no other deletion path.
type Remover struct {
	Scope       string
	ProjectRoot string
	// Parents are the operator-trusted install directories of Scope
	// (see TrustedParents).
	Parents []string
	// Force removes a skill whose directory differs from its recorded
	// digest, or has no recorded digest.
	Force bool
	// RemoveAll deletes a validated directory; os.RemoveAll when nil.
	RemoveAll func(string) error
	// SaveState persists the records that remain; called after each
	// directory is gone, so a crash leaves a record without a directory
	// (a retry succeeds), never a directory without a record.
	SaveState func([]state.InstalledSkill) error
	// OnSkillGone is called once with the skill name when no record of
	// it remains in the scope. An error is reported as a failure.
	OnSkillGone func(name string) error
}

// RemoveFailure is one record that could not be removed.
type RemoveFailure struct {
	Path   string
	Reason string
	Err    error
}

// RemoveResult lists what RemoveSkill removed and what it could not.
type RemoveResult struct {
	Removed  []string
	Failures []RemoveFailure
}

// RemoveSkill removes recs (records of skill name) in order: validate the
// path, check for local modification, remove the directory, then drop the
// record and save. A failing record is kept and the rest continue.
// all is the full record list of the scope; the returned list is what
// remains of it.
func (r *Remover) RemoveSkill(name string, recs, all []state.InstalledSkill) ([]state.InstalledSkill, RemoveResult) {
	var res RemoveResult
	left := append([]state.InstalledSkill(nil), all...)
	fail := func(rec state.InstalledSkill, reason string, err error) {
		res.Failures = append(res.Failures, RemoveFailure{Path: rec.InstallPath, Reason: reason, Err: err})
	}
	for _, rec := range recs {
		if err := r.removeDir(rec); err != nil {
			fail(rec, failureReason(err), err)
			continue
		}
		left = dropRecord(left, rec)
		if r.SaveState != nil {
			if err := r.SaveState(left); err != nil {
				fail(rec, ReasonRemoveFailed, fmt.Errorf("%s: state: %w", ReasonRemoveFailed, err))
				continue
			}
		}
		res.Removed = append(res.Removed, rec.InstallPath)
	}
	if len(res.Failures) == 0 && !hasName(left, name) && r.OnSkillGone != nil {
		if err := r.OnSkillGone(name); err != nil {
			fail(state.InstalledSkill{Name: name}, ReasonRemoveFailed, fmt.Errorf("%s: %w", ReasonRemoveFailed, err))
		}
	}
	return left, res
}

func failureReason(err error) string {
	if re, ok := err.(*RefusedError); ok {
		return re.Reason
	}
	return ReasonRemoveFailed
}

func (r *Remover) removeDir(rec state.InstalledSkill) error {
	if err := CheckRecordPath(rec, r.Scope, r.ProjectRoot, r.Parents); err != nil {
		return err
	}
	dir := rec.InstallPath
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil
	}
	if !r.Force {
		if err := checkUnmodified(rec); err != nil {
			return err
		}
	}
	rm := r.RemoveAll
	if rm == nil {
		rm = os.RemoveAll
	}
	if err := rm(dir); err != nil {
		return fmt.Errorf("%s: %s: %w", ReasonRemoveFailed, dir, err)
	}
	return nil
}

func checkUnmodified(rec state.InstalledSkill) error {
	modified := func(format string, a ...any) error {
		return &RefusedError{Reason: ReasonSkillModified, Detail: fmt.Sprintf(format, a...)}
	}
	if rec.Digest == "" {
		return modified("%s has no recorded digest (installed before digests were recorded); cannot verify local edits; --force to remove", rec.InstallPath)
	}
	got, err := Digest(rec.InstallPath)
	if err != nil {
		return modified("%s cannot be verified against recorded %s (%v); --force to remove", rec.InstallPath, rec.Digest, err)
	}
	if got != rec.Digest {
		return modified("%s differs from recorded %s; --force to remove", rec.InstallPath, rec.Digest)
	}
	return nil
}

func dropRecord(recs []state.InstalledSkill, rec state.InstalledSkill) []state.InstalledSkill {
	out := recs[:0:0]
	for _, r := range recs {
		if r.Name == rec.Name && r.InstallPath == rec.InstallPath {
			continue
		}
		out = append(out, r)
	}
	return out
}

func hasName(recs []state.InstalledSkill, name string) bool {
	for _, r := range recs {
		if r.Name == name {
			return true
		}
	}
	return false
}

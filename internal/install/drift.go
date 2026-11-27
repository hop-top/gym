package install

import (
	"fmt"
	"os"
	"sort"

	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/state"
)

// Drift kinds: how what is installed differs from the lock.
const (
	// DriftMissing: a lock entry with nothing installed (no state record,
	// or its directory is gone).
	DriftMissing = "missing"
	// DriftUntracked: an installed skill the lock does not list. A warning.
	DriftUntracked = "untracked"
	// DriftDigest: the digest recorded at install differs from the lock's.
	DriftDigest = "digest-drift"
	// DriftModified: the installed files no longer hash to the digest
	// recorded at install.
	DriftModified = "modified"
)

// ReasonLockDrift is the stable reason of `learn --frozen` and
// `list --check` failing on drift.
const ReasonLockDrift = "lock-drift"

// Drift is one difference between the lock and an installed scope.
type Drift struct {
	Name   string
	Kind   string
	Detail string
}

// Fails reports whether the drift makes `learn --frozen` and
// `list --check` fail: everything but an untracked skill.
func (d Drift) Fails() bool { return d.Kind != DriftUntracked }

// LockDrift compares the lock with the scope's installed records, which
// the caller has already checked with CheckRecordPath (nothing outside the
// scope is hashed). One finding per lock entry, the first of missing,
// digest-drift, modified in record order; then one per untracked skill.
// Findings come sorted by name.
func LockDrift(f *lock.File, recs []state.InstalledSkill) []Drift {
	byName := map[string][]state.InstalledSkill{}
	for _, r := range recs {
		byName[r.Name] = append(byName[r.Name], r)
	}
	for _, rs := range byName {
		sort.Slice(rs, func(i, j int) bool { return rs[i].InstallPath < rs[j].InstallPath })
	}
	var out []Drift
	locked := map[string]bool{}
	for _, e := range f.Skills {
		locked[e.Name] = true
		if d, ok := entryDrift(e, byName[e.Name]); ok {
			out = append(out, d)
		}
	}
	for name := range byName {
		if !locked[name] {
			out = append(out, Drift{Name: name, Kind: DriftUntracked, Detail: "installed, not in the lock"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func entryDrift(e lock.Entry, recs []state.InstalledSkill) (Drift, bool) {
	if len(recs) == 0 {
		return Drift{Name: e.Name, Kind: DriftMissing, Detail: "locked " + e.Version + ", not installed"}, true
	}
	for _, r := range recs {
		if _, err := os.Lstat(r.InstallPath); err != nil {
			return Drift{Name: e.Name, Kind: DriftMissing, Detail: r.InstallPath + " is gone"}, true
		}
		if r.Digest != e.Digest {
			return Drift{Name: e.Name, Kind: DriftDigest,
				Detail: fmt.Sprintf("installed %s, locked %s", orNone(r.Digest), e.Digest)}, true
		}
		got, err := Digest(r.InstallPath)
		if err != nil {
			return Drift{Name: e.Name, Kind: DriftModified, Detail: fmt.Sprintf("%s cannot be verified: %v", r.InstallPath, err)}, true
		}
		if got != r.Digest {
			return Drift{Name: e.Name, Kind: DriftModified, Detail: fmt.Sprintf("%s hashes to %s, recorded %s", r.InstallPath, got, r.Digest)}, true
		}
	}
	return Drift{}, false
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

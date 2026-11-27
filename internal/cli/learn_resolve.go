package cli

import (
	"fmt"
	"strings"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
)

// learnTarget is one `learn` token resolved to something installable.
type learnTarget struct {
	Entry registry.SkillEntry
	// Ref is the git ref to stage (from url#ref, or a locked commit).
	Ref string
	// Requested is what the operator pinned: the version after @, or the
	// ref after #. Empty means the install tracks.
	Requested string
	// Kind is the lock kind: registry, git or dir.
	Kind string
}

// splitNamePin splits `name@version`. Only registry names take @: URLs
// (git@host:..) never reach here.
func splitNamePin(tok string) (name, version string, pinned bool) {
	i := strings.LastIndex(tok, "@")
	if i <= 0 || i == len(tok)-1 {
		return tok, "", false
	}
	return tok[:i], tok[i+1:], true
}

// resolveLearnToken turns a learn argument into an install target:
//
//	url, url#ref, directory     a source, no registry involved
//	name@version                that exact registry version
//	name                        the best registry entry
func resolveLearnToken(tok string, idx registry.Index) (learnTarget, error) {
	if isURLLike(tok) || isExistingDir(tok) || strings.Contains(tok, "#") {
		if loc, ref := install.SplitRef(tok); ref != "" || isURLLike(tok) || isExistingDir(tok) {
			t := learnTarget{
				Entry:     registry.SkillEntry{Name: inferNameFromToken(loc), SourceURL: loc, Version: "0.0.0"},
				Ref:       ref,
				Requested: ref,
				Kind:      lock.KindGit,
			}
			if ref == "" && install.IsDirSource(loc) {
				t.Kind = lock.KindDir
			}
			return t, nil
		}
	}
	name, version, pinned := splitNamePin(tok)
	if !pinned {
		e, ok := idx.Find(tok)
		if !ok {
			return learnTarget{}, fmt.Errorf("skill %q not found in local registry; run `gym update` or use `gym learn` with a URL", tok)
		}
		return learnTarget{Entry: e, Kind: lock.KindRegistry}, nil
	}
	e, ok := idx.FindVersion(name, version)
	if !ok {
		avail := "none"
		if v := idx.Versions(name); len(v) > 0 {
			avail = strings.Join(v, ", ")
		}
		return learnTarget{}, fmt.Errorf("%s: %s@%s not in index (available: %s)", install.ReasonPinUnavailable, name, version, avail)
	}
	return learnTarget{Entry: e, Requested: version, Kind: lock.KindRegistry}, nil
}

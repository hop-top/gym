package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveExisting returns p with symlinks resolved for its longest
// existing prefix; the non-existing tail is appended unchanged. p must
// be absolute. It is how a path that does not exist yet (a fresh
// install destination) is compared against a root.
func ResolveExisting(p string) (string, error) {
	p = filepath.Clean(p)
	var tail []string
	cur := p
	for {
		if _, err := os.Lstat(cur); err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return real, nil
		}
		next := filepath.Dir(cur)
		if next == cur {
			return p, nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = next
	}
}

// Within reports whether child is root or lies beneath it, lexically.
// Both must be clean absolute paths.
func Within(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// ContainedIn reports whether p, an absolute path, lies inside root once
// symlinks along their existing prefixes are resolved.
func ContainedIn(root, p string) bool {
	rr, err := ResolveExisting(root)
	if err != nil {
		return false
	}
	rp, err := ResolveExisting(p)
	if err != nil {
		return false
	}
	return Within(rr, rp)
}

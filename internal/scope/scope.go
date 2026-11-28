// Package scope resolves the chain of folder scopes that apply to a
// directory: the nearest .gym root and the roots of its ancestors.
package scope

import (
	"fmt"
	"os"
	"path/filepath"

	"hop.top/gym/internal/project"
)

// Result is the outcome of Chain.
type Result struct {
	// Roots are the scope roots from the starting folder upward, innermost
	// first. The global scope is not listed: it has no root.
	Roots []string
	// Warnings names each ancestor root that was skipped as untrusted.
	Warnings []string
}

// Chain returns the roots that apply to dir, innermost first. dir itself
// counts when it holds a project marker. The walk stops at $HOME
// (inclusive), or at the filesystem root for a dir outside $HOME.
//
// A root above dir is repository or shared-directory content, so it is read
// only when its .gym directory is owned by the current user and not group-
// or world-writable (the analogue of git's safe.directory). Otherwise it is
// skipped with a warning. dir itself is the operator's own choice and is not
// tested.
func Chain(dir string) Result { return ChainOf(dir, project.IsRoot) }

// ChainOf is Chain with isRoot in place of the project marker test: the
// same walk, boundary and trust rules, for folders matching another marker.
func ChainOf(dir string, isRoot func(dir string) bool) Result {
	var res Result
	start, err := filepath.Abs(dir)
	if err != nil {
		return res
	}
	home := homeDir()
	cur := start
	for {
		if isRoot(cur) {
			if cur == start || trustedGymDir(cur) {
				res.Roots = append(res.Roots, cur)
			} else {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"scope: ignoring %s (not owned by you or writable by others)",
					filepath.Join(cur, project.ProjectDirName)))
			}
		}
		if atHome(cur, home) {
			break
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return res
}

// trustedGymDir reports whether root's .gym directory may be read as an
// ancestor scope.
func trustedGymDir(root string) bool {
	fi, err := os.Stat(filepath.Join(root, project.ProjectDirName))
	if err != nil {
		return false
	}
	return ownedByMe(fi) && !writableByOthers(fi)
}

func homeDir() string {
	h := os.Getenv("HOME")
	if h == "" {
		return ""
	}
	h, err := filepath.Abs(h)
	if err != nil {
		return ""
	}
	return h
}

// atHome reports whether cur is the home directory, by path or, when the
// two reach the same directory through symlinks, by resolved path.
func atHome(cur, home string) bool {
	if home == "" {
		return false
	}
	if cur == home {
		return true
	}
	rc, err1 := filepath.EvalSymlinks(cur)
	rh, err2 := filepath.EvalSymlinks(home)
	return err1 == nil && err2 == nil && rc == rh
}

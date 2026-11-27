//go:build unix

package scope

import (
	"os"
	"syscall"
)

// ownedByMe reports whether fi is owned by the current user. A variable so
// tests can stand in for a foreign owner, which needs root to create.
var ownedByMe = func(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(st.Uid) == os.Geteuid()
}

func writableByOthers(fi os.FileInfo) bool { return fi.Mode().Perm()&0o022 != 0 }

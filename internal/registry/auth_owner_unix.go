//go:build !windows

package registry

import (
	"os"
	"syscall"
)

// tokenFileTrusted: mode 0600-ish (no group or world bits) and owned by
// the running user.
func tokenFileTrusted(st os.FileInfo) bool {
	if st.Mode().Perm()&0o077 != 0 {
		return false
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	return !ok || int(s.Uid) == os.Getuid()
}

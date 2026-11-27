//go:build !unix

package scope

import "os"

// Platforms without POSIX owner and mode bits cannot run the ownership
// test: every directory passes it.
var ownedByMe = func(os.FileInfo) bool { return true }

func writableByOthers(os.FileInfo) bool { return false }

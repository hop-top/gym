//go:build windows

package registry

import "os"

// tokenFileTrusted: ownership and mode bits are not meaningful on Windows.
func tokenFileTrusted(os.FileInfo) bool { return true }

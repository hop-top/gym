package install

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ScanStaging walks a staged skill directory and lifts out the fields
// the CEL policy guard activates on:
//
//   - manifest.postinstall / shell_hook / allow_postinstall / large_binary
//     (read from gym.json, manifest.json, or skill.json at the root)
//   - largest_binary_bytes (largest non-text file under the root)
//
// Errors walking the tree are silently swallowed: the scan is
// best-effort and a missing dir / unreadable file should not block
// the install — guard fires on what is present, not on what we
// couldn't read.
func ScanStaging(root string) ScanResult {
	out := ScanResult{}
	if root == "" {
		return out
	}
	for _, name := range []string{"gym.json", "manifest.json", "skill.json"} {
		path := filepath.Join(root, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if s, ok := m["postinstall"].(string); ok {
			out.Postinstall = s
		}
		if s, ok := m["shell_hook"].(string); ok {
			out.ShellHook = s
		}
		if b, ok := m["allow_postinstall"].(bool); ok {
			out.AllowPostinstall = b
		}
		if b, ok := m["large_binary"].(bool); ok {
			out.LargeBinary = b
		}
		break
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			// Skip vendored/junk directories; cuts walk time on big
			// staging dirs without changing the result for any policy
			// that fires today.
			base := strings.ToLower(d.Name())
			if base == ".git" || base == "node_modules" || base == "vendor" {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > out.LargestBinaryBytes && looksBinary(p) {
			out.LargestBinaryBytes = info.Size()
		}
		return nil
	})
	return out
}

// looksBinary is a cheap heuristic: known-binary extensions count, plus
// a small first-byte sniff for files without an extension. Avoids
// reading the whole file. Good enough for the no-large-binary policy.
func looksBinary(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bin", ".exe", ".dll", ".so", ".dylib", ".a", ".o", ".tar", ".gz",
		".tgz", ".zip", ".7z", ".bz2", ".xz", ".rar",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf",
		".mp3", ".mp4", ".mov", ".webm", ".wav", ".woff", ".woff2", ".ttf":
		return true
	}
	return false
}

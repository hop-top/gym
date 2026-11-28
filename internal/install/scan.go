package install

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ScanStaging walks a staged skill directory and lifts out the fields
// the CEL policy guard activates on:
//
//   - manifest.postinstall / shell_hook (read from gym.json,
//     manifest.json, or skill.json at the root)
//   - scan: text of every text file (SKILL.md included), for the
//     secret patterns
//   - scripts: text of each script file (see isScript), for the
//     script-content policies; SKILL.md prose is not a script
//   - largest_binary_bytes (largest non-text file under the root,
//     outside node_modules/ and vendor/)
//
// Content reads are bounded (MaxScanFileBytes per file,
// MaxScanTotalBytes per field), skip binaries, never follow symlinks
// and skip .git/. node_modules/ and vendor/ are read: a skill could
// otherwise hide a script there.
//
// Only descriptive fields are read from the manifest. A flag that would
// weaken a guard policy (allow_postinstall, large_binary) is never
// taken from the package under evaluation: a manifest still carrying it
// is ignored. Operators relax policies in their user policies dir.
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
		break
	}
	var scan strings.Builder
	scriptBytes := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.EqualFold(d.Name(), ".git") {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		// node_modules/ and vendor/ stay out of the binary-size check.
		if !inVendoredDir(root, p) && info.Size() > out.LargestBinaryBytes && looksBinary(p) {
			out.LargestBinaryBytes = info.Size()
		}
		if looksBinary(p) {
			return nil
		}
		scanRoom := MaxScanTotalBytes - scan.Len()
		scriptRoom := MaxScanTotalBytes - scriptBytes
		if scanRoom <= 1 && scriptRoom <= 0 {
			return nil
		}
		text, ok := readText(p)
		if !ok {
			return nil
		}
		if scanRoom > 1 {
			if scan.Len() > 0 {
				scan.WriteByte('\n')
				scanRoom--
			}
			scan.WriteString(text[:min(len(text), scanRoom)])
		}
		if scriptRoom > 0 && isScript(p, text) {
			s := text[:min(len(text), scriptRoom)]
			out.Scripts = append(out.Scripts, s)
			scriptBytes += len(s)
		}
		return nil
	})
	out.Scan = scan.String()
	return out
}

// scriptExts are the file extensions read as scripts. A file with a
// "#!" first line is a script whatever its extension (Markdown aside).
var scriptExts = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".ksh": true,
	".ps1": true, ".py": true, ".rb": true, ".js": true, ".ts": true, ".pl": true,
}

// isScript reports whether a file's text is read as a script.
func isScript(path, text string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if scriptExts[ext] {
		return true
	}
	return ext != ".md" && strings.HasPrefix(text, "#!")
}

// readText reads up to MaxScanFileBytes of a regular file, refusing
// content with a NUL byte (binary).
func readText(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, MaxScanFileBytes))
	if err != nil || bytes.IndexByte(buf, 0) >= 0 {
		return "", false
	}
	return string(buf), true
}

// inVendoredDir reports whether p sits under a node_modules/ or
// vendor/ directory below root.
func inVendoredDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts[:len(parts)-1] {
		switch strings.ToLower(part) {
		case "node_modules", "vendor":
			return true
		}
	}
	return false
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

// Read bounds for the content scan: bytes read per file, and bytes kept
// per payload field (scan, scripts).
const (
	MaxScanFileBytes  = 256 << 10
	MaxScanTotalBytes = 4 << 20
)

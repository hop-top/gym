package install

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DigestPrefix is the algorithm tag of every digest string.
const DigestPrefix = "sha256:"

// DigestFile is one line of the digest listing.
type DigestFile struct {
	Path       string // slash-separated, relative to the skill root
	Executable bool   // owner-exec bit
	Hash       string // lowercase hex sha256 of the raw bytes
}

// Line renders the listing line `<x|-> <hex> <relpath>`.
func (f DigestFile) Line() string {
	mode := "-"
	if f.Executable {
		mode = "x"
	}
	return mode + " " + f.Hash + " " + f.Path
}

// DigestListing walks root and returns the per-file listing sorted
// bytewise on path (gym.digest/v1). Only `.git` directories are
// skipped: skipped content would be unhashed content. Symlinks and
// other irregular entries are rejected with digest-unsupported-entry.
func DigestListing(root string) ([]DigestFile, error) {
	var files []DigestFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" && p != root {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			return admissionErrorf(ReasonDigestUnsupportedEntry,
				"%s is not a regular file (%s)", rel, d.Type())
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := hashFile(p)
		if err != nil {
			return err
		}
		files = append(files, DigestFile{
			Path:       rel,
			Executable: info.Mode().Perm()&0o100 != 0,
			Hash:       h,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Digest returns the content digest of the skill rooted at root:
// `sha256:` + lowercase hex of sha256 over the sorted file listing.
func Digest(root string) (string, error) {
	files, err := DigestListing(root)
	if err != nil {
		return "", err
	}
	return DigestOfListing(files), nil
}

// DigestOfListing returns the digest string for a listing from DigestListing.
func DigestOfListing(files []DigestFile) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.Line())
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return DigestPrefix + hex.EncodeToString(sum[:])
}

// ValidDigest reports whether s has the form sha256:<64 lowercase hex>.
func ValidDigest(s string) bool {
	if !strings.HasPrefix(s, DigestPrefix) {
		return false
	}
	h := s[len(DigestPrefix):]
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

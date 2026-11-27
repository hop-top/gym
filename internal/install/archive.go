package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/registry"
)

// ReasonArchiveInvalid prefixes every archive rejection. It is a command
// error: nothing is published for it.
const ReasonArchiveInvalid = "archive-invalid"

// Archive limits.
const (
	// MaxArchiveEntries is the most entries (files and directories) one
	// archive may hold.
	MaxArchiveEntries = 10000
	// MaxArchiveBytes is the most expanded bytes one archive may hold; it
	// also bounds a downloaded archive.
	MaxArchiveBytes = 256 << 20
)

// The limits in force; tests lower them to exercise paths that would need
// hundreds of megabytes.
var (
	maxEntries       = MaxArchiveEntries
	maxBytes   int64 = MaxArchiveBytes
)

// ArchiveError is an archive-invalid rejection.
type ArchiveError struct{ Detail string }

func (e *ArchiveError) Error() string { return ReasonArchiveInvalid + ": " + e.Detail }

func archiveErrorf(format string, args ...any) *ArchiveError {
	return &ArchiveError{Detail: fmt.Sprintf(format, args...)}
}

const (
	kindTarGz = "tar.gz"
	kindZip   = "zip"
)

// archiveKind is the archive format a name's extension says, or "".
func archiveKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		return kindTarGz
	case strings.HasSuffix(n, ".zip"):
		return kindZip
	}
	return ""
}

// IsArchiveSource reports whether source names an archive: a path, a
// file:// URL or an http(s) URL ending in .tar.gz, .tgz or .zip. An
// existing directory of that name is a directory.
func IsArchiveSource(source string) bool {
	if IsDirSource(source) {
		return false
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		u, err := url.Parse(source)
		return err == nil && archiveKind(u.Path) != ""
	}
	if strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "ssh://") {
		return false
	}
	return archiveKind(strings.TrimPrefix(source, "file://")) != ""
}

// ArchiveBaseName is the skill name an archive source suggests: its file
// name without the archive extension.
func ArchiveBaseName(source string) string {
	p := source
	if u, err := url.Parse(source); err == nil && u.Scheme != "" && u.Path != "" {
		p = u.Path
	}
	b := path.Base(filepath.ToSlash(strings.TrimPrefix(p, "file://")))
	lb := strings.ToLower(b)
	for _, ext := range []string{".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(lb, ext) {
			return b[:len(b)-len(ext)]
		}
	}
	return b
}

// ArchiveAuth is the credential an archive URL may reuse: that of the
// repository that listed it, for that repository's host only.
type ArchiveAuth struct {
	Auth    *config.AuthConfig
	RepoURL string
}

// StageArchive extracts the archive at source (a path, a file:// URL or an
// http(s) URL) into a fresh temporary directory and returns the skill root:
// the extraction root, or its sole top-level directory.
func StageArchive(ctx context.Context, source string, au *ArchiveAuth) (Staged, error) {
	kind := archiveKind(source)
	if u, err := url.Parse(source); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		kind = archiveKind(u.Path)
	}
	if kind == "" {
		return Staged{}, fmt.Errorf("unsupported source %q: not an archive", RedactURL(source))
	}
	tmp, err := os.MkdirTemp("", "skills-archive-*")
	if err != nil {
		return Staged{}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	fail := func(err error) (Staged, error) { cleanup(); return Staged{}, err }

	file := strings.TrimPrefix(source, "file://")
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		file = filepath.Join(tmp, "download")
		if err := download(ctx, source, file, au); err != nil {
			return fail(err)
		}
	}
	tree := filepath.Join(tmp, "tree")
	if err := os.Mkdir(tree, 0o755); err != nil {
		return fail(err)
	}
	if err := ExtractArchive(file, kind, tree); err != nil {
		return fail(err)
	}
	root, err := archiveRoot(tree)
	if err != nil {
		return fail(err)
	}
	return Staged{Root: root, Cleanup: cleanup}, nil
}

// download fetches rawURL into file through the network policy, with the
// listing repository's credential when the host matches.
func download(ctx context.Context, rawURL, file string, au *ArchiveAuth) error {
	var auth *config.AuthConfig
	repo := ""
	if au != nil {
		auth, repo = au.Auth, au.RepoURL
	}
	resp, err := registry.Get(ctx, nil, rawURL, auth, repo)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("archive fetch %s failed: status %d", RedactURL(rawURL), resp.StatusCode)
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("archive fetch %s: %w", RedactURL(rawURL), err)
	}
	if n > maxBytes {
		return archiveErrorf("archive larger than %d MiB", maxBytes>>20)
	}
	return nil
}

// archiveRoot is the skill root of an extracted tree: the tree itself, or
// its sole top-level directory when that is the only entry.
func archiveRoot(tree string) (string, error) {
	ents, err := os.ReadDir(tree)
	if err != nil {
		return "", err
	}
	if len(ents) == 1 && ents[0].IsDir() {
		return filepath.Join(tree, ents[0].Name()), nil
	}
	return tree, nil
}

// ExtractArchive extracts the archive file (kind tar.gz or zip) into dest,
// an existing empty directory, with stdlib readers only. Anything that
// could write outside dest or that digests cannot represent is rejected
// with an *ArchiveError: absolute names, "..", symlinks, hardlinks,
// devices, duplicate paths, more than MaxArchiveEntries entries, more than
// MaxArchiveBytes expanded.
func ExtractArchive(file, kind, dest string) error {
	x := &extractor{dest: dest, files: map[string]bool{}, dirs: map[string]bool{}}
	switch kind {
	case kindZip:
		return x.zip(file)
	case kindTarGz:
		return x.tarGz(file)
	}
	return archiveErrorf("unknown archive format %q", kind)
}

type extractor struct {
	dest     string
	entries  int
	declared int64
	written  int64
	files    map[string]bool // lower-cased cleaned paths of files
	dirs     map[string]bool // lower-cased cleaned paths of directories
}

// entryName validates and cleans an entry name. skip is true for the
// archive root entry ("./").
func entryName(name string) (clean string, skip bool, err error) {
	if name == "" {
		return "", false, archiveErrorf("entry with an empty name")
	}
	if strings.ContainsRune(name, 0) || strings.Contains(name, `\`) {
		return "", false, archiveErrorf("invalid characters in entry name %q", name)
	}
	if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':') {
		return "", false, archiveErrorf("absolute path %q", name)
	}
	for _, p := range strings.Split(name, "/") {
		if p == ".." {
			return "", false, archiveErrorf("path traversal in %q", name)
		}
	}
	c := path.Clean(name)
	if c == "." {
		return "", true, nil
	}
	return c, false, nil
}

func (x *extractor) count() error {
	x.entries++
	if x.entries > maxEntries {
		return archiveErrorf("more than %d entries", maxEntries)
	}
	return nil
}

func (x *extractor) full(clean string) (string, error) {
	p := filepath.Join(x.dest, filepath.FromSlash(clean))
	if rel, err := filepath.Rel(x.dest, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", archiveErrorf("path traversal in %q", clean)
	}
	return p, nil
}

func (x *extractor) dir(clean string) error {
	key := strings.ToLower(clean)
	if x.files[key] {
		return archiveErrorf("duplicate path %q (a file and a directory)", clean)
	}
	for p := path.Dir(clean); p != "." && p != "/"; p = path.Dir(p) {
		if x.files[strings.ToLower(p)] {
			return archiveErrorf("%q lies below the file %q", clean, p)
		}
	}
	full, err := x.full(clean)
	if err != nil {
		return err
	}
	x.dirs[key] = true
	return os.MkdirAll(full, 0o755)
}

func (x *extractor) file(clean string, exec bool, size int64, r io.Reader) error {
	key := strings.ToLower(clean)
	if x.files[key] || x.dirs[key] {
		return archiveErrorf("duplicate path %q", clean)
	}
	for p := path.Dir(clean); p != "." && p != "/"; p = path.Dir(p) {
		if x.files[strings.ToLower(p)] {
			return archiveErrorf("%q lies below the file %q", clean, p)
		}
	}
	if size < 0 || x.declared+size > maxBytes {
		return archiveErrorf("expanded size exceeds %d MiB", maxBytes>>20)
	}
	x.declared += size
	full, err := x.full(clean)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	x.files[key] = true
	// The declared size is not trusted: count what is really written.
	n, cerr := io.Copy(f, io.LimitReader(r, maxBytes-x.written+1))
	if err := f.Close(); cerr == nil {
		cerr = err
	}
	x.written += n
	if x.written > maxBytes {
		return archiveErrorf("expanded size exceeds %d MiB", maxBytes>>20)
	}
	if cerr != nil {
		return archiveErrorf("corrupt entry %q: %v", clean, cerr)
	}
	return nil
}

func (x *extractor) tarGz(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return archiveErrorf("not a gzip stream: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return archiveErrorf("corrupt tar stream: %v", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if err := x.count(); err != nil {
			return err
		}
		clean, skip, err := entryName(h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeSymlink:
			return archiveErrorf("symlink entry %q", h.Name)
		case tar.TypeLink:
			return archiveErrorf("hardlink entry %q", h.Name)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return archiveErrorf("device or special entry %q", h.Name)
		case tar.TypeDir:
			if !skip {
				if err := x.dir(clean); err != nil {
					return err
				}
			}
		case tar.TypeReg, tar.TypeRegA:
			if skip {
				return archiveErrorf("invalid file name %q", h.Name)
			}
			if err := x.file(clean, h.Mode&0o100 != 0, h.Size, tr); err != nil {
				return err
			}
		default:
			return archiveErrorf("unsupported entry type %q for %q", string(h.Typeflag), h.Name)
		}
	}
}

func (x *extractor) zip(file string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return archiveErrorf("not a zip archive: %v", err)
	}
	defer zr.Close()
	if len(zr.File) > maxEntries {
		return archiveErrorf("more than %d entries", maxEntries)
	}
	for _, zf := range zr.File {
		if err := x.count(); err != nil {
			return err
		}
		clean, skip, err := entryName(zf.Name)
		if err != nil {
			return err
		}
		mode := zf.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			return archiveErrorf("symlink entry %q", zf.Name)
		case mode&(fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeCharDevice|fs.ModeIrregular) != 0:
			return archiveErrorf("device or special entry %q", zf.Name)
		case zf.FileInfo().IsDir() || strings.HasSuffix(zf.Name, "/"):
			if !skip {
				if err := x.dir(clean); err != nil {
					return err
				}
			}
		default:
			if skip {
				return archiveErrorf("invalid file name %q", zf.Name)
			}
			rc, err := zf.Open()
			if err != nil {
				return archiveErrorf("corrupt entry %q: %v", zf.Name, err)
			}
			err = x.file(clean, mode&0o100 != 0, int64(zf.UncompressedSize64), rc)
			_ = rc.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

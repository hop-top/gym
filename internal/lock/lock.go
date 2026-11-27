// Package lock reads and writes gym.lock/v1: the committed record of which
// source, version, commit and content digest each installed skill of one
// scope came from.
//
// A project lock lives in a repository and is therefore repository content:
// nothing in it is trusted as a path or as code. Parse is strict (closed
// object, known schema, scope matching the file's location) and
// ResolveSource refuses local sources that leave the scope root.
package lock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/fsutil"
)

// Schema and DigestScheme are the only values a lock may carry.
const (
	Schema       = "gym.lock/v1"
	DigestScheme = "gym.digest/v1"
)

// Scope values; they equal the install scope strings.
const (
	ScopeProject = "project"
	ScopeGlobal  = "global"
)

// Entry kinds.
const (
	KindRegistry = "registry"
	KindGit      = "git"
	KindDir      = "dir"
	KindArchive  = "archive"
)

// Stable reasons and finding names.
const (
	ReasonInvalid       = "lock-invalid"
	ReasonMissing       = "lock-missing"
	ReasonSourceEscapes = "lock-source-escapes"
	// FindingNotPortable is the warning for a local source that cannot be
	// stored relative to the scope root.
	FindingNotPortable = "lock-source-not-portable"
)

// FileName is the lock's file name in both scopes.
const FileName = "lock.json"

// Error is a lock failure with a stable Reason.
type Error struct {
	Reason string
	Detail string
}

func (e *Error) Error() string { return e.Reason + ": " + e.Detail }

func errorf(reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// IsMissing reports whether err is lock-missing.
func IsMissing(err error) bool {
	var le *Error
	return errors.As(err, &le) && le.Reason == ReasonMissing
}

// Entry is one locked skill. Field order is the file's field order.
type Entry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Source is the credential-free locator. Local sources inside the lock's
	// folder are stored relative to it.
	Source string `json:"source"`
	// Requested is what the operator typed after @ or #; absent means the
	// entry tracks the registry or branch and `gym train` may move it.
	Requested string `json:"requested,omitempty"`
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Path      string `json:"path,omitempty"`
	Registry  string `json:"registry,omitempty"`
	Digest    string `json:"digest"`
}

// File is a whole lock.
type File struct {
	Schema       string  `json:"schema"`
	DigestScheme string  `json:"digest_scheme"`
	Scope        string  `json:"scope"`
	Skills       []Entry `json:"skills"`
}

// New returns an empty lock for scope.
func New(scope string) *File {
	return &File{Schema: Schema, DigestScheme: DigestScheme, Scope: scope, Skills: []Entry{}}
}

// ProjectPath is the lock of the folder scope rooted at root.
func ProjectPath(root string) string {
	return filepath.Join(root, ".gym", FileName)
}

// GlobalPath is the global lock, in the user config dir.
func GlobalPath(configHome string) string {
	return filepath.Join(configHome, FileName)
}

// Find returns a copy of the entry for name.
func (f *File) Find(name string) (Entry, bool) {
	for _, e := range f.Skills {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Set adds e or replaces the entry of the same name.
func (f *File) Set(e Entry) {
	for i := range f.Skills {
		if f.Skills[i].Name == e.Name {
			f.Skills[i] = e
			return
		}
	}
	f.Skills = append(f.Skills, e)
}

// Remove drops the entry for name and reports whether there was one.
func (f *File) Remove(name string) bool {
	for i := range f.Skills {
		if f.Skills[i].Name == name {
			f.Skills = append(f.Skills[:i], f.Skills[i+1:]...)
			return true
		}
	}
	return false
}

// Marshal renders the lock deterministically: entries sorted bytewise by
// name, fixed field order, two-space indent, trailing newline, no
// timestamps. The receiver is not modified.
func (f *File) Marshal() ([]byte, error) {
	out := File{Schema: f.Schema, DigestScheme: f.DigestScheme, Scope: f.Scope}
	out.Skills = append([]Entry{}, f.Skills...)
	sort.Slice(out.Skills, func(i, j int) bool { return out.Skills[i].Name < out.Skills[j].Name })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Save writes f to path atomically, creating the parent directory.
func Save(path string, f *File) error {
	blob, err := f.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, blob, 0o644)
}

var (
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitRe = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

// Parse decodes and validates a lock read from path for scope. Unknown
// fields, trailing data, another schema, or a scope that is not the one
// the file's location implies are lock-invalid.
func Parse(data []byte, scope, path string) (*File, error) {
	bad := func(format string, args ...any) error {
		return errorf(ReasonInvalid, "%s: %s", path, fmt.Sprintf(format, args...))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, bad("%v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, bad("unexpected data after the lock object")
	}
	if f.Schema != Schema {
		return nil, bad("schema %q, want %q", f.Schema, Schema)
	}
	if f.DigestScheme != DigestScheme {
		return nil, bad("digest_scheme %q, want %q", f.DigestScheme, DigestScheme)
	}
	if f.Scope != scope {
		return nil, bad("scope %q, but the file's location is scope %q", f.Scope, scope)
	}
	seen := map[string]bool{}
	for i, e := range f.Skills {
		if !validName(e.Name) {
			return nil, bad("skills[%d]: invalid name %q", i, e.Name)
		}
		if seen[e.Name] {
			return nil, bad("skills[%d]: duplicate name %q", i, e.Name)
		}
		seen[e.Name] = true
		switch e.Kind {
		case KindRegistry, KindGit, KindDir, KindArchive:
		default:
			return nil, bad("%s: invalid kind %q", e.Name, e.Kind)
		}
		if e.Source == "" {
			return nil, bad("%s: source is empty", e.Name)
		}
		if !digestRe.MatchString(e.Digest) {
			return nil, bad("%s: invalid digest %q", e.Name, e.Digest)
		}
		if e.Commit != "" && !commitRe.MatchString(e.Commit) {
			return nil, bad("%s: invalid commit %q", e.Name, e.Commit)
		}
		if e.Path != "" && !validSubpath(e.Path) {
			return nil, bad("%s: path %q must be relative and stay inside the source", e.Name, e.Path)
		}
	}
	if f.Skills == nil {
		f.Skills = []Entry{}
	}
	return &f, nil
}

// Load reads the lock at path. A missing file is lock-missing.
func Load(path, scope string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errorf(ReasonMissing, "%s not found", path)
	}
	if err != nil {
		return nil, err
	}
	return Parse(data, scope, path)
}

// LoadOrNew is Load, with a missing file giving an empty lock.
func LoadOrNew(path, scope string) (*File, error) {
	f, err := Load(path, scope)
	if IsMissing(err) {
		return New(scope), nil
	}
	return f, err
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && !strings.ContainsRune(name, 0)
}

// validSubpath: a clean relative path with no ".." element.
func validSubpath(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) ||
		strings.ContainsRune(p, 0) || filepath.VolumeName(p) != "" {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// IsLocalSource reports whether source names something on this machine's
// filesystem: a plain path or a file:// URL. Network URLs and scp-style
// git@host:path are not.
func IsLocalSource(source string) bool {
	if strings.HasPrefix(source, "file://") {
		return true
	}
	return !strings.Contains(source, "://") && !strings.HasPrefix(source, "git@")
}

func localPath(source string) string { return strings.TrimPrefix(source, "file://") }

// SourceForLock is the form of source to store in the lock whose scope
// root (project) or folder (global) is base. A local source inside base is
// stored relative, with slashes; any other local source is stored
// absolute and portable is false (finding lock-source-not-portable).
// Sources that are not local pass through.
func SourceForLock(base, source string) (stored string, portable bool) {
	if !IsLocalSource(source) {
		return source, true
	}
	p := localPath(source)
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return source, false
		}
		p = abs
	}
	p = filepath.Clean(p)
	if base != "" && config.Within(filepath.Clean(base), p) && config.ContainedIn(base, p) {
		if rel, err := filepath.Rel(base, p); err == nil {
			return filepath.ToSlash(rel), true
		}
	}
	return p, false
}

// ResolveSource turns a locked source into what the installer should use.
// Network sources pass through. A local source is resolved against base;
// in a project lock it must stay inside base, symlinks resolved, and may
// not be absolute: a cloned repository's lock cannot point the installer
// at the operator's files (lock-source-escapes). The global lock is the
// operator's own and may hold absolute paths.
func ResolveSource(base, scope, source string) (string, error) {
	if !IsLocalSource(source) {
		return source, nil
	}
	p := localPath(source)
	if scope == ScopeProject {
		if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || filepath.VolumeName(p) != "" {
			return "", errorf(ReasonSourceEscapes, "%s", source)
		}
		abs := filepath.Join(base, p)
		if !config.Within(filepath.Clean(base), abs) || !config.ContainedIn(base, abs) {
			return "", errorf(ReasonSourceEscapes, "%s", source)
		}
		return abs, nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p), nil
}

// gitignoreBody is what gym keeps out of version control in .gym: the
// machine-local state and the installed trees. The lock is committed.
const gitignoreBody = "installed.json\nskills/\n"

// EnsureGitignore writes <root>/.gym/.gitignore once, if absent.
func EnsureGitignore(root string) error {
	dir := filepath.Join(root, ".gym")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, ".gitignore")
	if _, err := os.Lstat(p); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, werr := f.WriteString(gitignoreBody)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

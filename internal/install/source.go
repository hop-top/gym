package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"hop.top/kit/go/console/progress"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/registry"
)

// Reasons raised while resolving a pin. They are command errors, not
// rejections: nothing is published for them.
const (
	// ReasonPinUnavailable: the registry index has no such name@version.
	ReasonPinUnavailable = "pin-unavailable"
	// ReasonRefNotFound: the git remote has no such branch, tag or commit.
	ReasonRefNotFound = "ref-not-found"
)

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://")
}

// IsDirSource reports whether source (a path, or a file:// URL of a path)
// names an existing directory.
func IsDirSource(source string) bool {
	st, err := os.Stat(strings.TrimPrefix(source, "file://"))
	return err == nil && st.IsDir()
}

// SplitRef splits `source#ref` into the source and the git ref. A fragment
// is a ref only on something git can fetch: a URL, a file:// URL or an
// existing directory; an existing directory whose name contains '#' is not
// split. Without a fragment ref is empty.
func SplitRef(raw string) (source, ref string) {
	if IsDirSource(raw) {
		return raw, ""
	}
	i := strings.LastIndex(raw, "#")
	if i <= 0 || i == len(raw)-1 {
		return raw, ""
	}
	src := raw[:i]
	if looksLikeURL(src) || strings.HasPrefix(src, "file://") || IsDirSource(src) {
		return src, raw[i+1:]
	}
	return raw, ""
}

var (
	refRe    = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)
	commitRe = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
	// refMissingRe matches what git prints when the remote lacks the ref,
	// as opposed to an unreachable or missing repository.
	refMissingRe = regexp.MustCompile(`(?i)remote branch .* not found|couldn't find remote ref|not our ref|unadvertised object|did not match any|invalid object|bad object|unknown revision`)
)

// ValidRef reports whether ref can be handed to git as a branch, tag or
// commit: no leading '-' (option injection), no spaces or control
// characters, no "..".
func ValidRef(ref string) bool {
	return len(ref) <= 255 && refRe.MatchString(ref) && !strings.HasPrefix(ref, "-") && !strings.Contains(ref, "..")
}

// IsCommitRef reports whether ref is a full 40- or 64-hex commit id.
func IsCommitRef(ref string) bool { return commitRe.MatchString(ref) }

// Staged is a source made readable on disk.
type Staged struct {
	// Root is the directory holding the skill tree.
	Root string
	// Commit is `git rev-parse HEAD` of a git checkout; empty for a
	// directory source.
	Commit string
	// Cleanup removes what staging created; safe to call once.
	Cleanup func()
}

// StageSource makes source readable. A directory (path or file:// URL) is
// used in place. A URL, or anything with a ref, is fetched with git: no ref
// takes the default branch, a branch or tag is cloned with --branch, and a
// 40/64-hex commit is fetched with init + fetch + detached checkout.
func StageSource(ctx context.Context, source, ref string) (Staged, error) {
	if ref == "" && IsDirSource(source) {
		return Staged{Root: strings.TrimPrefix(source, "file://"), Cleanup: func() {}}, nil
	}
	if ref == "" && !looksLikeURL(source) {
		return Staged{}, fmt.Errorf("unsupported source %q: expected URL or existing directory", source)
	}
	if ref != "" && !ValidRef(ref) {
		return Staged{}, fmt.Errorf("invalid ref %q", ref)
	}
	if ref != "" && !looksLikeURL(source) && !strings.HasPrefix(source, "file://") && !IsDirSource(source) {
		return Staged{}, fmt.Errorf("unsupported source %q: expected URL or existing directory", source)
	}
	if strings.HasPrefix(source, "-") {
		return Staged{}, fmt.Errorf("unsupported source %q", source)
	}
	tmp, err := os.MkdirTemp("", "skills-install-*")
	if err != nil {
		return Staged{}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	rep := progress.FromContext(ctx)
	item := RedactURL(source)
	emit := func(ok *bool) {
		rep.Emit(ctx, progress.Event{Phase: "clone", Item: item, OK: ok, At: time.Now().UTC()})
	}
	emit(nil)
	fail := func(err error) (Staged, error) {
		ok := false
		emit(&ok)
		cleanup()
		return Staged{}, err
	}

	var out []byte
	switch {
	case ref == "":
		out, err = git(ctx, "", "clone", "--depth", "1", "--", source, tmp)
	case IsCommitRef(ref):
		out, err = fetchCommit(ctx, tmp, source, strings.ToLower(ref))
	default:
		out, err = git(ctx, "", "clone", "--depth", "1", "--branch", ref, "--", source, tmp)
	}
	if err != nil {
		text := redactText(strings.TrimSpace(string(out)), source)
		if ref != "" && refMissingRe.MatchString(text) {
			return fail(fmt.Errorf("%s: %s at %s", ReasonRefNotFound, ref, item))
		}
		return fail(fmt.Errorf("git clone failed: %s\n%s", redactText(err.Error(), source), text))
	}
	commit, err := revParseHead(ctx, tmp)
	if err != nil {
		return fail(err)
	}
	if IsCommitRef(ref) && !strings.EqualFold(commit, ref) {
		return fail(fmt.Errorf("%s: %s at %s", ReasonRefNotFound, ref, item))
	}
	ok := true
	emit(&ok)
	return Staged{Root: tmp, Commit: commit, Cleanup: cleanup}, nil
}

// fetchCommit checks out one commit into dir: git cannot clone a sha.
func fetchCommit(ctx context.Context, dir, source, sha string) ([]byte, error) {
	for _, args := range [][]string{
		{"init", "-q", dir},
		{"-C", dir, "remote", "add", "origin", "--", source},
		{"-C", dir, "fetch", "-q", "--depth", "1", "origin", sha},
		{"-C", dir, "checkout", "-q", "--detach", "FETCH_HEAD"},
	} {
		if out, err := git(ctx, "", args...); err != nil {
			return out, err
		}
	}
	return nil, nil
}

func revParseHead(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// git runs one git command (in dir when set) and returns its combined output.
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// stageEntry stages entry's source at ref and returns the skill directory
// inside it (entry.Path below the staged root, which may not leave it).
func stageEntry(ctx context.Context, entry registry.SkillEntry, ref string) (skillPath, commit string, cleanup func(), err error) {
	st, err := StageSource(ctx, entry.SourceURL, ref)
	if err != nil {
		return "", "", nil, err
	}
	skillPath = st.Root
	if entry.Path != "" {
		skillPath = filepath.Join(st.Root, entry.Path)
		if !config.Within(filepath.Clean(st.Root), filepath.Clean(skillPath)) {
			st.Cleanup()
			return "", "", nil, fmt.Errorf("skill path %q leaves the source", entry.Path)
		}
	}
	return skillPath, st.Commit, st.Cleanup, nil
}

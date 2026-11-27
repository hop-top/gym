package install

import (
	"os"
	"path/filepath"
	"strings"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/ide"
	"hop.top/gym/internal/state"
)

// ReasonInstallPathUntrusted refuses a state record whose install_path is
// not where gym would have installed that skill.
const ReasonInstallPathUntrusted = "install-path-untrusted"

// RefusedError is a refusal raised before any network or filesystem
// effect: a source URL with embedded credentials, an untrusted state
// record. Error text is "<reason>: <detail>". Not relaxable by --insecure.
type RefusedError struct {
	Reason string
	Detail string
}

func (e *RefusedError) Error() string { return e.Reason + ": " + e.Detail }

// ValidSkillName reports whether name is a single path component that can
// name an install directory.
func ValidSkillName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && !strings.ContainsRune(name, 0)
}

// TrustedParents are the directories a skill of scope may be installed
// into by operator configuration: the configured install paths of that
// scope and, for global scope, the standard global locations. cfg's
// project-layer paths are already contained to the project tree.
func TrustedParents(scope string, cfg config.Config) []string {
	var out []string
	for _, p := range cfg.InstallPaths {
		if p.Scope == scope && p.Path != "" {
			out = append(out, p.Path)
		}
	}
	if scope == "global" {
		for _, p := range ide.StandardGlobalPathCandidates(os.Getenv("HOME")) {
			out = append(out, p.Path)
		}
	}
	return out
}

// CheckRecordPath decides whether a state record's install_path may be
// written to or deleted. State files can come from a cloned repository
// (the project file) so the path is trusted only if ALL hold:
//
//   - it is a clean absolute path whose base name is the skill name;
//   - its parent, with symlinks resolved, is inside projectRoot (project
//     scope) or equals a trusted parent (see TrustedParents) of that scope;
//   - it is a directory or absent, never a symlink or file.
//
// Anything else returns a *RefusedError (install-path-untrusted) and the
// caller must not touch the directory. Not relaxable by --insecure; the
// single function any uninstall must call before deleting a recorded path.
func CheckRecordPath(rec state.InstalledSkill, scope, projectRoot string, parents []string) error {
	untrusted := func() error {
		return &RefusedError{Reason: ReasonInstallPathUntrusted, Detail: rec.InstallPath}
	}
	p := rec.InstallPath
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || !ValidSkillName(rec.Name) ||
		filepath.Base(p) != rec.Name {
		return untrusted()
	}
	parent, err := config.ResolveExisting(filepath.Dir(p))
	if err != nil {
		return untrusted()
	}
	ok := false
	if scope == "project" && projectRoot != "" {
		if abs, err := filepath.Abs(projectRoot); err == nil && config.ContainedIn(abs, parent) {
			ok = true
		}
	}
	for _, tp := range parents {
		if !filepath.IsAbs(tp) {
			continue
		}
		if rp, err := config.ResolveExisting(tp); err == nil && rp == parent {
			ok = true
			break
		}
	}
	if !ok {
		return untrusted()
	}
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !fi.IsDir() {
		return untrusted()
	}
	return nil
}

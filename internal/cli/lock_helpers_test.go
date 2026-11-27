package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/xdg"
)

// lkEnv is an isolated HOME with a project folder to learn into.
type lkEnv struct {
	home, proj string
	dirs       xdg.Dirs
}

func newLkEnv(t *testing.T, userCfg string) lkEnv {
	t.Helper()
	home := isolate(t)
	writeUserConfig(t, home, userCfg)
	proj := filepath.Join(scratchDir(t), "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs, err := xdg.AppDirs(config.AppName)
	if err != nil {
		t.Fatal(err)
	}
	if err := xdg.Ensure(dirs); err != nil {
		t.Fatal(err)
	}
	return lkEnv{home: home, proj: proj, dirs: dirs}
}

func (e lkEnv) gym(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runGym(t, e.proj, args...)
}

// mustGym runs gym, fails on a non-zero exit, and returns stdout+stderr.
func (e lkEnv) mustGym(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := e.gym(t, args...)
	if code != 0 {
		t.Fatalf("gym %v: exit %d\n%s\n%s", args, code, out, errs)
	}
	return out + errs
}

// index replaces the local registry index.
func (e lkEnv) index(t *testing.T, entries ...registry.SkillEntry) {
	t.Helper()
	if err := registry.SaveIndex(e.dirs.DataHome, registry.Index{Skills: entries}); err != nil {
		t.Fatal(err)
	}
}

func (e lkEnv) lockPath() string { return lock.ProjectPath(e.proj) }

func (e lkEnv) loadLock(t *testing.T) *lock.File {
	t.Helper()
	f, err := lock.Load(e.lockPath(), lock.ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (e lkEnv) lockEntry(t *testing.T, name string) lock.Entry {
	t.Helper()
	ent, ok := e.loadLock(t).Find(name)
	if !ok {
		t.Fatalf("no lock entry for %s", name)
	}
	return ent
}

func (e lkEnv) skillDir(name string) string { return filepath.Join(e.proj, ".gym", "skills", name) }

// lkSkillSrc writes the skill directory <parent>/<name>; tag varies the body.
func lkSkillSrc(t *testing.T, parent, name, tag string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	writeFiles(t, dir, map[string]string{
		"SKILL.md": "---\nname: " + name + "\ndescription: d\n---\nbody " + tag + "\n",
	})
	return dir
}

func lkRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lkRequireGit skips the test when the git binary is missing.
func lkRequireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed: git-backed pin tests need it")
	}
}

// lkGit runs git in dir with a fixed identity and no global or system config.
func lkGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// lkGitRepo creates a repository at dir with one commit holding files and
// returns its HEAD commit.
func lkGitRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lkGit(t, dir, "init", "-q")
	return lkGitCommit(t, dir, files)
}

// lkGitCommit rewrites files in dir, commits, returns the new HEAD.
func lkGitCommit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	writeFiles(t, dir, files)
	lkGit(t, dir, "add", "-A")
	lkGit(t, dir, "commit", "-q", "-m", "c")
	return lkGit(t, dir, "rev-parse", "HEAD")
}

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// learnSkill stages a skill with the given manifest (and optionally a
// sparse >25 MiB binary) and runs `gym learn` on it through the real
// guard. The destination sits under the package dir: the built-in
// path-allowlist denies system temp.
const schemaPrefix = `"schema":"gym.skill/v1","allowed_tools":[],"data_class":"public"`

func learnSkill(t *testing.T, manifest string, bigBinary bool, extra ...string) (string, int) {
	t.Helper()
	_, cwd := isolateCfg(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "SKILL.md"), "---\nname: s\ndescription: test skill\n---\n# s\n")
	write(t, filepath.Join(src, "gym.json"), manifest)
	if bigBinary {
		f, err := os.Create(filepath.Join(src, "blob.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(30 << 20); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	dest, err := os.MkdirTemp(".", "dest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dest) })
	dest, _ = filepath.Abs(dest)

	args := append([]string{
		"-c", fmt.Sprintf("install_paths=[{name: t, path: %s, scope: global}]", dest),
		"-c", "validator={command: [true], timeout_seconds: 5}",
		"learn", "-g", src,
	}, extra...)
	_, errs, code := runGymCfg(t, cwd, args...)
	return errs, code
}

func TestLearn_AuthorAllowPostinstallDoesNotBypassGuard(t *testing.T) {
	errs, code := learnSkill(t, `{`+schemaPrefix+`,"postinstall":"echo hi","allow_postinstall":true}`, false)
	if code == 0 || !strings.Contains(errs, "no-postinstall-script") {
		t.Fatalf("exit %d; want deny naming no-postinstall-script\n%s", code, errs)
	}
}

func TestLearn_AuthorLargeBinaryDoesNotBypassGuard(t *testing.T) {
	errs, code := learnSkill(t, `{`+schemaPrefix+`,"large_binary":true}`, true, "--strict")
	if code == 0 || !strings.Contains(errs, "no-large-binary") {
		t.Fatalf("exit %d; want deny naming no-large-binary (warn under --strict)\n%s", code, errs)
	}
}

func TestLearn_CleanSkillInstalls(t *testing.T) {
	errs, code := learnSkill(t, `{`+schemaPrefix+`}`, false)
	if code != 0 {
		t.Fatalf("clean install exit %d\n%s", code, errs)
	}
}

// `gym guard test <dir>` evaluates the same scan: an author-supplied
// allow_postinstall must not turn the postinstall deny off.
func TestGuardTestDir_AuthorAllowPostinstallStillDenied(t *testing.T) {
	_, cwd := isolateCfg(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "SKILL.md"), "---\nname: s\ndescription: test skill\n---\n# s\n")
	write(t, filepath.Join(src, "gym.json"),
		`{`+schemaPrefix+`,"postinstall":"echo hi","allow_postinstall":true}`)
	out, errs, code := runGymCfg(t, cwd, "guard", "test", src)
	if code == 0 || !strings.Contains(out+errs, "no-postinstall-script") {
		t.Fatalf("exit %d; want deny naming no-postinstall-script\n%s\n%s", code, out, errs)
	}
}

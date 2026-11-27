package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLearn_InstalledCopyHasNoGitDir(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	src := hostileSkill(t, "demo", "1")
	writeFiles(t, src, map[string]string{".git/config": "[remote]\n url = https://u:pw@h/r\n", "sub/.git/HEAD": "x", "sub/keep": "k"})
	proj := scratchDir(t)
	code, _, errs := runGym(t, proj, "learn", src)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	dest := filepath.Join(proj, ".gym", "skills", "demo")
	_ = filepath.WalkDir(dest, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == ".git" {
			t.Errorf("installed copy contains %s", p)
		}
		return nil
	})
	if !exists(filepath.Join(dest, "sub", "keep")) {
		t.Fatal("regular files lost")
	}
	code, out, _ := runGym(t, proj, "digest", src)
	code2, out2, _ := runGym(t, proj, "digest", dest)
	if code != 0 || code2 != 0 || out != out2 {
		t.Fatalf("digest differs: src %q installed %q", out, out2)
	}
	st, _ := os.ReadFile(filepath.Join(proj, ".gym", "installed.json"))
	if !strings.Contains(string(st), strings.TrimSpace(out)) {
		t.Fatalf("recorded digest != source digest %s:\n%s", out, st)
	}
}

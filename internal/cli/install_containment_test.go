package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectConfig_SymlinkedDefaultInstallPathRefused(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	parent := scratchDir(t)
	proj := filepath.Join(parent, "proj")
	victim := filepath.Join(parent, "victim")
	writeFiles(t, victim, map[string]string{"demo/precious": "keep"})
	writeFiles(t, proj, map[string]string{".gym/config.yaml": "curation: {min_stars: 1}\n"})
	if err := os.Symlink(victim, filepath.Join(proj, ".gym", "skills")); err != nil {
		t.Skip("symlinks unavailable")
	}
	before := snapshot(t, victim)
	code, _, errs := runGym(t, proj, "learn", hostileSkill(t, "demo", "1"))
	if code == 0 || !strings.Contains(errs, "escapes project") {
		t.Fatalf("exit %d, want refusal:\n%s", code, errs)
	}
	if snapshot(t, victim) != before {
		t.Fatal("victim modified through symlinked .gym/skills")
	}
}

func TestTrain_UntrustedStateRecordsRefused(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	parent := scratchDir(t)
	proj := filepath.Join(parent, "proj")
	outside := filepath.Join(parent, "outside")
	linkTarget := filepath.Join(parent, "linktarget")
	skills := filepath.Join(proj, ".gym", "skills")

	writeFiles(t, outside, map[string]string{"evil/precious": "keep"})
	writeFiles(t, linkTarget, map[string]string{"precious": "keep"})
	writeFiles(t, skills, map[string]string{"good/VERSION": "1", "other/precious": "keep"})
	if err := os.Symlink(linkTarget, filepath.Join(skills, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}

	type rec struct{ name, path string }
	recs := []rec{
		{"good", filepath.Join(skills, "good")},
		{"evil", filepath.Join(outside, "evil")},
		{"link", filepath.Join(skills, "link")},
		{"wb", filepath.Join(skills, "other")},
	}
	var rows, idx []string
	for _, r := range recs {
		src := hostileSkill(t, r.name, "2")
		rows = append(rows, fmt.Sprintf(`{"name":%q,"version":"1.0.0","source_url":%q,"install_path":%q}`, r.name, src, r.path))
		idx = append(idx, fmt.Sprintf(`{"name":%q,"version":"2.0.0","source_url":%q}`, r.name, src))
	}
	writeFiles(t, proj, map[string]string{".gym/installed.json": `{"skills":[` + strings.Join(rows, ",") + `]}`})
	writeFiles(t, filepath.Join(home, ".local", "share", "gym"), map[string]string{
		"registry-index.json": `{"skills":[` + strings.Join(idx, ",") + `]}`})

	snapOutside, snapTarget, snapOther := snapshot(t, outside), snapshot(t, linkTarget), snapshot(t, filepath.Join(skills, "other"))
	code, _, errs := runGym(t, proj, "train", "--no-global")
	if code == 0 {
		t.Fatalf("exit 0; want non-zero for refused records:\n%s", errs)
	}
	for _, r := range recs[1:] {
		if !strings.Contains(errs, "install-path-untrusted: "+r.path) {
			t.Errorf("missing refusal for %s:\n%s", r.name, errs)
		}
	}
	if strings.Contains(errs, "install-path-untrusted: "+recs[0].path) {
		t.Errorf("legitimate record refused:\n%s", errs)
	}
	if snapshot(t, outside) != snapOutside || snapshot(t, linkTarget) != snapTarget ||
		snapshot(t, filepath.Join(skills, "other")) != snapOther {
		t.Fatal("a refused record's directory was touched")
	}
	if fi, err := os.Lstat(filepath.Join(skills, "link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink record was replaced")
	}
	if b, _ := os.ReadFile(filepath.Join(skills, "good", "VERSION")); string(b) != "2" {
		t.Fatalf("legitimate record not upgraded (VERSION=%q):\n%s", b, errs)
	}
}

// A registry entry name is a single path component: a repository can add
// a registry source, and a name such as "../x" must not steer the copy or
// its RemoveAll out of the install path.
func TestLearn_RegistryNameCannotEscapeInstallPath(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	proj := scratchDir(t)
	src := hostileSkill(t, "demo", "1")
	writeFiles(t, filepath.Join(home, ".local", "share", "gym"), map[string]string{
		"registry-index.json": `{"skills":[{"name":"../evil","version":"1.0.0","source_url":"` + src + `"}]}`})
	writeFiles(t, proj, map[string]string{".gym/evil/precious": "keep", ".gym/skills/keep": ""})
	before := snapshot(t, filepath.Join(proj, ".gym", "evil"))
	code, _, errs := runGym(t, proj, "learn", "../evil")
	if code == 0 || !strings.Contains(errs, "invalid skill name") {
		t.Fatalf("exit %d, want refusal:\n%s", code, errs)
	}
	if snapshot(t, filepath.Join(proj, ".gym", "evil")) != before {
		t.Fatal("directory outside the install path was replaced")
	}
}

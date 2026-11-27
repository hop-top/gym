package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/xdg"
)

func learn(t *testing.T, home string, dir, dest string, extra ...string) (int, string) {
	t.Helper()
	args := append([]string{"learn", dir, "--global", "--path", dest}, extra...)
	code, _, errs := runGym(t, home, args...)
	return code, errs
}

// Case 21: --max-data-class overrides the configured ceiling.
func TestLearn_MaxDataClassFlagWinsOverConfig(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "admission:\n  max_data_class: internal\n")
	src := admSkill(t, t.TempDir(), "lint", "", classManifest("confidential"))
	dest := scratchDir(t)

	code, errs := learn(t, home, src, dest)
	if code != 1 || !strings.Contains(errs, "admission-data-class") || installed(dest, "lint") {
		t.Fatalf("over ceiling: exit %d installed=%v\n%s", code, installed(dest, "lint"), errs)
	}
	code, errs = learn(t, home, src, dest, "--max-data-class", "confidential")
	if code != 0 || !installed(dest, "lint") {
		t.Fatalf("flag raising ceiling: exit %d\n%s", code, errs)
	}
	// and the flag can lower it
	dest2 := scratchDir(t)
	cfgHome := isolate(t)
	writeUserConfig(t, cfgHome, "admission:\n  max_data_class: special_category\n")
	src2 := admSkill(t, t.TempDir(), "lint", "", classManifest("internal"))
	code, errs = learn(t, cfgHome, src2, dest2, "--max-data-class", "public")
	if code != 1 || installed(dest2, "lint") {
		t.Fatalf("flag lowering ceiling: exit %d\n%s", code, errs)
	}
	if code, _ := learn(t, cfgHome, src2, dest2, "--max-data-class", "secret"); code != 1 {
		t.Fatal("invalid --max-data-class accepted")
	}
}

// Case 4 of decisions: exit code 1 for every rejection kind.
func TestLearn_RejectionsExitOne(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	for name, manifest := range map[string]string{
		"invalid": `{"schema":"gym.skill/v1"}`,
		"class":   classManifest("special_category"),
	} {
		src := admSkill(t, t.TempDir(), "s-"+name, "", manifest)
		if code, errs := learn(t, home, src, scratchDir(t)); code != 1 {
			t.Errorf("%s: exit %d\n%s", name, code, errs)
		}
	}
}

// Case 14 (integration): undeclared frontmatter tool is denied.
func TestLearn_UndeclaredToolDenied(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	src := admSkill(t, t.TempDir(), "lint", "Read Write", classManifest("internal", "Read"))
	dest := scratchDir(t)
	code, errs := learn(t, home, src, dest)
	if code != 1 || !strings.Contains(errs, "admission-tools-undeclared") || installed(dest, "lint") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	src = admSkill(t, t.TempDir(), "lint", "Read Bash(git:status)", classManifest("internal", "Read", "Bash(git:*)"))
	if code, errs := learn(t, home, src, dest); code != 0 || !installed(dest, "lint") {
		t.Fatalf("declared tools rejected: exit %d\n%s", code, errs)
	}
}

// Case 23: require_approval denies an unreviewed skill; approving the
// exact directory lets it through.
func TestLearn_RequireApprovalDeniesUntilApproved(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "admission:\n  require_approval: true\n")
	src := admSkill(t, t.TempDir(), "lint", "", classManifest("internal"))
	dest := scratchDir(t)
	code, errs := learn(t, home, src, dest)
	if code != 1 || !strings.Contains(errs, "admission-approval-enforced") || installed(dest, "lint") {
		t.Fatalf("unreviewed: exit %d\n%s", code, errs)
	}
	if code, _, e := runGym(t, home, "guard", "approve", src); code != 0 {
		t.Fatal(e)
	}
	if code, errs := learn(t, home, src, dest); code != 0 || !installed(dest, "lint") {
		t.Fatalf("approved: exit %d\n%s", code, errs)
	}
	// content change returns it to unreviewed
	writeFiles(t, src, map[string]string{"extra.md": "new"})
	dest2 := scratchDir(t)
	if code, errs := learn(t, home, src, dest2); code != 1 || installed(dest2, "lint") {
		t.Fatalf("changed content: exit %d\n%s", code, errs)
	}
}

func TestLearn_RevokedFailsHard(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	src := admSkill(t, t.TempDir(), "lint", "", "")
	if code, _, e := runGym(t, home, "guard", "revoke", "lint"); code != 0 {
		t.Fatal(e)
	}
	dest := scratchDir(t)
	code, errs := learn(t, home, src, dest)
	if code != 1 || !strings.Contains(errs, "approval-revoked") || installed(dest, "lint") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	// --insecure does not relax a revocation
	if code, _ := learn(t, home, src, dest, "--insecure"); code != 1 {
		t.Fatal("--insecure installed a revoked skill")
	}
}

// pinAndApprove registers dir in the registry index with its real
// digest and records an operator approval, so the only admission
// finding left in play is the one a test is about.
func pinAndApprove(t *testing.T, home, dir string) {
	t.Helper()
	d, err := install.Digest(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeIndex(t, dir, d)
	if code, _, e := runGym(t, home, "guard", "approve", dir); code != 0 {
		t.Fatal(e)
	}
}

// Case 25: no manifest under off / warn / reject, and warn with --strict.
func TestLearn_NoManifestModes(t *testing.T) {
	cases := []struct {
		name     string
		mode     string
		args     []string
		wantCode int
		wantWarn bool
	}{
		{"off", "off", nil, 0, false},
		{"warn", "warn", nil, 0, true},
		{"reject", "reject", nil, 1, false},
		{"warn strict", "warn", []string{"--strict"}, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := isolate(t)
			writeUserConfig(t, home, "admission:\n  require_manifest: '"+c.mode+"'\n")
			src := admSkill(t, t.TempDir(), "lint", "", "")
			pinAndApprove(t, home, src)
			dest := scratchDir(t)
			code, errs := learn(t, home, "lint", dest, c.args...)
			if code != c.wantCode {
				t.Fatalf("exit %d, want %d\n%s", code, c.wantCode, errs)
			}
			if c.wantCode == 0 && c.mode != "off" && !strings.Contains(errs, "admission-manifest-missing") {
				t.Errorf("expected manifest-missing warning\n%s", errs)
			}
			if c.mode == "off" && strings.Contains(errs, "admission-manifest") {
				t.Errorf("off mode produced a finding\n%s", errs)
			}
			if (c.wantCode == 0) != installed(dest, "lint") {
				t.Errorf("installed = %v with exit %d", installed(dest, "lint"), code)
			}
			if c.mode == "reject" && !strings.Contains(errs, "admission-manifest-required") {
				t.Errorf("reject mode did not name its rule\n%s", errs)
			}
		})
	}
}

// Case 26: digest and approval are checked for a skill without manifest.
func TestLearn_NoManifestStillChecksDigest(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	src := admSkill(t, t.TempDir(), "lint", "", "")
	writeIndex(t, src, "sha256:"+strings.Repeat("0", 64))
	dest := scratchDir(t)
	code, errs := learn(t, home, "lint", dest)
	if code != 1 || !strings.Contains(errs, "digest-mismatch") || installed(dest, "lint") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
}

func TestLearn_PinMatchInstallsAndRecordsDigestInState(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	src := admSkill(t, t.TempDir(), "lint", "", classManifest("internal"))
	d, _ := install.Digest(src)
	writeIndex(t, src, d)
	dest := scratchDir(t)
	if code, errs := learn(t, home, "lint", dest); code != 0 || !installed(dest, "lint") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	dirs, _ := xdg.AppDirs(config.AppName)
	skills, err := state.LoadGlobal(dirs.StateHome)
	if err != nil || len(skills) != 1 || skills[0].Digest != d || skills[0].DataClass != "internal" {
		t.Fatalf("state = %+v, %v", skills, err)
	}
}

// writeIndex registers dir in the local registry index (with its real
// digest pinned) so strict runs have no unpinned-digest warning.
func writeIndex(t *testing.T, dir string, digest ...string) registry.Index {
	t.Helper()
	dirs, err := xdg.AppDirs(config.AppName)
	if err != nil {
		t.Fatal(err)
	}
	e := registry.SkillEntry{Name: filepath.Base(dir), SourceURL: dir, Version: "1.0.0"}
	if len(digest) > 0 {
		e.Digest = digest[0]
	}
	idx := registry.Index{Skills: []registry.SkillEntry{e}}
	if err := registry.SaveIndex(dirs.DataHome, idx); err != nil {
		t.Fatal(err)
	}
	return idx
}

// Precedence through the CLI: project (clamped) < -c file < -c
// key=value < --max-data-class. -c layers are operator-trusted, so
// they may raise the ceiling over a project value.
func TestLearn_ConfigFlagRaisesCeilingOverProject(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	writeFiles(t, home, map[string]string{".gym/config.yaml": "admission:\n  max_data_class: public\n"})
	src := admSkill(t, t.TempDir(), "lint", "", classManifest("confidential"))
	dest := scratchDir(t)

	if code, errs := learn(t, home, src, dest); code != 1 || installed(dest, "lint") {
		t.Fatalf("project ceiling not applied: exit %d\n%s", code, errs)
	}
	code, errs := learn(t, home, src, dest, "-c", "admission.max_data_class=confidential")
	if code != 0 || !installed(dest, "lint") {
		t.Fatalf("-c key=value did not raise ceiling: exit %d\n%s", code, errs)
	}
}

func TestLearn_ConfigFlagFileRaisesCeilingOverProject(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	writeFiles(t, home, map[string]string{
		".gym/config.yaml": "admission:\n  max_data_class: public\n",
		"extra.yaml":       "admission:\n  max_data_class: confidential\n",
	})
	src := admSkill(t, t.TempDir(), "lint", "", classManifest("confidential"))
	dest := scratchDir(t)
	code, errs := learn(t, home, src, dest, "-c", filepath.Join(home, "extra.yaml"))
	if code != 0 || !installed(dest, "lint") {
		t.Fatalf("-c file did not raise ceiling: exit %d\n%s", code, errs)
	}
}

func TestLearn_MaxDataClassFlagWinsOverEverything(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "admission:\n  max_data_class: confidential\n")
	writeFiles(t, home, map[string]string{".gym/config.yaml": "admission:\n  max_data_class: public\n"})
	src := admSkill(t, t.TempDir(), "lint", "", classManifest("confidential"))
	dest := scratchDir(t)

	// -c key=value lowers; the flag raises back over it.
	code, errs := learn(t, home, src, dest, "-c", "admission.max_data_class=public",
		"--max-data-class", "confidential")
	if code != 0 || !installed(dest, "lint") {
		t.Fatalf("flag did not beat -c: exit %d\n%s", code, errs)
	}
	// and the flag lowers over a permissive -c.
	dest2 := scratchDir(t)
	code, _ = learn(t, home, src, dest2, "-c", "admission.max_data_class=confidential",
		"--max-data-class", "public")
	if code != 1 || installed(dest2, "lint") {
		t.Fatalf("flag did not lower over -c: exit %d", code)
	}
}

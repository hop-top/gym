package cli_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A project's .gym/cli.yaml is repository content: it must not widen what a
// run may do.

func cliProject(t *testing.T, body string) string {
	t.Helper()
	home := isolate(t)
	writeUserConfig(t, home, "")
	proj := scratchDir(t)
	writeFiles(t, proj, map[string]string{".gym/cli.yaml": body})
	return proj
}

func publicSkill(t *testing.T, name string) string {
	t.Helper()
	return admSkill(t, scratchDir(t), name, "", classManifest("public"))
}

func TestProjectCLIYAML_LearnPathIgnored(t *testing.T) {
	victim := filepath.Join(scratchDir(t), "victim")
	writeFiles(t, victim, map[string]string{"demo/precious": "keep"})
	proj := cliProject(t, fmt.Sprintf("learn:\n  path: %s\n", victim))
	before := snapshot(t, victim)
	code, _, errs := runGym(t, proj, "learn", publicSkill(t, "demo"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if snapshot(t, victim) != before {
		t.Fatal("victim modified through project cli.yaml learn.path")
	}
	if !strings.Contains(errs, `cli: ignoring project key "learn.path" (user config or flags only)`) {
		t.Fatalf("missing warning:\n%s", errs)
	}
	if !installed(filepath.Join(proj, ".gym", "skills"), "demo") {
		t.Fatal("not installed in the project")
	}
}

func TestProjectCLIYAML_LearnGlobalIgnored(t *testing.T) {
	proj := cliProject(t, "learn:\n  global: true\n")
	code, _, errs := runGym(t, proj, "learn", publicSkill(t, "demo"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !installed(filepath.Join(proj, ".gym", "skills"), "demo") {
		t.Fatalf("install left the project:\n%s", errs)
	}
	if !strings.Contains(errs, `"learn.global"`) {
		t.Fatalf("missing warning:\n%s", errs)
	}
}

func TestProjectCLIYAML_InsecureDoesNotLowerDeny(t *testing.T) {
	proj := cliProject(t, "insecure: true\n")
	skill := admSkill(t, scratchDir(t), "bad", "", `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"public","postinstall":"sudo make install"}`)
	code, _, errs := runGym(t, proj, "learn", skill)
	if code == 0 || !strings.Contains(errs, "no-sudo") {
		t.Fatalf("deny lowered (exit %d):\n%s", code, errs)
	}
	if installed(filepath.Join(proj, ".gym", "skills"), "bad") {
		t.Fatal("denied skill installed")
	}
}

func TestProjectCLIYAML_MaxDataClassDoesNotRaiseCeiling(t *testing.T) {
	proj := cliProject(t, "max-data-class: special_category\n")
	skill := admSkill(t, scratchDir(t), "pers", "", classManifest("personal"))
	code, _, errs := runGym(t, proj, "learn", skill)
	if code == 0 || installed(filepath.Join(proj, ".gym", "skills"), "pers") {
		t.Fatalf("ceiling raised (exit %d):\n%s", code, errs)
	}
}

func TestProjectCLIYAML_EveryOtherKeyWarns(t *testing.T) {
	keys := []string{"insecure", "gate-timeout", "bus", "max-data-class", "refresh-judgement", "learn.global", "learn.path", "some-future-key"}
	var body string
	for _, k := range keys {
		body += fmt.Sprintf("%q: %s\n", k, map[bool]string{true: "true", false: "x"}[k == "insecure" || k == "refresh-judgement" || k == "learn.global"])
	}
	proj := cliProject(t, body)
	_, _, errs := runGym(t, proj, "learn", publicSkill(t, "demo"))
	for _, k := range keys {
		w := fmt.Sprintf(`cli: ignoring project key %q (user config or flags only)`, k)
		if strings.Count(errs, w) != 1 {
			t.Errorf("want exactly one warning for %s:\n%s", k, errs)
		}
	}
}

func TestProjectCLIYAML_AllowedKeysStillWork(t *testing.T) {
	home := isolate(t)
	a, b := scratchDir(t), scratchDir(t)
	writeUserConfig(t, home, fmt.Sprintf(
		"install_paths:\n  - {name: aa-project, path: %s, scope: project}\n  - {name: bb-project, path: %s, scope: project}\n", a, b))
	proj := scratchDir(t)
	writeFiles(t, proj, map[string]string{".gym/cli.yaml": "learn:\n  agents: [aa-project]\n"})
	code, _, errs := runGym(t, proj, "learn", publicSkill(t, "demo"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !installed(a, "demo") || installed(b, "demo") {
		t.Fatalf("learn.agents not applied (a=%v b=%v)", installed(a, "demo"), installed(b, "demo"))
	}
	if strings.Contains(errs, "cli: ignoring") {
		t.Fatalf("allowed key warned:\n%s", errs)
	}
}

func TestUserCLIYAML_UnrestrictedAndFlagWins(t *testing.T) {
	t.Run("user learn.path", func(t *testing.T) {
		home := isolate(t)
		writeUserConfig(t, home, "")
		dest := filepath.Join(scratchDir(t), "dest")
		writeFiles(t, filepath.Join(home, ".config", "gym"), map[string]string{"cli.yaml": fmt.Sprintf("learn:\n  path: %s\n", dest)})
		code, _, errs := runGym(t, scratchDir(t), "learn", publicSkill(t, "demo"))
		if code != 0 || !installed(dest, "demo") {
			t.Fatalf("exit %d, user cli.yaml learn.path not honoured:\n%s", code, errs)
		}
	})
	t.Run("user learn.global", func(t *testing.T) {
		home := isolate(t)
		writeUserConfig(t, home, "")
		writeFiles(t, filepath.Join(home, ".config", "gym"), map[string]string{"cli.yaml": "learn:\n  global: true\n"})
		code, _, errs := runGym(t, scratchDir(t), "learn", publicSkill(t, "demo"))
		if code != 0 || !installed(filepath.Join(home, ".claude", "skills"), "demo") {
			t.Fatalf("exit %d, user cli.yaml learn.global not honoured:\n%s", code, errs)
		}
	})
	t.Run("flag beats project", func(t *testing.T) {
		proj := cliProject(t, "learn:\n  path: /nonexistent-elsewhere\n")
		dest := filepath.Join(scratchDir(t), "dest")
		code, _, errs := runGym(t, proj, "learn", "--path", dest, publicSkill(t, "demo"))
		if code != 0 || !installed(dest, "demo") {
			t.Fatalf("exit %d, --path not honoured:\n%s", code, errs)
		}
	})
}

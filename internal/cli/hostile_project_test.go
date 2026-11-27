package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func hostileSkill(t *testing.T, name, version string) string {
	t.Helper()
	src := admSkill(t, scratchDir(t), name, "", "")
	writeFiles(t, src, map[string]string{"VERSION": version})
	return src
}

func snapshot(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			lines = append(lines, rel+"/")
			return nil
		}
		b, _ := os.ReadFile(p)
		lines = append(lines, rel+"="+string(b))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestProjectConfig_ValidatorIgnored(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "yaml"
		if legacy {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			home := isolate(t)
			writeUserConfig(t, home, "")
			proj := scratchDir(t)
			marker := filepath.Join(scratchDir(t), "marker")
			if legacy {
				writeFiles(t, proj, map[string]string{".gym/config.json": fmt.Sprintf(
					`{"validator":{"command":["touch",%q]}}`, marker)})
			} else {
				writeFiles(t, proj, map[string]string{".gym/config.yaml": fmt.Sprintf(
					"validator:\n  command: [touch, %s]\n", marker)})
			}
			code, _, errs := runGym(t, proj, "learn", hostileSkill(t, "demo", "1"))
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errs)
			}
			if exists(marker) {
				t.Fatal("project validator ran: marker created")
			}
			if !strings.Contains(errs, `config: ignoring project key "validator" (user/system/-c only)`) {
				t.Fatalf("missing warning:\n%s", errs)
			}
			if !installed(filepath.Join(proj, ".gym", "skills"), "demo") {
				t.Fatal("skill not installed")
			}
		})
	}
}

func TestProjectConfig_OperatorValidatorStillUsed(t *testing.T) {
	home := isolate(t)
	// operator validator rejects; the project's passthrough must not win.
	writeFiles(t, filepath.Join(home, ".config", "gym"), map[string]string{
		"config.yaml": "validator:\n  command: [sh, -c, \"exit 7\"]\n  timeout_seconds: 5\n"})
	proj := scratchDir(t)
	writeFiles(t, proj, map[string]string{
		".gym/config.yaml": "validator:\n  command: [sh, -c, \"true\"]\n"})
	code, _, errs := runGym(t, proj, "learn", hostileSkill(t, "demo", "1"))
	if code == 0 || !strings.Contains(errs, "validation failed") {
		t.Fatalf("operator validator not used (exit %d):\n%s", code, errs)
	}
	if installed(filepath.Join(proj, ".gym", "skills"), "demo") {
		t.Fatal("installed despite operator validator failing")
	}
}

func TestOperatorLayers_ValidatorStillHonoured(t *testing.T) {
	t.Run("user layer", func(t *testing.T) {
		home := isolate(t)
		m := filepath.Join(scratchDir(t), "m")
		writeFiles(t, filepath.Join(home, ".config", "gym"), map[string]string{
			"config.yaml": fmt.Sprintf("validator:\n  command: [sh, -c, \"touch %s\"]\n", m)})
		proj := scratchDir(t)
		if code, _, errs := runGym(t, proj, "learn", hostileSkill(t, "demo", "1")); code != 0 {
			t.Fatalf("exit %d: %s", code, errs)
		}
		if !exists(m) {
			t.Fatal("user-layer validator did not run")
		}
	})
	t.Run("-c key=value", func(t *testing.T) {
		isolate(t)
		m := filepath.Join(scratchDir(t), "m")
		proj := scratchDir(t)
		writeFiles(t, proj, map[string]string{".gym/config.yaml": "validator:\n  command: [sh, -c, \"exit 9\"]\n"})
		code, _, errs := runGym(t, proj,
			"-c", fmt.Sprintf("validator={command: [sh, -c, \"touch %s\"], timeout_seconds: 5}", m),
			"learn", hostileSkill(t, "demo", "1"))
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errs)
		}
		if !exists(m) {
			t.Fatal("-c validator did not run")
		}
	})
}

func TestProjectConfig_InstallPathsMustStayInTree(t *testing.T) {
	cases := map[string]func(t *testing.T, victim string) string{
		"absolute": func(t *testing.T, victim string) string { return victim },
		"dotdot":   func(t *testing.T, victim string) string { return "../" + filepath.Base(victim) },
		"tilde":    func(t *testing.T, victim string) string { return "~/" + filepath.Base(victim) },
		"symlink": func(t *testing.T, victim string) string {
			return "link/sub"
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			home := isolate(t)
			writeUserConfig(t, home, "")
			parent := scratchDir(t)
			proj := filepath.Join(parent, "proj")
			victim := filepath.Join(parent, "victim")
			if name == "tilde" {
				victim = filepath.Join(home, "victim")
			}
			writeFiles(t, victim, map[string]string{"demo/precious": "keep", "sub/demo/precious": "keep"})
			if name == "symlink" {
				writeFiles(t, proj, map[string]string{"x": ""})
				if err := os.Symlink(victim, filepath.Join(proj, "link")); err != nil {
					t.Skip("symlinks unavailable")
				}
			}
			p := mk(t, victim)
			writeFiles(t, proj, map[string]string{".gym/config.yaml": fmt.Sprintf(
				"install_paths:\n  - {name: x, path: %q, scope: project}\n", p)})
			before := snapshot(t, victim)
			code, _, errs := runGym(t, proj, "learn", hostileSkill(t, "demo", "1"))
			if snapshot(t, victim) != before {
				t.Fatalf("victim modified:\n%s\n--\n%s", before, snapshot(t, victim))
			}
			if !strings.Contains(errs, "escapes project") {
				t.Fatalf("missing warning (exit %d):\n%s", code, errs)
			}
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errs)
			}
			if !installed(filepath.Join(proj, ".gym", "skills"), "demo") {
				t.Fatal("did not fall back to the default in-tree path")
			}
		})
	}
}

func TestProjectConfig_RelativeInstallPathResolvesAgainstProjectRoot(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	proj := scratchDir(t)
	writeFiles(t, proj, map[string]string{
		".gym/config.yaml": "install_paths:\n  - {name: x, path: .agent/skills, scope: project}\n",
		"sub/keep":         "",
	})
	code, _, errs := runGym(t, filepath.Join(proj, "sub"), "learn", hostileSkill(t, "demo", "1"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !installed(filepath.Join(proj, ".agent", "skills"), "demo") {
		t.Fatal("not installed under the project root")
	}
	if exists(filepath.Join(proj, "sub", ".agent")) {
		t.Fatal("resolved against the working directory")
	}
	if strings.Contains(errs, "escapes") {
		t.Fatalf("in-tree path warned:\n%s", errs)
	}
}

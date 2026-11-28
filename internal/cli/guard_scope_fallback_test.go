package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/xdg"
)

// A scope whose engine cannot be built (injected: repository policy files
// are skipped, so only I/O or user-config failures remain) is gated by the
// start-chain engine, never by no gate: the root's deny still applies to it,
// with a warning naming the scope and cause; under --strict it is denied as
// guard unavailable for that scope.
func TestTrainRecursive_UnbuildableScopeEngineFallsBackToStartChain(t *testing.T) {
	setup := func(t *testing.T) (root, broken string, app func(args ...string) (int, string)) {
		home := isolateInternal(t)
		internalConfig(t, home, "")
		root = filepath.Join(home, "org")
		broken = filepath.Join(root, "broken")
		src := skillTree(t, "lint", "")
		dirs, _ := xdg.AppDirs(config.AppName)
		if err := xdg.Ensure(dirs); err != nil {
			t.Fatal(err)
		}
		if err := registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{{Name: "lint", Version: "1.1.0", SourceURL: src}}}); err != nil {
			t.Fatal(err)
		}
		for _, d := range []string{root, broken} {
			old := filepath.Join(d, ".gym", "skills", "lint")
			if err := os.MkdirAll(old, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, "SKILL.md"), []byte("---\nname: lint\ndescription: old\n---\nOLD\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := state.SaveProject(d, []state.InstalledSkill{{Name: "lint", Version: "1.0.0", SourceURL: src, InstallPath: old}}); err != nil {
				t.Fatal(err)
			}
		}
		pol := filepath.Join(root, ".gym", "policies")
		if err := os.MkdirAll(pol, 0o755); err != nil {
			t.Fatal(err)
		}
		deny := "when: 'has(payload.version) && payload.version == \"1.1.0\"'\neffect: deny\notherwise: allow\nseverity: deny\nmessage: root denies\n"
		if err := os.WriteFile(filepath.Join(pol, "root-deny.yaml"), []byte(deny), 0o644); err != nil {
			t.Fatal(err)
		}
		return root, broken, func(args ...string) (int, string) {
			var out, errb bytes.Buffer
			a := NewApp(&out, &errb, root)
			a.engineBuild = func(roots []string) (*guard.Engine, error) {
				for _, r := range roots {
					if r == broken {
						return nil, os.ErrPermission
					}
				}
				return a.buildGuardEngine(roots)
			}
			return a.Run(append([]string{"--offline"}, args...)), errb.String()
		}
	}
	skill := func(dir string) string {
		b, _ := os.ReadFile(filepath.Join(dir, ".gym", "skills", "lint", "SKILL.md"))
		return string(b)
	}

	t.Run("warns and gates on the start chain", func(t *testing.T) {
		root, broken, run := setup(t)
		code, errs := run("train", "-r")
		if code != 1 {
			t.Fatalf("exit %d, want 1:\n%s", code, errs)
		}
		if !strings.Contains(errs, "guard unavailable for scope "+broken) {
			t.Errorf("no warning naming the scope:\n%s", errs)
		}
		if !strings.Contains(skill(broken), "OLD") || !strings.Contains(skill(root), "OLD") {
			t.Errorf("an upgrade passed the root's deny policy\n%s", errs)
		}
		denied := false
		for _, l := range strings.Split(errs, "\n") {
			if strings.Contains(l, "scope "+broken+":") && strings.Contains(l, "root-deny") {
				denied = true
			}
		}
		if !denied {
			t.Errorf("root policy did not deny the fallback scope itself:\n%s", errs)
		}
	})
	t.Run("denied under strict", func(t *testing.T) {
		_, broken, run := setup(t)
		code, errs := run("--strict", "train", "-r")
		if code != 1 || !strings.Contains(errs, "guard unavailable for scope "+broken) {
			t.Fatalf("exit %d:\n%s", code, errs)
		}
	})
}

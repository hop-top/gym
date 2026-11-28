package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/xdg"
)

// denyNew denies any upgrade to lint 1.1.0 at the per-skill gate (the plan
// gate carries no version, so only the content-aware gate trips it). The
// policy name is the file name, so a scope can tell which one denied.
const denyNew = "when: 'has(payload.version) && payload.version == \"1.1.0\"'\n" +
	"effect: deny\notherwise: allow\nseverity: deny\nmessage: lint 1.1.0 is not allowed here\n"

func scopePolicy(t *testing.T, scopeDir, file, body string) {
	t.Helper()
	writeFiles(t, filepath.Join(scopeDir, ".gym", "policies"), map[string]string{file: body})
}

// trainFixture seeds an index and returns the source dir plus a seeder that
// leaves each named scope holding lint 1.0.0 (old content).
func trainFixture(t *testing.T) (home string, seed func(dir string) string) {
	t.Helper()
	home = isolate(t)
	writeUserConfig(t, home, "")
	src := admSkill(t, scratchDir(t), "lint", "", "")
	seedIndex(t, src)
	return home, func(dir string) string { return seedTrainScope(t, dir, src, "") }
}

func upgraded(old string) bool { return !strings.Contains(readSkill(old), "OLD") }

// A nested scope's own policy gates its own upgrade in a recursive run from
// the root: the denied scope keeps its skill, the sibling without the policy
// still upgrades, and the run exits 1 at the end.
func TestTrainRecursive_NestedScopePolicyDeniesItsOwnUpgrade(t *testing.T) {
	home, seed := trainFixture(t)
	root := filepath.Join(home, "org")
	denied, sibling := filepath.Join(root, "unit", "proj"), filepath.Join(root, "unit", "sib")
	oldRoot, oldDenied, oldSib := seed(root), seed(denied), seed(sibling)
	scopePolicy(t, denied, "no-new-lint.yaml", denyNew)

	code, _, errs := runGym(t, root, "train", "-r")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, errs)
	}
	if upgraded(oldDenied) {
		t.Errorf("nested scope's own deny policy did not stop its upgrade\n%s", errs)
	}
	if !upgraded(oldRoot) || !upgraded(oldSib) {
		t.Errorf("scopes without the policy were not upgraded (root=%v sibling=%v)\n%s", upgraded(oldRoot), upgraded(oldSib), errs)
	}
	if !strings.Contains(errs, "no-new-lint") || !strings.Contains(errs, denied) {
		t.Errorf("denial not reported for the nested scope:\n%s", errs)
	}
}

// A policy defined at an intermediate scope applies to the scopes below it
// and not to its siblings.
func TestTrainRecursive_IntermediatePolicyAppliesBelowNotToSiblings(t *testing.T) {
	home, seed := trainFixture(t)
	root := filepath.Join(home, "org")
	unit, p1, p2 := filepath.Join(root, "unit"), filepath.Join(root, "unit", "p1"), filepath.Join(root, "unit", "p2")
	side := filepath.Join(root, "side")
	olds := map[string]string{}
	for _, d := range []string{root, unit, p1, p2, side} {
		olds[d] = seed(d)
	}
	scopePolicy(t, unit, "no-new-lint.yaml", denyNew)

	code, _, errs := runGym(t, root, "train", "-r")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, errs)
	}
	for _, d := range []string{unit, p1, p2} {
		if upgraded(olds[d]) {
			t.Errorf("%s was upgraded despite the unit policy\n%s", d, errs)
		}
	}
	for _, d := range []string{root, side} {
		if !upgraded(olds[d]) {
			t.Errorf("%s (outside the unit) was blocked by the unit policy\n%s", d, errs)
		}
	}
}

// The root's policies still apply to every nested scope: a root deny denies
// nested upgrades, with or without a policy of their own.
func TestTrainRecursive_RootPolicyStillAppliesToNested(t *testing.T) {
	home, seed := trainFixture(t)
	root := filepath.Join(home, "org")
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b", "deep")
	olds := map[string]string{}
	for _, d := range []string{root, a, b} {
		olds[d] = seed(d)
	}
	scopePolicy(t, root, "no-new-lint.yaml", denyNew)
	scopePolicy(t, a, "other.yaml", "when: 'false'\neffect: deny\notherwise: allow\nseverity: deny\n")

	code, _, errs := runGym(t, root, "train", "-r")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, errs)
	}
	for d, old := range olds {
		if upgraded(old) {
			t.Errorf("%s upgraded despite the root deny policy\n%s", d, errs)
		}
	}
}

// Plain `gym train` inside a nested scope and `train --dir <nested>` from
// elsewhere keep gating on that scope's chain; without -r the root run
// leaves nested scopes alone.
func TestTrain_PlainAndDirRunsKeepScopePolicies(t *testing.T) {
	home, seed := trainFixture(t)
	root := filepath.Join(home, "org")
	nested := filepath.Join(root, "unit", "proj")
	oldRoot, oldNested := seed(root), seed(nested)
	scopePolicy(t, nested, "no-new-lint.yaml", denyNew)

	if code, _, errs := runGym(t, nested, "train"); code != 1 || upgraded(oldNested) {
		t.Fatalf("train inside nested: exit %d upgraded=%v\n%s", code, upgraded(oldNested), errs)
	}
	if code, _, errs := runGym(t, home, "train", "--dir", nested); code != 1 || upgraded(oldNested) {
		t.Fatalf("train --dir nested: exit %d upgraded=%v\n%s", code, upgraded(oldNested), errs)
	}
	if code, _, errs := runGym(t, root, "train"); code != 0 || !upgraded(oldRoot) || upgraded(oldNested) {
		t.Fatalf("train at root: exit %d root=%v nested=%v\n%s", code, upgraded(oldRoot), upgraded(oldNested), errs)
	}
}

// `learn --dir <nested>` run from elsewhere gates on the nested scope's
// chain: the engine follows --dir, not the current directory.
func TestLearnDir_GatesOnTheNamedScopesPolicies(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	root := filepath.Join(home, "org")
	nested := filepath.Join(root, "unit", "proj")
	scopeTree(t, root)
	scopeTree(t, nested)
	scopePolicy(t, nested, "no-lint.yaml",
		"when: 'payload.skill == \"lint\"'\neffect: deny\notherwise: allow\nseverity: deny\nmessage: no lint here\n")
	src := admSkill(t, scratchDir(t), "lint", "", "")

	code, _, errs := runGym(t, home, "learn", "--dir", nested, src)
	if code != 1 || !strings.Contains(errs, "no-lint") || installed(filepath.Join(nested, ".gym", "skills"), "lint") {
		t.Fatalf("learn --dir nested: exit %d\n%s", code, errs)
	}
	code, _, errs = runGym(t, home, "learn", "--dir", root, src)
	if code != 0 || !installed(filepath.Join(root, ".gym", "skills"), "lint") {
		t.Fatalf("learn --dir root (control): exit %d\n%s", code, errs)
	}
}

// The global mirror upgraded alongside a nested scope is gated by that
// scope's chain too, not the root's.
func TestTrainRecursive_GlobalMirrorGatedByNestedScope(t *testing.T) {
	home := isolate(t)
	gdest := scratchDir(t)
	writeUserConfig(t, home, "install_paths:\n  - {name: g, path: "+gdest+", scope: global}\n")
	src := admSkill(t, scratchDir(t), "lint", "", "")
	seedIndex(t, src)

	root := filepath.Join(home, "org")
	nested := filepath.Join(root, "proj")
	// The root holds another skill only, so it plans nothing for `lint`.
	other := filepath.Join(root, ".gym", "skills", "other")
	writeFiles(t, other, map[string]string{"SKILL.md": "---\nname: other\ndescription: o\n---\nx\n"})
	if err := state.SaveProject(root, []state.InstalledSkill{{Name: "other", Version: "1.0.0", SourceURL: src, InstallPath: other}}); err != nil {
		t.Fatal(err)
	}
	// The nested project copy is already current: only the global mirror upgrades.
	cur := filepath.Join(nested, ".gym", "skills", "lint")
	writeFiles(t, cur, map[string]string{"SKILL.md": "---\nname: lint\ndescription: new\n---\nNEW\n"})
	if err := state.SaveProject(nested, []state.InstalledSkill{{Name: "lint", Version: "1.1.0", SourceURL: src, InstallPath: cur}}); err != nil {
		t.Fatal(err)
	}
	gold := filepath.Join(gdest, "lint")
	writeFiles(t, gold, map[string]string{"SKILL.md": "---\nname: lint\ndescription: old\n---\nOLD\n"})
	dirs, _ := xdg.AppDirs(config.AppName)
	if err := state.SaveGlobal(dirs.StateHome, []state.InstalledSkill{{Name: "lint", Version: "1.0.0", SourceURL: src, InstallPath: gold}}); err != nil {
		t.Fatal(err)
	}
	scopePolicy(t, nested, "no-new-lint.yaml", denyNew)

	code, _, errs := runGym(t, root, "train", "-r")
	if code != 1 || upgraded(gold) {
		t.Fatalf("global mirror escaped the nested scope's policy: exit %d upgraded=%v\n%s", code, upgraded(gold), errs)
	}
}

// A nested scope whose policy files fail to load is "guard unavailable" for
// that scope only: a warning without --strict (the upgrade proceeds, as for
// a guard that failed to start), a deny under --strict. Other scopes are
// unaffected.
func TestTrainRecursive_BrokenNestedPolicyIsGuardUnavailableForThatScope(t *testing.T) {
	setup := func(t *testing.T) (root, broken string, olds map[string]string) {
		home, seed := trainFixture(t)
		root = filepath.Join(home, "org")
		broken = filepath.Join(root, "broken")
		good := filepath.Join(root, "good")
		olds = map[string]string{broken: seed(broken), good: seed(good)}
		scopePolicy(t, broken, "bad.yaml", "name: [unclosed\n")
		return root, broken, olds
	}

	t.Run("warns without strict", func(t *testing.T) {
		root, broken, olds := setup(t)
		code, _, errs := runGym(t, root, "train", "-r")
		if code != 0 {
			t.Fatalf("exit %d, want 0 (fail-open):\n%s", code, errs)
		}
		if !strings.Contains(errs, "guard unavailable") || !strings.Contains(errs, "bad.yaml") {
			t.Errorf("no warning naming the cause:\n%s", errs)
		}
		for d, old := range olds {
			if !upgraded(old) {
				t.Errorf("%s not upgraded\n%s", d, errs)
			}
		}
		_ = broken
	})

	t.Run("denies under strict", func(t *testing.T) {
		root, broken, olds := setup(t)
		code, _, errs := runGym(t, root, "--strict", "train", "-r")
		if code != 1 {
			t.Fatalf("exit %d, want 1:\n%s", code, errs)
		}
		if upgraded(olds[broken]) {
			t.Errorf("broken scope upgraded under --strict\n%s", errs)
		}
		var line string
		for _, l := range strings.Split(errs, "\n") {
			if strings.Contains(l, broken) && strings.Contains(l, "guard unavailable") && strings.Contains(l, "bad.yaml") {
				line = l
			}
		}
		if line == "" {
			t.Errorf("no per-scope denial naming the cause:\n%s", errs)
		}
		for _, l := range strings.Split(errs, "\n") {
			if strings.Contains(l, filepath.Join(root, "good")) && strings.Contains(l, "guard unavailable") {
				t.Errorf("healthy scope reported as guard unavailable: %s", l)
			}
		}
	})
}

// An ancestor scope skipped by the ownership/mode check contributes no
// policies to the scopes below it, as when running inside them.
func TestTrainRecursive_UntrustedIntermediatePolicyContributesNothing(t *testing.T) {
	home, seed := trainFixture(t)
	root := filepath.Join(home, "org")
	unit, proj := filepath.Join(root, "unit"), filepath.Join(root, "unit", "proj")
	seed(root)
	seed(unit)
	oldProj := seed(proj)
	scopePolicy(t, unit, "no-new-lint.yaml", denyNew)
	if err := os.Chmod(filepath.Join(unit, ".gym"), 0o777); err != nil {
		t.Fatal(err)
	}

	runGym(t, root, "train", "-r")
	if !upgraded(oldProj) {
		t.Fatal("policy of a group/world-writable ancestor scope applied to a scope below it")
	}
}

// The command's own start folder is the operator's choice and is not
// trust-checked: its policies keep gating the scopes below it in a
// recursive run, even when the ancestor check would skip it for a scope
// started inside (no smaller set than before).
func TestTrainRecursive_StartFolderPoliciesNeverDropped(t *testing.T) {
	home, seed := trainFixture(t)
	root := filepath.Join(home, "org")
	nested := filepath.Join(root, "proj")
	seed(root)
	oldNested := seed(nested)
	scopePolicy(t, root, "no-new-lint.yaml", denyNew)
	if err := os.Chmod(filepath.Join(root, ".gym"), 0o777); err != nil {
		t.Fatal(err)
	}

	runGym(t, root, "train", "-r")
	if upgraded(oldNested) {
		t.Fatal("start folder's policy stopped applying to a nested scope")
	}
}

package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/xdg"
)

// scopeTree makes root a folder scope (an empty installed.json).
func scopeTree(t *testing.T, root string) {
	t.Helper()
	writeFiles(t, root, map[string]string{".gym/installed.json": `{"skills":[]}`})
}

// learnInto installs a skill named name into the folder scope at dir.
func learnInto(t *testing.T, cwd, dir, name string) {
	t.Helper()
	src := admSkill(t, scratchDir(t), name, "", "")
	if code, _, errs := runGym(t, cwd, "learn", "--dir", dir, src); code != 0 {
		t.Fatalf("learn %s into %s: exit %d\n%s", name, dir, code, errs)
	}
}

type warmupRow struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Folder string `json:"folder"`
	Depth  int    `json:"depth"`
}

func warmupRows(t *testing.T, cwd string, args ...string) []warmupRow {
	t.Helper()
	code, out, errs := runGym(t, cwd, append([]string{"warmup", "--format", "json"}, args...)...)
	if code != 0 {
		t.Fatalf("warmup exit %d: %s", code, errs)
	}
	var rows []warmupRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("warmup json: %v\n%s", err, out)
	}
	return rows
}

// A1: learn --dir installs under that folder's .gym and leaves CWD alone.
func TestLearnDir_InstallsIntoNamedFolder(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	cwd := filepath.Join(home, "elsewhere")
	target := filepath.Join(home, "org", "unit")
	for _, d := range []string{cwd, target} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src := admSkill(t, scratchDir(t), "lint", "", "")
	code, _, errs := runGym(t, cwd, "learn", "--dir", target, src)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !installed(filepath.Join(target, ".gym", "skills"), "lint") {
		t.Fatal("skill not under <dir>/.gym/skills")
	}
	recs, err := state.LoadProject(target)
	if err != nil || len(recs) != 1 || recs[0].Name != "lint" {
		t.Fatalf("state under <dir>: %+v %v", recs, err)
	}
	if exists(filepath.Join(cwd, ".gym")) {
		t.Fatal("working directory was touched")
	}
	if strings.Contains(errs, "note:") {
		t.Fatalf("--dir is deliberate, no note expected:\n%s", errs)
	}
}

// A2: --dir must name an existing directory and excludes -g.
func TestDirFlag_Errors(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	file := filepath.Join(home, "afile")
	writeFiles(t, home, map[string]string{"afile": "x"})
	missing := filepath.Join(home, "missing")
	src := admSkill(t, scratchDir(t), "lint", "", "")

	for _, cmd := range [][]string{{"learn", src}, {"train"}, {"list"}, {"warmup"}} {
		for _, bad := range []string{missing, file} {
			args := append([]string{cmd[0], "--dir", bad}, cmd[1:]...)
			code, _, errs := runGym(t, home, args...)
			if code == 0 || !strings.Contains(errs, "--dir "+bad+": not a directory") {
				t.Errorf("%v: exit %d, want not-a-directory:\n%s", args, code, errs)
			}
		}
	}
	for _, cmd := range [][]string{{"learn", "-g", src}, {"list", "-g"}} {
		args := append([]string{cmd[0], "--dir", home}, cmd[1:]...)
		code, _, errs := runGym(t, home, args...)
		if code == 0 || !strings.Contains(errs, "--dir and --global are mutually exclusive") {
			t.Errorf("%v: exit %d, want mutual exclusion:\n%s", args, code, errs)
		}
	}
}

// A3: learn from a folder below an existing scope creates a new scope in
// the working directory and says so on stderr.
func TestLearn_NewScopeBelowRootNotes(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	root := filepath.Join(home, "org")
	sub := filepath.Join(root, "unit", "proj")
	scopeTree(t, root)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	src := admSkill(t, scratchDir(t), "lint", "", "")
	code, _, errs := runGym(t, sub, "learn", src)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := fmt.Sprintf("note: installing into new folder scope %s; nearest existing scope is %s (--dir %s to use it)", sub, root, root)
	if !strings.Contains(errs, want) {
		t.Fatalf("missing note %q in:\n%s", want, errs)
	}
	if !installed(filepath.Join(sub, ".gym", "skills"), "lint") {
		t.Fatal("not installed into the working directory")
	}
	if recs, _ := state.LoadProject(root); len(recs) != 0 {
		t.Fatalf("ancestor scope modified: %+v", recs)
	}
	// Second learn: the scope exists now, no note.
	src2 := admSkill(t, scratchDir(t), "fmt", "", "")
	if _, _, errs := runGym(t, sub, "learn", src2); strings.Contains(errs, "note:") {
		t.Fatalf("note repeated for an existing scope:\n%s", errs)
	}
}

// A7: the org ceiling still applies when the inner folder has its own
// config.yaml that is silent on admission (the nearest-file-only loader
// dropped the org file).
func TestLearn_OrgCeilingAppliesBelowSilentInnerConfig(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	org := filepath.Join(home, "org")
	proj := filepath.Join(org, "proj")
	writeFiles(t, org, map[string]string{".gym/config.yaml": "admission:\n  max_data_class: public\n"})
	writeFiles(t, proj, map[string]string{".gym/config.yaml": "curation: {min_stars: 1}\n"})
	src := admSkill(t, scratchDir(t), "lint", "", classManifest("internal"))

	code, _, errs := runGym(t, proj, "learn", src)
	if code == 0 {
		t.Fatalf("install above the org ceiling succeeded:\n%s", errs)
	}
	if installed(filepath.Join(proj, ".gym", "skills"), "lint") {
		t.Fatal("skill written despite the ceiling")
	}

	// Control: without the org ceiling the same install passes.
	if err := os.Remove(filepath.Join(org, ".gym", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runGym(t, proj, "learn", src); code != 0 {
		t.Fatalf("control install failed: %d\n%s", code, errs)
	}
}

// A11: warmup is effective by default; --no-inherit restores nearest-only.
func TestWarmup_EffectiveVersusNoInherit(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	org := filepath.Join(home, "org")
	proj := filepath.Join(org, "unit", "proj")
	sub := filepath.Join(proj, "sub")
	for _, d := range []string{org, proj, sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	learnInto(t, home, org, "org-skill")
	learnInto(t, home, proj, "proj-skill")
	learnInto(t, home, proj, "shared")
	learnInto(t, home, org, "shared")

	names := func(rows []warmupRow) string {
		var n []string
		for _, r := range rows {
			n = append(n, fmt.Sprintf("%s@%d", r.Name, r.Depth))
		}
		return strings.Join(n, ",")
	}

	rows := warmupRows(t, sub)
	if got, want := names(rows), "org-skill@1,proj-skill@0,shared@0"; got != want {
		t.Fatalf("effective = %s, want %s", got, want)
	}
	for _, r := range rows {
		wantFolder := org
		if r.Depth == 0 {
			wantFolder = proj
		}
		if r.Scope != "project" || r.Folder != wantFolder {
			t.Fatalf("row %+v: want project scope, folder %s", r, wantFolder)
		}
	}

	rows = warmupRows(t, sub, "--no-inherit")
	if got, want := names(rows), "proj-skill@0,shared@0"; got != want {
		t.Fatalf("--no-inherit = %s, want %s", got, want)
	}

	rows = warmupRows(t, home, "--dir", org)
	if got, want := names(rows), "org-skill@0,shared@0"; got != want {
		t.Fatalf("--dir org = %s, want %s", got, want)
	}
}

// warmup: a root above $HOME is not read, and an ancestor with a writable
// .gym is skipped with a warning.
func TestWarmup_IgnoresUntrustedAndAboveHome(t *testing.T) {
	parent := scratchDir(t)
	home := filepath.Join(parent, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")
	writeUserConfig(t, home, "")
	proj := filepath.Join(home, "unit", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Root above HOME, with a skill: never read.
	scopeTree(t, parent)
	learnInto(t, home, parent, "above-home")
	// Unit ancestor with a writable .gym: skipped.
	unit := filepath.Join(home, "unit")
	learnInto(t, home, unit, "planted")
	if err := os.Chmod(filepath.Join(unit, ".gym"), 0o775); err != nil {
		t.Fatal(err)
	}
	learnInto(t, home, proj, "mine")

	code, out, errs := runGym(t, proj, "warmup", "--format", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if strings.Contains(out, "above-home") || strings.Contains(out, "planted") || !strings.Contains(out, "mine") {
		t.Fatalf("effective set wrong:\n%s", out)
	}
	if !strings.Contains(errs, "scope: ignoring "+unit+"/.gym (not owned by you or writable by others)") {
		t.Fatalf("no ownership warning:\n%s", errs)
	}
}

type listRow struct {
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	Folder     string `json:"folder"`
	Depth      int    `json:"depth"`
	ShadowedBy string `json:"shadowed_by"`
}

func listRows(t *testing.T, cwd string, args ...string) []listRow {
	t.Helper()
	code, out, errs := runGym(t, cwd, append([]string{"list", "--format", "json"}, args...)...)
	if code != 0 {
		t.Fatalf("list exit %d: %s", code, errs)
	}
	var rows []listRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("list json: %v\n%s", err, out)
	}
	return rows
}

func TestList_ScopesAndEffective(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	org := filepath.Join(home, "org")
	proj := filepath.Join(org, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	learnInto(t, home, org, "shared")
	learnInto(t, home, org, "org-only")
	learnInto(t, home, proj, "shared")
	// A global skill with the same name as an org one.
	dirs, _ := xdg.AppDirs(config.AppName)
	gdir := filepath.Join(home, ".claude", "skills", "org-only")
	writeFiles(t, gdir, map[string]string{"SKILL.md": "---\nname: org-only\ndescription: g\n---\n"})
	if err := state.SaveGlobal(dirs.StateHome, []state.InstalledSkill{{Name: "org-only", Version: "1", InstallPath: gdir}}); err != nil {
		t.Fatal(err)
	}

	rows := listRows(t, proj)
	if len(rows) != 1 || rows[0].Name != "shared" || rows[0].Folder != proj || rows[0].ShadowedBy != "" {
		t.Fatalf("own scope = %+v", rows)
	}
	if rows := listRows(t, home, "--dir", org); len(rows) != 2 {
		t.Fatalf("--dir org = %+v", rows)
	}
	if rows := listRows(t, proj, "-g"); len(rows) != 1 || rows[0].Scope != "global" {
		t.Fatalf("-g = %+v", rows)
	}

	rows = listRows(t, proj, "--effective")
	got := map[string]string{}
	for _, r := range rows {
		got[fmt.Sprintf("%s|%s", r.Name, r.Scope+":"+r.Folder)] = r.ShadowedBy
	}
	want := map[string]string{
		"shared|project:" + proj:  "",
		"shared|project:" + org:   proj,
		"org-only|project:" + org: "",
		"org-only|global:":        org,
	}
	if len(got) != len(want) {
		t.Fatalf("effective = %+v", rows)
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Fatalf("effective[%s] = %q (present %v), want %q\n%+v", k, g, ok, v, rows)
		}
	}

	// Table form carries the same names.
	code, out, _ := runGym(t, proj, "list", "--effective")
	if code != 0 || !strings.Contains(out, "SHADOWED BY") || !strings.Contains(out, "org-only") {
		t.Fatalf("table:\n%s", out)
	}
}

// seedTrainScope writes a scope at dir holding lint 1.0.0 (old content),
// recorded at path (default: the scope's own install dir).
func seedTrainScope(t *testing.T, dir, src, path string) string {
	t.Helper()
	old := filepath.Join(dir, ".gym", "skills", "lint")
	writeFiles(t, old, map[string]string{"SKILL.md": "---\nname: lint\ndescription: old\n---\nOLD\n"})
	if path == "" {
		path = old
	}
	if err := state.SaveProject(dir, []state.InstalledSkill{{
		Name: "lint", Version: "1.0.0", SourceURL: src, InstallPath: path,
	}}); err != nil {
		t.Fatal(err)
	}
	return old
}

func seedIndex(t *testing.T, src string) {
	t.Helper()
	dirs, _ := xdg.AppDirs(config.AppName)
	if err := xdg.Ensure(dirs); err != nil {
		t.Fatal(err)
	}
	if err := registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{
		{Name: "lint", Version: "1.1.0", SourceURL: src},
	}}); err != nil {
		t.Fatal(err)
	}
}

func readSkill(p string) string {
	b, _ := os.ReadFile(filepath.Join(p, "SKILL.md"))
	return string(b)
}

// A12: train -r upgrades every scope below the root, a failing scope does
// not stop the others, and the exit code is 1.
func TestTrainRecursive_ContinuesPastFailingScope(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	root := filepath.Join(home, "org")
	src := admSkill(t, scratchDir(t), "lint", "", "")
	seedIndex(t, src)
	victim := filepath.Join(scratchDir(t), "victim", "lint")
	writeFiles(t, victim, map[string]string{"SKILL.md": "---\nname: lint\ndescription: victim\n---\nVICTIM\n"})

	olds := map[string]string{}
	for _, s := range []string{"a", "b", "c"} {
		path := ""
		if s == "b" {
			path = victim // hostile record: refused, never touched
		}
		olds[s] = seedTrainScope(t, filepath.Join(root, s), src, path)
	}

	code, _, errs := runGym(t, home, "train", "--dir", root, "-r")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, errs)
	}
	for _, s := range []string{"a", "c"} {
		if strings.Contains(readSkill(olds[s]), "OLD") {
			t.Errorf("scope %s not upgraded\n%s", s, errs)
		}
	}
	if !strings.Contains(readSkill(victim), "VICTIM") {
		t.Error("untrusted record's directory was touched")
	}
	if !strings.Contains(errs, "install-path-untrusted") || !strings.Contains(errs, filepath.Join(root, "b")) {
		t.Errorf("failure not reported per scope:\n%s", errs)
	}
}

// train without -r leaves nested scopes alone; --depth bounds the walk.
func TestTrain_RecursiveScopeSelection(t *testing.T) {
	home := isolate(t)
	writeUserConfig(t, home, "")
	root := filepath.Join(home, "org")
	nested := filepath.Join(root, "child")
	deep := filepath.Join(root, "x", "y", "z")
	src := admSkill(t, scratchDir(t), "lint", "", "")
	seedIndex(t, src)
	oldRoot := seedTrainScope(t, root, src, "")
	oldNested := seedTrainScope(t, nested, src, "")
	oldDeep := seedTrainScope(t, deep, src, "")
	skipped := seedTrainScope(t, filepath.Join(root, "node_modules", "pkg"), src, "")

	if code, _, errs := runGym(t, home, "train", "--dir", root); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if strings.Contains(readSkill(oldRoot), "OLD") || !strings.Contains(readSkill(oldNested), "OLD") {
		t.Fatal("without -r only the scope root is trained")
	}

	if code, _, errs := runGym(t, home, "train", "--dir", root, "-r", "--depth", "2"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if strings.Contains(readSkill(oldNested), "OLD") {
		t.Fatal("-r did not train the nested scope")
	}
	if !strings.Contains(readSkill(oldDeep), "OLD") {
		t.Fatal("--depth 2 reached a scope three levels down")
	}

	if code, _, errs := runGym(t, home, "train", "--dir", root, "-r"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if strings.Contains(readSkill(oldDeep), "OLD") {
		t.Fatal("default depth did not reach three levels down")
	}
	if !strings.Contains(readSkill(skipped), "OLD") {
		t.Fatal("node_modules was walked")
	}
}

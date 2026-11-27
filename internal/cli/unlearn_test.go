package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/lock"
)

type unlearnEnv struct {
	home, proj string
}

// newUnlearnEnv isolates HOME/XDG and creates a project under the package
// directory with two project install paths, a and b.
func newUnlearnEnv(t *testing.T) unlearnEnv {
	t.Helper()
	home := isolate(t)
	proj := filepath.Join(scratchDir(t), "proj")
	writeUserConfig(t, home, fmt.Sprintf(
		"install_paths:\n  - {name: a, path: %s, scope: project}\n  - {name: b, path: %s, scope: project}\n",
		filepath.Join(proj, ".a"), filepath.Join(proj, ".b")))
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	return unlearnEnv{home: home, proj: proj}
}

func (e unlearnEnv) learn(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if code, _, errs := runGym(t, e.proj, "learn", hostileSkill(t, n, "1")); code != 0 {
			t.Fatalf("learn %s: exit %d\n%s", n, code, errs)
		}
	}
}

func (e unlearnEnv) dirs(name string) []string {
	return []string{filepath.Join(e.proj, ".a", name), filepath.Join(e.proj, ".b", name)}
}

func (e unlearnEnv) stateFile() string { return filepath.Join(e.proj, ".gym", "installed.json") }

func (e unlearnEnv) records(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(e.stateFile())
	if err != nil {
		t.Fatalf("installed.json: %v", err)
	}
	var s struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s.Skills
}

// C1: both install dirs and both records are gone.
func TestUnlearn_C1_RemovesNamedSkillFromTwoPaths(t *testing.T) {
	e := newUnlearnEnv(t)
	e.learn(t, "lint", "keep")
	for _, d := range e.dirs("lint") {
		if !exists(d) {
			t.Fatalf("setup: %s missing", d)
		}
	}
	code, out, errs := runGym(t, e.proj, "unlearn", "lint")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	for _, d := range e.dirs("lint") {
		if exists(d) || !strings.Contains(out, d) {
			t.Errorf("%s: exists=%v, out:\n%s", d, exists(d), out)
		}
	}
	for _, d := range e.dirs("keep") {
		if !exists(d) {
			t.Errorf("unrelated skill removed: %s", d)
		}
	}
	for _, r := range e.records(t) {
		if r["name"] == "lint" {
			t.Fatalf("record kept: %v", r)
		}
	}
	if len(e.records(t)) != 2 {
		t.Fatalf("records = %v, want the 2 keep records", e.records(t))
	}
	// The test skills live outside the project, so they are never locked;
	// removal of an entry is covered by TestUnlearnRemovesLockEntry.
	if f, err := lock.Load(lock.ProjectPath(e.proj), lock.ScopeProject); err == nil {
		if _, ok := f.Find("lint"); ok {
			t.Fatalf("lock entry kept: %+v", f.Skills)
		}
	}
}

func TestUnlearn_AliasesAndNoArgs(t *testing.T) {
	e := newUnlearnEnv(t)
	for _, alias := range []string{"uninstall", "remove", "rm"} {
		e.learn(t, "lint")
		if code, _, errs := runGym(t, e.proj, alias, "lint"); code != 0 {
			t.Fatalf("%s: exit %d\n%s", alias, code, errs)
		}
		if exists(e.dirs("lint")[0]) {
			t.Fatalf("%s did not remove", alias)
		}
	}
	if code, _, _ := runGym(t, e.proj, "unlearn"); code == 0 {
		t.Fatal("no arguments must fail")
	}
}

func TestUnlearn_C2_DryRunChangesNothing(t *testing.T) {
	e := newUnlearnEnv(t)
	e.learn(t, "lint")
	before, stBefore := snapshot(t, e.proj), snapshotFile(t, e.stateFile())
	for _, flag := range []string{"-n", "--dry-run"} {
		code, out, errs := runGym(t, e.proj, "unlearn", flag, "lint")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, errs)
		}
		for _, d := range e.dirs("lint") {
			if !strings.Contains(out, "would remove lint "+d) {
				t.Errorf("listing lacks %s:\n%s", d, out)
			}
		}
	}
	if snapshot(t, e.proj) != before || snapshotFile(t, e.stateFile()) != stBefore {
		t.Fatal("dry-run changed the tree")
	}
}

func snapshotFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUnlearn_C3_AbsentSkill(t *testing.T) {
	e := newUnlearnEnv(t)
	e.learn(t, "other")
	code, _, errs := runGym(t, e.proj, "unlearn", "lint")
	if code != 1 || !strings.Contains(errs, "skill-not-installed: lint in "+e.proj) {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if code, _, errs := runGym(t, e.proj, "unlearn", "--ignore-missing", "lint"); code != 0 {
		t.Fatalf("--ignore-missing: exit %d\n%s", code, errs)
	}
	// ignore-missing does not hide real failures of other names
	if code, _, _ := runGym(t, e.proj, "unlearn", "--ignore-missing", "lint", "other"); code != 0 {
		t.Fatal("present + absent with --ignore-missing must exit 0")
	}
}

func TestUnlearn_C3_HintsOtherScope(t *testing.T) {
	e := newUnlearnEnv(t)
	if code, _, errs := runGym(t, e.proj, "learn", "-g", hostileSkill(t, "lint", "1")); code != 0 {
		t.Fatalf("learn -g: exit %d\n%s", code, errs)
	}
	code, _, errs := runGym(t, e.proj, "unlearn", "lint")
	if code != 1 || !strings.Contains(errs, "(installed in: global)") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if code, _, errs := runGym(t, e.proj, "unlearn", "-g", "lint"); code != 0 {
		t.Fatalf("unlearn -g: exit %d\n%s", code, errs)
	}
}

func TestUnlearn_C4_SecondRunIsNotInstalled(t *testing.T) {
	e := newUnlearnEnv(t)
	e.learn(t, "lint", "keep")
	if code, _, errs := runGym(t, e.proj, "unlearn", "lint"); code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	after := snapshot(t, e.proj)
	code, _, errs := runGym(t, e.proj, "unlearn", "lint")
	if code != 1 || !strings.Contains(errs, "skill-not-installed") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if snapshot(t, e.proj) != after {
		t.Fatal("second run changed the tree")
	}
}

// hostile state: a record outside the tree, one through a symlink, one
// with a wrong base name, next to a legitimate record.
type hostileEnv struct {
	unlearnEnv
	victims map[string]string // record name -> victim dir
	recs    map[string]string // record name -> install_path
}

func newHostileEnv(t *testing.T) hostileEnv {
	t.Helper()
	e := newUnlearnEnv(t)
	parent := filepath.Dir(e.proj)
	outside := filepath.Join(parent, "outside")
	linkTarget := filepath.Join(parent, "linktarget")
	a := filepath.Join(e.proj, ".a")
	writeFiles(t, outside, map[string]string{"evil/precious": "keep"})
	writeFiles(t, linkTarget, map[string]string{"precious": "keep"})
	writeFiles(t, a, map[string]string{"good/SKILL.md": "x", "other/precious": "keep"})
	if err := os.Symlink(linkTarget, filepath.Join(a, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	h := hostileEnv{unlearnEnv: e,
		victims: map[string]string{"evil": outside, "link": linkTarget, "wb": filepath.Join(a, "other")},
		recs: map[string]string{
			"good": filepath.Join(a, "good"), "evil": filepath.Join(outside, "evil"),
			"link": filepath.Join(a, "link"), "wb": filepath.Join(a, "other"),
		}}
	var rows []string
	for _, n := range []string{"good", "evil", "link", "wb"} {
		rows = append(rows, fmt.Sprintf(`{"name":%q,"version":"1","install_path":%q}`, n, h.recs[n]))
	}
	writeFiles(t, e.proj, map[string]string{".gym/installed.json": `{"skills":[` + strings.Join(rows, ",") + `]}`})
	return h
}

func (h hostileEnv) snapshots(t *testing.T) map[string]string {
	m := map[string]string{}
	for n, v := range h.victims {
		m[n] = snapshot(t, v)
	}
	return m
}

func TestUnlearn_C5_HostileRecordsRefusedVictimIntact(t *testing.T) {
	h := newHostileEnv(t)
	before := h.snapshots(t)
	code, _, errs := runGym(t, h.proj, "unlearn", "--force", "evil", "link", "wb")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, errs)
	}
	for _, n := range []string{"evil", "link", "wb"} {
		if !strings.Contains(errs, "install-path-untrusted: "+h.recs[n]) {
			t.Errorf("missing refusal for %s:\n%s", n, errs)
		}
	}
	for n, s := range h.snapshots(t) {
		if s != before[n] {
			t.Errorf("victim of %s modified", n)
		}
	}
	if fi, err := os.Lstat(h.recs["link"]); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink record was removed or replaced")
	}
	if len(h.records(t)) != 4 {
		t.Fatalf("refused records must stay in state: %v", h.records(t))
	}
}

// The legitimate record is removed even when another record is refused.
func TestUnlearn_C5_RefusalDoesNotBlockOthers(t *testing.T) {
	h := newHostileEnv(t)
	code, _, errs := runGym(t, h.proj, "unlearn", "--force", "*", "--yes")
	if code != 1 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if exists(h.recs["good"]) {
		t.Fatal("legitimate record not removed")
	}
	names := map[string]bool{}
	for _, r := range h.records(t) {
		names[r["name"].(string)] = true
	}
	if names["good"] || !names["evil"] || !names["link"] || !names["wb"] {
		t.Fatalf("records = %v", names)
	}
}

func TestUnlearn_C6_ModifiedNeedsForce(t *testing.T) {
	e := newUnlearnEnv(t)
	e.learn(t, "lint")
	edited := filepath.Join(e.dirs("lint")[0], "SKILL.md")
	if err := os.WriteFile(edited, []byte("locally edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runGym(t, e.proj, "unlearn", "lint")
	if code != 1 || !strings.Contains(errs, "skill-modified: "+e.dirs("lint")[0]+" differs from recorded sha256:") ||
		!strings.Contains(errs, "--force to remove") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if !exists(e.dirs("lint")[0]) || len(e.records(t)) != 1 {
		t.Fatal("modified skill was removed or its record dropped")
	}
	// the unmodified copy in b is removed even though a was refused
	if exists(e.dirs("lint")[1]) {
		t.Fatal("unmodified copy not removed")
	}
	if code, _, errs := runGym(t, e.proj, "unlearn", "--force", "lint"); code != 0 {
		t.Fatalf("--force: exit %d\n%s", code, errs)
	}
	if exists(e.dirs("lint")[0]) || len(e.records(t)) != 0 {
		t.Fatal("--force did not remove")
	}
}

func TestUnlearn_C6_NoRecordedDigestNeedsForce(t *testing.T) {
	e := newUnlearnEnv(t)
	dir := filepath.Join(e.proj, ".a", "old")
	writeFiles(t, dir, map[string]string{"SKILL.md": "x"})
	writeFiles(t, e.proj, map[string]string{".gym/installed.json": fmt.Sprintf(
		`{"skills":[{"name":"old","version":"1","install_path":%q}]}`, dir)})
	code, _, errs := runGym(t, e.proj, "unlearn", "old")
	if code != 1 || !strings.Contains(errs, "skill-modified") || !strings.Contains(errs, "no recorded digest") || !exists(dir) {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if code, _, errs := runGym(t, e.proj, "unlearn", "--force", "old"); code != 0 || exists(dir) {
		t.Fatalf("--force: exit %d\n%s", code, errs)
	}
}

func TestUnlearn_C9_StateFileStaysWithEmptyList(t *testing.T) {
	e := newUnlearnEnv(t)
	e.learn(t, "lint")
	if code, _, errs := runGym(t, e.proj, "unlearn", "lint"); code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	b, err := os.ReadFile(e.stateFile())
	if err != nil {
		t.Fatalf("installed.json removed: %v", err)
	}
	var s map[string]json.RawMessage
	if err := json.Unmarshal(b, &s); err != nil || strings.ReplaceAll(string(s["skills"]), " ", "") != "[]" {
		t.Fatalf("state = %s (%v)", b, err)
	}
}

func TestUnlearn_AgentAndPathNarrowRecords(t *testing.T) {
	e := newUnlearnEnv(t)
	e.learn(t, "lint")
	if code, _, errs := runGym(t, e.proj, "unlearn", "--agent", "a", "lint"); code != 0 {
		t.Fatalf("--agent: exit %d\n%s", code, errs)
	}
	if exists(e.dirs("lint")[0]) || !exists(e.dirs("lint")[1]) || len(e.records(t)) != 1 {
		t.Fatal("--agent a must remove only the a copy")
	}
	if code, _, errs := runGym(t, e.proj, "unlearn", "--path", "a", "lint"); code != 1 || !strings.Contains(errs, "skill-not-installed") {
		t.Fatalf("--path a: exit %d\n%s", code, errs)
	}
	if code, _, errs := runGym(t, e.proj, "unlearn", "--path", "b", "lint"); code != 0 || exists(e.dirs("lint")[1]) {
		t.Fatalf("--path b: exit %d\n%s", code, errs)
	}
}

// C11: train on the hostile state file refuses the same records.
func TestUnlearn_C11_TrainRefusesHostileStateFile(t *testing.T) {
	h := newHostileEnv(t)
	var idx []string
	for _, n := range []string{"good", "evil", "link", "wb"} {
		idx = append(idx, fmt.Sprintf(`{"name":%q,"version":"2.0.0","source_url":%q}`, n, hostileSkill(t, n, "2")))
	}
	writeFiles(t, filepath.Join(h.home, ".local", "share", "gym"), map[string]string{
		"registry-index.json": `{"skills":[` + strings.Join(idx, ",") + `]}`})
	before := h.snapshots(t)
	code, _, errs := runGym(t, h.proj, "train", "--no-global")
	if code == 0 {
		t.Fatalf("exit 0; want refusal\n%s", errs)
	}
	for _, n := range []string{"evil", "link", "wb"} {
		if !strings.Contains(errs, "install-path-untrusted: "+h.recs[n]) {
			t.Errorf("train did not refuse %s:\n%s", n, errs)
		}
	}
	for n, s := range h.snapshots(t) {
		if s != before[n] {
			t.Errorf("train modified the victim of %s", n)
		}
	}
}

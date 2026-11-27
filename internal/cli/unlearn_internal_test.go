package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
)

// unlearnProj isolates HOME/XDG and returns a project dir with two
// project install paths, a and b, configured.
func unlearnProj(t *testing.T) string {
	t.Helper()
	home := isolateInternal(t)
	proj := filepath.Join(scratch(t), "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".config", "gym", "config.yaml")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "install_paths:\n  - {name: a, path: %s, scope: project}\n  - {name: b, path: %s, scope: project}\n",
		filepath.Join(proj, ".a"), filepath.Join(proj, ".b"))
	_ = f.Close()
	return proj
}

func runApp(app *App, args ...string) int { return app.Run(append([]string{"--offline"}, args...)) }

func learnInto(t *testing.T, proj string, names ...string) {
	t.Helper()
	for _, n := range names {
		app := NewApp(&bytes.Buffer{}, &bytes.Buffer{}, proj)
		if code := runApp(app, "learn", skillTree(t, n, "")); code != 0 {
			t.Fatalf("learn %s: exit %d", n, code)
		}
	}
}

// C7: one install path cannot be removed. The failure is injected through
// the remover variable so the case proves the ordering on every platform
// (root ignores read-only directories). The other path is removed, the
// failed record stays, exit 1.
func TestUnlearn_C7_FailedPathKeepsRecordOthersRemoved(t *testing.T) {
	proj := unlearnProj(t)
	learnInto(t, proj, "lint")
	failing := filepath.Join(proj, ".a", "lint")
	orig := unlearnRemoveAll
	t.Cleanup(func() { unlearnRemoveAll = orig })
	unlearnRemoveAll = func(p string) error {
		if p == failing {
			return errors.New("injected: permission denied")
		}
		return os.RemoveAll(p)
	}
	var errb bytes.Buffer
	app := NewApp(&bytes.Buffer{}, &errb, proj)
	if code := runApp(app, "unlearn", "lint"); code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "remove-failed") {
		t.Fatalf("stderr:\n%s", errb.String())
	}
	if _, err := os.Stat(failing); err != nil {
		t.Fatal("failing directory should still exist")
	}
	if _, err := os.Stat(filepath.Join(proj, ".b", "lint")); !os.IsNotExist(err) {
		t.Fatal("the other path was not removed")
	}
	recs := readRecords(t, proj)
	if len(recs) != 1 || recs[0]["install_path"] != failing {
		t.Fatalf("records = %v; want only the failed one kept", recs)
	}
}

// C7 on a platform where permissions bind: a real read-only parent.
func TestUnlearn_C7_ReadOnlyParent(t *testing.T) {
	proj := unlearnProj(t)
	learnInto(t, proj, "lint")
	ro := filepath.Join(proj, ".a")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	probe := filepath.Join(ro, "probe")
	if err := os.Mkdir(probe, 0o755); err == nil {
		_ = os.Remove(probe)
		t.Skip("read-only directories do not bind here (running as root); injected variant covers the ordering")
	}
	var errb bytes.Buffer
	app := NewApp(&bytes.Buffer{}, &errb, proj)
	if code := runApp(app, "unlearn", "lint"); code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(ro, "lint")); err != nil {
		t.Fatal("undeletable directory vanished")
	}
	if _, err := os.Stat(filepath.Join(proj, ".b", "lint")); !os.IsNotExist(err) {
		t.Fatal("the other path was not removed")
	}
	if recs := readRecords(t, proj); len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
}

func readRecords(t *testing.T, proj string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, ".gym", "installed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s.Skills
}

func TestUnlearn_C8_GlobNeedsYesOffTerminal(t *testing.T) {
	proj := unlearnProj(t)
	learnInto(t, proj, "s1", "s2", "s3")
	var errb bytes.Buffer
	app := NewApp(&bytes.Buffer{}, &errb, proj)
	app.Stdin = strings.NewReader("")
	if code := runApp(app, "unlearn", "s*"); code != 1 || !strings.Contains(errb.String(), "confirmation-required: pattern matched 3 skills; pass --yes") {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}
	if len(readRecords(t, proj)) != 6 {
		t.Fatal("refused run changed state")
	}
	// explicit names never prompt
	app = NewApp(&bytes.Buffer{}, &errb, proj)
	app.Stdin = strings.NewReader("")
	if code := runApp(app, "unlearn", "s1", "s2"); code != 0 {
		t.Fatalf("explicit names: exit %d\n%s", code, errb.String())
	}
	// a glob matching one skill never prompts
	if code := runApp(app, "unlearn", "s3*"); code != 0 {
		t.Fatalf("single-match glob: exit %d", code)
	}
	learnInto(t, proj, "s1", "s2")
	app = NewApp(&bytes.Buffer{}, &errb, proj)
	app.Stdin = strings.NewReader("")
	if code := runApp(app, "unlearn", "--yes", "s*"); code != 0 {
		t.Fatalf("--yes: exit %d\n%s", code, errb.String())
	}
	if len(readRecords(t, proj)) != 0 {
		t.Fatal("--yes did not remove")
	}
}

func TestUnlearn_C8_TerminalPrompt(t *testing.T) {
	orig := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = orig })
	stdinIsTerminal = func(io.Reader) bool { return true }
	for _, c := range []struct {
		answer string
		code   int
		left   int
	}{{"y\n", 0, 0}, {"n\n", 1, 4}, {"\n", 1, 4}} {
		proj := unlearnProj(t)
		learnInto(t, proj, "s1", "s2")
		var errb bytes.Buffer
		app := NewApp(&bytes.Buffer{}, &errb, proj)
		app.Stdin = strings.NewReader(c.answer)
		code := runApp(app, "unlearn", "s*")
		if code != c.code || len(readRecords(t, proj)) != c.left || !strings.Contains(errb.String(), "remove 2 skills from "+proj+"? [y/N]") {
			t.Fatalf("answer %q: exit %d, records %d\n%s", c.answer, code, len(readRecords(t, proj)), errb.String())
		}
	}
}

type uninstallEvents struct {
	mu             sync.Mutex
	req            []events.SkillUninstallRequestedPayload
	done           []events.SkillUninstallCompletedPayload
	failed         []events.SkillUninstallFailedPayload
	order          []string
	app            *App
	stdout, stderr *bytes.Buffer
}

func newUninstallEvents(t *testing.T, proj string) *uninstallEvents {
	t.Helper()
	ue := &uninstallEvents{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	ue.app = NewApp(ue.stdout, ue.stderr, proj)
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	ue.app.bus = b
	sub := func(topic bus.Topic, f func(raw []byte)) {
		b.Subscribe(string(topic), func(_ context.Context, e bus.Event) error {
			raw, _ := json.Marshal(e.Payload)
			ue.mu.Lock()
			defer ue.mu.Unlock()
			ue.order = append(ue.order, string(topic))
			f(raw)
			return nil
		})
	}
	sub(events.TopicSkillUninstallRequested, func(raw []byte) {
		var p events.SkillUninstallRequestedPayload
		_ = json.Unmarshal(raw, &p)
		ue.req = append(ue.req, p)
	})
	sub(events.TopicSkillUninstallCompleted, func(raw []byte) {
		var p events.SkillUninstallCompletedPayload
		_ = json.Unmarshal(raw, &p)
		ue.done = append(ue.done, p)
	})
	sub(events.TopicSkillUninstallFailed, func(raw []byte) {
		var p events.SkillUninstallFailedPayload
		_ = json.Unmarshal(raw, &p)
		ue.failed = append(ue.failed, p)
	})
	return ue
}

func TestUnlearn_C10_EventsRequestedThenCompleted(t *testing.T) {
	proj := unlearnProj(t)
	learnInto(t, proj, "lint", "keep")
	ue := newUninstallEvents(t, proj)
	if code := runApp(ue.app, "unlearn", "lint", "keep"); code != 0 {
		t.Fatalf("exit %d\n%s", code, ue.stderr.String())
	}
	time.Sleep(150 * time.Millisecond)
	ue.mu.Lock()
	defer ue.mu.Unlock()
	if len(ue.req) != 2 || len(ue.done) != 2 || len(ue.failed) != 0 {
		t.Fatalf("req=%d done=%d failed=%d", len(ue.req), len(ue.done), len(ue.failed))
	}
	ids := map[string]bool{}
	for _, d := range ue.done {
		ids[d.RequestID] = true
		var r *events.SkillUninstallRequestedPayload
		for i := range ue.req {
			if ue.req[i].RequestID == d.RequestID {
				r = &ue.req[i]
			}
		}
		if r == nil || r.Skill != d.Skill || r.Scope != "project" || r.Folder != proj || len(r.Paths) != 2 || len(d.Paths) != 2 {
			t.Fatalf("completed %+v lacks matching requested %+v", d, r)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("request ids not unique per skill attempt: %v", ids)
	}
}

func TestUnlearn_C10_FailedEventCarriesReason(t *testing.T) {
	proj := unlearnProj(t)
	learnInto(t, proj, "lint")
	if err := os.WriteFile(filepath.Join(proj, ".a", "lint", "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	ue := newUninstallEvents(t, proj)
	if code := runApp(ue.app, "unlearn", "lint"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	time.Sleep(150 * time.Millisecond)
	ue.mu.Lock()
	defer ue.mu.Unlock()
	if len(ue.failed) != 1 || ue.failed[0].Reason != "skill-modified" || ue.failed[0].Skill != "lint" ||
		len(ue.req) != 1 || ue.req[0].RequestID != ue.failed[0].RequestID || len(ue.done) != 0 {
		t.Fatalf("failed=%+v req=%+v done=%+v", ue.failed, ue.req, ue.done)
	}
}

func TestUnlearn_DryRunPublishesNothing(t *testing.T) {
	proj := unlearnProj(t)
	learnInto(t, proj, "lint")
	ue := newUninstallEvents(t, proj)
	if code := runApp(ue.app, "unlearn", "-n", "lint"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	time.Sleep(100 * time.Millisecond)
	ue.mu.Lock()
	defer ue.mu.Unlock()
	if len(ue.order) != 0 {
		t.Fatalf("dry-run published %v", ue.order)
	}
}

// The sequence seen with a read-only parent: files deleted, final rmdir
// fails. Exit 1, record kept; the rerun needs no --force.
func TestUnlearn_PartialRemovalRerunWithoutForce(t *testing.T) {
	proj := unlearnProj(t)
	learnInto(t, proj, "lint")
	failing := filepath.Join(proj, ".a", "lint")
	orig := unlearnRemoveAll
	t.Cleanup(func() { unlearnRemoveAll = orig })
	unlearnRemoveAll = func(p string) error {
		if p != failing {
			return os.RemoveAll(p)
		}
		ents, _ := os.ReadDir(p)
		for _, e := range ents {
			_ = os.RemoveAll(filepath.Join(p, e.Name()))
		}
		return errors.New("injected: rmdir permission denied")
	}
	var errb bytes.Buffer
	if code := runApp(NewApp(&bytes.Buffer{}, &errb, proj), "unlearn", "lint"); code != 1 {
		t.Fatalf("first run exit %d\n%s", code, errb.String())
	}
	if recs := readRecords(t, proj); len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
	unlearnRemoveAll = orig
	errb.Reset()
	if code := runApp(NewApp(&bytes.Buffer{}, &errb, proj), "unlearn", "lint"); code != 0 {
		t.Fatalf("rerun exit %d\n%s", code, errb.String())
	}
	if _, err := os.Stat(failing); !os.IsNotExist(err) || len(readRecords(t, proj)) != 0 {
		t.Fatal("rerun did not remove directory and record")
	}
}

func TestUnlearn_EventFolderIsResolvedDir(t *testing.T) {
	proj := unlearnProj(t)
	learnInto(t, proj, "lint")
	ue := newUninstallEvents(t, scratch(t))
	if code := runApp(ue.app, "unlearn", "--dir", proj, "lint"); code != 0 {
		t.Fatalf("exit %d\n%s", code, ue.stderr.String())
	}
	time.Sleep(150 * time.Millisecond)
	ue.mu.Lock()
	defer ue.mu.Unlock()
	if len(ue.req) != 1 || ue.req[0].Folder != proj || len(ue.done) != 1 || ue.done[0].Folder != proj {
		t.Fatalf("req=%+v done=%+v", ue.req, ue.done)
	}
}

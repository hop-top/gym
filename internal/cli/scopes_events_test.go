package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/xdg"
)

// A project learn publishes the scope root as `folder`; a global learn none.
func TestLearn_RequestedEventCarriesFolder(t *testing.T) {
	home := isolateInternal(t)
	root := filepath.Join(home, "org", "unit")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ba := newBusApp(t, root, true)
	if err := ba.app.runLearnCtx(context.Background(), false, "", nil, []string{skillTree(t, "lint", "")}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	_, req := ba.snapshot()
	if len(req) == 0 || req[len(req)-1].Folder != root || req[len(req)-1].Scope != "project" {
		t.Fatalf("requested = %+v, want folder %s", req, root)
	}

	ba = newBusApp(t, root, true)
	if err := ba.learn(t, skillTree(t, "fmt", ""), scratch(t)); err != nil {
		t.Fatal(err)
	}
	_, req = ba.snapshot()
	if len(req) == 0 || req[len(req)-1].Folder != "" || req[len(req)-1].Scope != "global" {
		t.Fatalf("global requested = %+v, want no folder", req)
	}
}

// A project train publishes the scope root on the plan event; --dir picks it.
func TestTrain_PlanCarriesScopeRoot(t *testing.T) {
	home := isolateInternal(t)
	root := filepath.Join(home, "org")
	src := skillTree(t, "lint", "")
	old := filepath.Join(root, ".gym", "skills", "lint")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "SKILL.md"), []byte("---\nname: lint\ndescription: o\n---\nOLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveProject(root, []state.InstalledSkill{{Name: "lint", Version: "1.0.0", SourceURL: src, InstallPath: old}}); err != nil {
		t.Fatal(err)
	}
	dirs, _ := xdg.AppDirs(config.AppName)
	_ = registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{{Name: "lint", Version: "1.1.0", SourceURL: src}}})

	ba := newBusApp(t, home, true)
	ba.app.scopeDir = root
	got := make(chan string, 4)
	ba.app.bus.Subscribe(string(events.TopicSkillUpgradePlanned), func(_ context.Context, e bus.Event) error {
		var p events.SkillUpgradePlannedPayload
		raw, _ := json.Marshal(e.Payload)
		_ = json.Unmarshal(raw, &p)
		got <- p.Folder
		return nil
	})
	if err := ba.app.runTrainCtx(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-got:
		if f != root {
			t.Fatalf("plan folder = %q, want %q", f, root)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no plan event")
	}
}

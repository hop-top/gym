package cli

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/xdg"
)

// A source_url already in state (older binary, or a hand-edited file) may
// carry credentials; neither the upgrade-planned event nor the rewritten
// state may repeat them.
func TestTrain_EventAndStateRedactSourceURL(t *testing.T) {
	home := isolateInternal(t)
	src := skillTree(t, "lint", "")
	seedUpgrade(t, src)
	dirs, err := xdg.AppDirs(config.AppName)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := state.LoadGlobal(dirs.StateHome)
	if err != nil || len(skills) != 1 {
		t.Fatalf("seed: %v %v", skills, err)
	}
	skills[0].SourceURL = "https://user:hunter2@127.0.0.1:1/org/lint.git?token=SEKRET"
	if err := state.SaveGlobal(dirs.StateHome, skills); err != nil {
		t.Fatal(err)
	}

	ba := newBusApp(t, home, true)
	var mu sync.Mutex
	var planned []string
	ba.app.bus.Subscribe(string(events.TopicSkillUpgradePlanned), func(_ context.Context, e bus.Event) error {
		raw, _ := json.Marshal(e.Payload)
		mu.Lock()
		planned = append(planned, string(raw))
		mu.Unlock()
		return nil
	})
	if err := ba.train(t); err != nil {
		t.Fatalf("train: %v\n%s", err, ba.stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(planned) != 1 {
		t.Fatalf("planned events = %d", len(planned))
	}
	for _, leak := range []string{"hunter2", "SEKRET"} {
		if strings.Contains(planned[0], leak) {
			t.Errorf("event payload leaks %s: %s", leak, planned[0])
		}
	}
	after, _ := state.LoadGlobal(dirs.StateHome)
	for _, s := range after {
		if strings.Contains(s.SourceURL, "hunter2") || strings.Contains(s.SourceURL, "SEKRET") {
			t.Errorf("state leaks credentials: %s", s.SourceURL)
		}
	}
}

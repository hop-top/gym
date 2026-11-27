package cheat_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"hop.top/kit/go/runtime/bus"

	"hop.top/gym/internal/cheat"
	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/state"
)

// fakeRoot rebuilds a minimal cobra tree similar to the real gym root
// so the self-spec walk has something representative without booting
// the kit/cli root (which mutates global env).
func fakeRoot() *cobra.Command {
	root := &cobra.Command{Use: "gym"}
	root.PersistentFlags().Bool("offline", false, "disable network")
	root.PersistentFlags().String("bus", "", "bus adapter")

	root.AddCommand(
		&cobra.Command{Use: "version", Short: "print version"},
		&cobra.Command{Use: "warmup", Short: "warmup metadata"},
		&cobra.Command{Use: "learn", Short: "install"},
		&cobra.Command{Use: "train", Short: "upgrade"},
	)
	coachCmd := &cobra.Command{Use: "coach"}
	coachCmd.AddCommand(
		&cobra.Command{Use: "list"},
		&cobra.Command{Use: "hire"},
		&cobra.Command{Use: "dismiss"},
	)
	root.AddCommand(coachCmd)
	return root
}

func TestBuildSelfSpec_GoldenShape(t *testing.T) {
	spec := cheat.BuildSelfSpec(fakeRoot(), "0.1.0")
	if spec == nil {
		t.Fatal("BuildSelfSpec returned nil")
	}
	if spec.Name != cheat.ToolName {
		t.Errorf("Name = %q; want %q", spec.Name, cheat.ToolName)
	}
	if spec.SchemaVersion != cheat.SchemaVersion {
		t.Errorf("SchemaVersion = %q; want %q", spec.SchemaVersion, cheat.SchemaVersion)
	}

	// warmup, learn, train must appear with the expected safety levels.
	want := map[string]string{
		"warmup":  "safe",
		"learn":   "caution",
		"train":   "caution",
		"version": "safe",
		"coach":   "safe",
	}
	got := map[string]string{}
	for _, c := range spec.Commands {
		if c.Safety != nil {
			got[c.Name] = string(c.Safety.Level)
		}
	}
	for name, lvl := range want {
		if got[name] != lvl {
			t.Errorf("safety %s: got %q, want %q", name, got[name], lvl)
		}
	}

	// coach.hire/dismiss must show up as children with caution.
	coach := spec.FindCommand("coach")
	if coach == nil {
		t.Fatal("coach command missing")
	}
	wantChildren := map[string]string{"hire": "caution", "dismiss": "caution", "list": "safe"}
	for _, child := range coach.Children {
		exp, ok := wantChildren[child.Name]
		if !ok {
			continue
		}
		if child.Safety == nil || string(child.Safety.Level) != exp {
			t.Errorf("coach.%s safety: got %v, want %q", child.Name, child.Safety, exp)
		}
	}
}

func TestBuildSelfSpec_ContractAndOutputSchema(t *testing.T) {
	spec := cheat.BuildSelfSpec(fakeRoot(), "0.1.0")
	warmup := spec.FindCommand("warmup")
	if warmup == nil {
		t.Fatal("warmup missing")
	}
	if warmup.OutputSchema == nil || warmup.OutputSchema.Format != "json" {
		t.Errorf("warmup output schema missing or wrong: %+v", warmup.OutputSchema)
	}
	learn := spec.FindCommand("learn")
	if learn == nil || learn.Contract == nil || !learn.Contract.Idempotent {
		t.Errorf("learn contract should be idempotent: %+v", learn)
	}
}

func TestBuildSkillSpecs_FromInstalledFixture(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`---
name: alpha
description: An example skill that does alpha things
metadata:
  commands: alpha foo, alpha bar
  examples: |
    alpha foo --opt
    alpha bar
---
# alpha

body
`)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	specs := cheat.BuildSkillSpecs([]state.InstalledSkill{
		{Name: "alpha", Version: "1.0.0", InstallPath: skillDir},
		{Name: "beta", Version: "0.9.0", InstallPath: filepath.Join(dir, "missing")},
	})
	if len(specs) != 2 {
		t.Fatalf("len = %d; want 2", len(specs))
	}

	alpha := specs[0]
	if alpha.Name != "alpha" {
		t.Errorf("alpha.Name = %q", alpha.Name)
	}
	if len(alpha.Commands) != 1 {
		t.Fatalf("alpha.Commands = %d; want 1", len(alpha.Commands))
	}
	root := alpha.Commands[0]
	if root.Intent == nil || len(root.Intent.Tags) == 0 {
		t.Errorf("alpha description missing: %+v", root.Intent)
	}
	if got := len(root.Children); got != 2 {
		t.Errorf("alpha child commands = %d; want 2 (parsed from frontmatter)", got)
	}
	if len(alpha.Workflows) != 1 {
		t.Errorf("alpha workflows = %d; want 1", len(alpha.Workflows))
	}

	// beta has no SKILL.md; spec must still exist with empty extras.
	beta := specs[1]
	if beta.Name != "beta" {
		t.Errorf("beta.Name = %q", beta.Name)
	}
}

func TestRegistry_LookupResolution(t *testing.T) {
	root := fakeRoot()
	self := cheat.BuildSelfSpec(root, "0.1.0")
	skills := []map[string]string{}
	_ = skills
	reg := cheat.NewRegistry()
	reg.Update(self, nil)

	// Self lookup
	res, ok := reg.Lookup("gym")
	if !ok || res.Kind != cheat.KindSelf {
		t.Errorf("gym lookup: ok=%v kind=%v", ok, res.Kind)
	}
	res, ok = reg.Lookup("warmup")
	if !ok || res.Kind != cheat.KindGymCommand || res.Command.Name != "warmup" {
		t.Errorf("warmup lookup: %+v ok=%v", res, ok)
	}
	// Space-separated form
	res, ok = reg.Lookup("coach hire")
	if !ok || res.Kind != cheat.KindGymCommand || res.Command.Name != "hire" {
		t.Errorf("coach hire lookup: %+v ok=%v", res, ok)
	}
	// Dot form
	res, ok = reg.Lookup("coach.dismiss")
	if !ok || res.Command.Name != "dismiss" {
		t.Errorf("coach.dismiss lookup: %+v ok=%v", res, ok)
	}
	// Unknown
	if _, ok := reg.Lookup("nope"); ok {
		t.Error("nope should be unknown")
	}
}

func TestWriteTipAdapter_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adapters", "gym.json")

	root := fakeRoot()
	self := cheat.BuildSelfSpec(root, "0.1.0")
	reg := cheat.NewRegistry()
	reg.Update(self, nil)

	got, err := cheat.WriteTipAdapter(path, reg, "0.1.0")
	if err != nil {
		t.Fatalf("WriteTipAdapter: %v", err)
	}
	if got != path {
		t.Errorf("WriteTipAdapter path = %q; want %q", got, path)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file cheat.AdapterFile
	if err := json.Unmarshal(blob, &file); err != nil {
		t.Fatalf("Unmarshal adapter file: %v", err)
	}
	if file.Name != "gym" {
		t.Errorf("Name = %q", file.Name)
	}
	if file.SchemaVersion != cheat.SchemaVersion {
		t.Errorf("SchemaVersion = %q", file.SchemaVersion)
	}
	if file.Spec == nil || file.Spec.Name != "gym" {
		t.Errorf("Spec missing or wrong: %+v", file.Spec)
	}
	if len(file.Commands) == 0 {
		t.Error("Commands list should not be empty")
	}
	wantCaps := map[string]bool{"toolspec": true, "cheat-sheets": true, "skill-registry": true}
	for _, c := range file.Capabilities {
		delete(wantCaps, c)
	}
	if len(wantCaps) != 0 {
		t.Errorf("missing capabilities: %v", wantCaps)
	}
}

func TestMaybeStartBridge_OffByDefault(t *testing.T) {
	t.Setenv(cheat.BusBridgeEnvVar, "")
	b, err := eventbus.New(eventbus.Config{})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer b.Close(context.Background())

	reg := cheat.NewRegistry()
	reg.Update(cheat.BuildSelfSpec(fakeRoot(), "0.1.0"), nil)

	unsub := cheat.MaybeStartBridge(b, reg)
	if unsub != nil {
		t.Error("bridge should be off when env var is unset")
	}
}

func TestMaybeStartBridge_AnswersFromRegistry(t *testing.T) {
	t.Setenv(cheat.BusBridgeEnvVar, "1")
	b, err := eventbus.New(eventbus.Config{})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer b.Close(context.Background())

	reg := cheat.NewRegistry()
	reg.Update(cheat.BuildSelfSpec(fakeRoot(), "0.1.0"), nil)

	unsub := cheat.MaybeStartBridge(b, reg)
	if unsub == nil {
		t.Fatal("bridge should be on when GYM_TIP_BUS_BRIDGE=1")
	}
	defer unsub()

	gotHint := make(chan string, 1)
	b.Subscribe(string(events.TopicCheatHintEmitted), func(ctx context.Context, e bus.Event) error {
		raw, _ := json.Marshal(e.Payload)
		var p events.CheatHintEmittedPayload
		_ = json.Unmarshal(raw, &p)
		select {
		case gotHint <- p.Hint:
		default:
		}
		return nil
	})

	if err := b.Publish(context.Background(), events.TopicTipShellCommandObserved,
		events.TipShellCommandObservedPayload{Command: "gym warmup"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case h := <-gotHint:
		if h == "" {
			t.Error("hint empty")
		}
	default:
		t.Error("expected hint to be emitted within publish call")
	}
}

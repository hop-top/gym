package guard_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/guard"
)

// resolverEngine loads built-ins plus one project policy dir that denies
// the skill "blocked".
func resolverEngine(t *testing.T) *guard.Engine {
	t.Helper()
	dir := t.TempDir()
	body := "when: 'payload.skill == \"blocked\"'\neffect: deny\notherwise: allow\nseverity: deny\nmessage: scope says no\n"
	if err := os.WriteFile(filepath.Join(dir, "scope-deny.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	policies, err := guard.Load(guard.LoadOptions{ProjectDirs: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := guard.NewEngine(policies)
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

// A request names its policy root; the guard evaluates it with the engine
// the resolver returns for that root. A request without a root, or a
// resolver failure, never gets the default set's verdict by accident: the
// first uses the default engine, the second is denied.
func TestGuard_EngineResolver_SelectsEngineByPolicyRoot(t *testing.T) {
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")
	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())

	def := allBuiltinEngine(t)
	scoped := resolverEngine(t)
	calls := map[string]int{}
	g, err := guard.New(b, guard.WithEngine(def), guard.WithEngineResolver(func(root string) (*guard.Engine, error) {
		calls[root]++
		switch root {
		case "/scoped":
			return scoped, nil
		case "/broken":
			return nil, errors.New("bad.yaml: unclosed")
		}
		return def, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	gate := func(id, root string) error {
		return guard.GateInstall(context.Background(), b, events.SkillInstallRequestedPayload{
			Skill: "blocked", SourceURL: "https://example.com/x", Scope: "project",
			PolicyRoot: root, RequestID: id,
		}, guard.GateConfig{Timeout: 5 * time.Second})
	}

	if err := gate("r-default", ""); err != nil {
		t.Errorf("no policy root: %v, want allow from the default engine", err)
	}
	if calls[""] != 0 {
		t.Error("resolver consulted for a request without a policy root")
	}
	err = gate("r-scoped", "/scoped")
	if !errors.Is(err, guard.ErrPolicyDeny) || !strings.Contains(err.Error(), "scope-deny") {
		t.Errorf("scoped engine not applied: %v", err)
	}
	err = gate("r-broken", "/broken")
	if !errors.Is(err, guard.ErrPolicyDeny) || !strings.Contains(err.Error(), "guard-unavailable") {
		t.Errorf("resolver failure not denied: %v", err)
	}
}

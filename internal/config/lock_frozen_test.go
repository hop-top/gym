package config_test

import (
	"testing"

	"hop.top/gym/internal/config"
)

// lock.frozen folds with "or": a project or folder may turn it on and never
// off; the operator's -c layer may set either.
func TestLockFrozenFoldIsOr(t *testing.T) {
	load := func(t *testing.T, org, unit, proj, user string, over map[string]any) bool {
		t.Helper()
		_, _, p := chainEnv(t, org, unit, proj)
		if user != "" {
			writeUserYAML(t, user)
		}
		cfg, err := config.LoadMergedConfig(p, nil, over)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Lock.Frozen
	}
	on, off := "lock:\n  frozen: true\n", "lock:\n  frozen: false\n"
	if load(t, "", "", "", "", nil) {
		t.Fatal("default must be unfrozen")
	}
	if !load(t, "", "", on, "", nil) {
		t.Fatal("a project must be able to turn frozen on")
	}
	if !load(t, on, off, off, "", nil) {
		t.Fatal("an inner folder turned an outer frozen off")
	}
	if !load(t, "", "", off, on, nil) {
		t.Fatal("a project turned the user's frozen off")
	}
	if load(t, "", "", on, off, map[string]any{"lock": map[string]any{"frozen": false}}) {
		t.Fatal("the operator's -c override must be able to turn frozen off")
	}
	if !load(t, "", "", "", off, map[string]any{"lock": map[string]any{"frozen": true}}) {
		t.Fatal("the operator's -c override must be able to turn frozen on")
	}
}

func TestLockKeyIsOnTheProjectAllowlist(t *testing.T) {
	_, _, p := chainEnv(t, "lock:\n  frozen: true\n", "", "")
	_, warns, err := config.LoadMergedConfigWarn(p, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warns {
		t.Errorf("lock key warned as ignored: %s", w)
	}
}

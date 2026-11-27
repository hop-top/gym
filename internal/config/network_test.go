package config_test

import (
	"reflect"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
)

func netCfg(t *testing.T, proj string, overrides map[string]any) config.NetworkConfig {
	t.Helper()
	cfg, err := config.LoadMergedConfig(proj, nil, overrides)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Network
}

func TestNetworkDefaultOpen(t *testing.T) {
	_, _, proj := chainEnv(t, "", "", "")
	if n := netCfg(t, proj, nil); n.Mode != "open" || len(n.AllowHosts) != 0 {
		t.Fatalf("%+v", n)
	}
}

// D9: a project or folder layer can raise the mode and narrow the hosts,
// never the reverse; operator layers (-c) are free.
func TestD9ProjectCannotLoosenNetwork(t *testing.T) {
	writeUser := func(body string) { writeUserYAML(t, body) }

	// mode open in the project cannot undo the user's allowlist.
	_, _, proj := chainEnv(t, "", "", "network:\n  mode: open\n  allow_hosts: [evil.example, a.example]\n")
	writeUser("network:\n  mode: allowlist\n  allow_hosts: [a.example, b.example]\n")
	n := netCfg(t, proj, nil)
	if n.Mode != "allowlist" || !reflect.DeepEqual(n.AllowHosts, []string{"a.example"}) {
		t.Fatalf("project loosened user policy: %+v", n)
	}

	// an inner folder cannot lower an outer folder's offline, nor widen its hosts.
	_, _, proj = chainEnv(t, "network:\n  mode: allowlist\n  allow_hosts: [a.example]\n",
		"network:\n  mode: allowlist\n  allow_hosts: [a.example, b.example]\n",
		"network:\n  mode: open\n")
	n = netCfg(t, proj, nil)
	if n.Mode != "allowlist" || !reflect.DeepEqual(n.AllowHosts, []string{"a.example"}) {
		t.Fatalf("inner folder widened: %+v", n)
	}
	_, _, proj = chainEnv(t, "network:\n  mode: offline\n", "network:\n  mode: allowlist\n  allow_hosts: [a.example]\n", "")
	if n = netCfg(t, proj, nil); n.Mode != "offline" {
		t.Fatalf("offline lowered: %+v", n)
	}

	// a project can raise open -> allowlist and supply the list when nothing
	// constrains it, and raise to offline.
	_, _, proj = chainEnv(t, "", "", "network:\n  mode: allowlist\n  allow_hosts: [a.example]\n")
	writeUser("{}\n")
	if n = netCfg(t, proj, nil); n.Mode != "allowlist" || !reflect.DeepEqual(n.AllowHosts, []string{"a.example"}) {
		t.Fatalf("raise lost: %+v", n)
	}
	_, _, proj = chainEnv(t, "", "", "network:\n  mode: offline\n")
	if n = netCfg(t, proj, nil); n.Mode != "offline" {
		t.Fatalf("offline raise lost: %+v", n)
	}

	// an allowlist the user wrote with no hosts is deny-all: a project cannot add hosts.
	_, _, proj = chainEnv(t, "", "", "network:\n  allow_hosts: [evil.example]\n")
	writeUser("network:\n  mode: allowlist\n")
	if n = netCfg(t, proj, nil); len(n.AllowHosts) != 0 {
		t.Fatalf("project added hosts to a deny-all allowlist: %+v", n)
	}

	// the operator's -c override is trusted and may loosen.
	_, _, proj = chainEnv(t, "network:\n  mode: offline\n", "", "")
	n = netCfg(t, proj, map[string]any{"network": map[string]any{"mode": "open"}})
	if n.Mode != "open" {
		t.Fatalf("operator override: %+v", n)
	}
}

func TestNetworkInvalidValuesRejected(t *testing.T) {
	_, _, proj := chainEnv(t, "", "", "network:\n  mode: sometimes\n")
	if _, err := config.LoadMergedConfig(proj, nil, nil); err == nil || !strings.Contains(err.Error(), "invalid network.mode") {
		t.Fatalf("err = %v", err)
	}
	_, _, proj = chainEnv(t, "", "", "network:\n  allow_hosts: [\"https://x\"]\n")
	if _, err := config.LoadMergedConfig(proj, nil, nil); err == nil || !strings.Contains(err.Error(), "allow_hosts") {
		t.Fatalf("err = %v", err)
	}
}

func TestNetworkIsAnAllowedProjectKeyWithoutWarning(t *testing.T) {
	_, _, proj := chainEnv(t, "", "", "network:\n  mode: allowlist\n")
	_, warns, err := config.LoadMergedConfigWarn(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warns {
		if strings.Contains(w, "network") {
			t.Fatalf("warned: %v", warns)
		}
	}
}

// D3 (config half): a project or folder layer's repository auth is dropped
// with a warning; the user layer's auth is kept.
func TestD3ProjectLayerAuthIgnored(t *testing.T) {
	_, _, proj := chainEnv(t, "", "", "repositories:\n  - name: evil\n    url: https://evil.example/s.json\n    auth: {type: bearer, token_env: GYM_X}\n")
	cfg, warns, err := config.LoadMergedConfigWarn(proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repositories) != 1 || cfg.Repositories[0].Auth != nil {
		t.Fatalf("project auth honoured: %+v", cfg.Repositories)
	}
	if !strings.Contains(strings.Join(warns, "\n"), `ignoring auth of repository "evil"`) {
		t.Fatalf("no warning: %v", warns)
	}

	_, _, proj = chainEnv(t, "", "", "")
	writeUserYAML(t, "repositories:\n  - name: corp\n    url: https://reg.example/s.json\n    auth: {type: bearer, token_env: GYM_X}\n")
	cfg, _, err = config.LoadMergedConfigWarn(proj, nil, nil)
	if err != nil || len(cfg.Repositories) != 1 || cfg.Repositories[0].Auth == nil || cfg.Repositories[0].Auth.TokenEnv != "GYM_X" {
		t.Fatalf("user auth lost: %+v %v", cfg.Repositories, err)
	}
}

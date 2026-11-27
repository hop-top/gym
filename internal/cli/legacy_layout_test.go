package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An install made before the move to kit's xdg/config keeps working: the
// previous XDG-style dirs and JSON config files are still read.
func TestPaths_PreviousLayoutStillRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cwd := t.TempDir()
	write(t, filepath.Join(home, ".config", "gym", "config.json"),
		`{"install_paths":[{"name":"fromuser","path":"/u","scope":"global"}]}`)
	write(t, filepath.Join(cwd, ".gym", "config.json"),
		`{"install_paths":[{"name":"fromproject","path":"/p","scope":"global"}]}`)

	out, errs, code := runGymCfg(t, cwd, "paths")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if strings.Contains(out, "configured install paths: none") {
		t.Fatalf("legacy config ignored:\n%s", out)
	}
	// project layer replaces user list; assert the layered winner is read.
	if !strings.Contains(out, "fromproject [global] /p") {
		t.Fatalf("project config.json not read:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "gym", "config.yaml")); err == nil {
		t.Fatal("read-only command created config.yaml")
	}
}

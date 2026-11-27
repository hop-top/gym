package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/cli"
)

// isolate points HOME and every XDG dir at fresh temp dirs and returns
// (home, cwd). The user config dir is <home>/cfg/gym.
func isolate(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	return home, t.TempDir()
}

func runGym(t *testing.T, cwd string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	app := cli.NewApp(&out, &errb, cwd)
	code := app.Run(args)
	return out.String(), errb.String(), code
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFlag_KeyValueOverrideEndToEnd(t *testing.T) {
	_, cwd := isolate(t)
	out, errs, code := runGym(t, cwd,
		"-c", "install_paths=[{name: viaflag, path: /opt/skills, scope: global}]", "paths")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "viaflag [global] /opt/skills") {
		t.Fatalf("override not visible in `paths` output:\n%s", out)
	}
}

func TestConfigFlag_ExtraFileLayersAfterDiscovered(t *testing.T) {
	home, cwd := isolate(t)
	write(t, filepath.Join(home, "cfg", "gym", "config.yaml"),
		"install_paths:\n  - {name: fromuser, path: /u, scope: global}\n")
	write(t, filepath.Join(cwd, ".gym", "config.yaml"),
		"install_paths:\n  - {name: fromproject, path: /p, scope: global}\n")

	// Discovered only: project wins over user.
	out, errs, code := runGym(t, cwd, "paths")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "fromproject") || strings.Contains(out, "fromuser") {
		t.Fatalf("discovered layering wrong:\n%s", out)
	}

	// -c <file> layers after both discovered files.
	extra := filepath.Join(t.TempDir(), "extra.yaml")
	write(t, extra, "install_paths:\n  - {name: fromextra, path: /e, scope: global}\n")
	out, errs, code = runGym(t, cwd, "-c", extra, "paths")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "fromextra") || strings.Contains(out, "fromproject") {
		t.Fatalf("-c file not layered last:\n%s", out)
	}

	// -c key=value beats the -c file.
	out, errs, code = runGym(t, cwd, "-c", extra,
		"-c", "install_paths=[{name: fromoverride, path: /o, scope: global}]", "paths")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "fromoverride") || strings.Contains(out, "fromextra") {
		t.Fatalf("-c key=value not applied last:\n%s", out)
	}
}

func TestConfigFlag_MissingFileFails(t *testing.T) {
	_, cwd := isolate(t)
	_, _, code := runGym(t, cwd, "-c", filepath.Join(cwd, "absent.yaml"), "paths")
	if code == 0 {
		t.Fatal("want non-zero exit for missing -c file")
	}
}

// kit hides its plumbing flags (-c/--config among them) from the default
// --help and reveals them under --help-all.
func TestConfigFlag_HelpAllShowsKitFlagOnce(t *testing.T) {
	_, cwd := isolate(t)
	out, _, code := runGym(t, cwd, "--help-all")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := strings.Count(out, "--config"); n != 1 {
		t.Fatalf("--config appears %d times in help, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "-c, --config") {
		t.Fatalf("help missing `-c, --config`:\n%s", out)
	}
}

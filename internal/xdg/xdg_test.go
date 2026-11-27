package xdg_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hop.top/gym/internal/xdg"
)

func TestAppDirsUsesXDGEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/cfg")
	t.Setenv("XDG_DATA_HOME", "/tmp/data")
	t.Setenv("XDG_CACHE_HOME", "/tmp/cache")
	t.Setenv("XDG_STATE_HOME", "/tmp/state")

	dirs, err := xdg.AppDirs("skills")
	if err != nil {
		t.Fatalf("AppDirs() error = %v", err)
	}

	if got, want := dirs.ConfigHome, filepath.Join("/tmp/cfg", "skills"); got != want {
		t.Fatalf("ConfigHome = %q, want %q", got, want)
	}
	if got, want := dirs.DataHome, filepath.Join("/tmp/data", "skills"); got != want {
		t.Fatalf("DataHome = %q, want %q", got, want)
	}
	if got, want := dirs.CacheHome, filepath.Join("/tmp/cache", "skills"); got != want {
		t.Fatalf("CacheHome = %q, want %q", got, want)
	}
	if got, want := dirs.StateHome, filepath.Join("/tmp/state", "skills"); got != want {
		t.Fatalf("StateHome = %q, want %q", got, want)
	}
}

// TestAppDirsFallsBackToHome verifies kit/xdg's OS-native fallbacks when
// XDG_* env vars are unset. Linux/BSD pick ~/.config etc., macOS picks
// ~/Library/Application Support, Windows uses %APPDATA%. We assert each
// path lives under HOME (or %APPDATA% on Windows) and ends with the app
// name, but don't pin the exact intermediate path — that's adrg/xdg's
// responsibility.
func TestAppDirsFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	dirs, err := xdg.AppDirs("skills")
	if err != nil {
		t.Fatalf("AppDirs() error = %v", err)
	}

	for _, p := range []struct {
		name string
		path string
	}{
		{"ConfigHome", dirs.ConfigHome},
		{"DataHome", dirs.DataHome},
		{"CacheHome", dirs.CacheHome},
		{"StateHome", dirs.StateHome},
	} {
		if !strings.HasSuffix(p.path, "skills") {
			t.Errorf("%s = %q does not end with app name", p.name, p.path)
		}
		if runtime.GOOS != "windows" && !strings.HasPrefix(p.path, home) {
			t.Errorf("%s = %q does not live under HOME=%q", p.name, p.path, home)
		}
	}
}

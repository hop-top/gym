package xdg_test

import (
	"path/filepath"
	"testing"

	"github.com/hop-top/gym/internal/xdg"
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

	if got, want := dirs.ConfigHome, filepath.Join(home, ".config", "skills"); got != want {
		t.Fatalf("ConfigHome = %q, want %q", got, want)
	}
	if got, want := dirs.DataHome, filepath.Join(home, ".local", "share", "skills"); got != want {
		t.Fatalf("DataHome = %q, want %q", got, want)
	}
	if got, want := dirs.CacheHome, filepath.Join(home, ".cache", "skills"); got != want {
		t.Fatalf("CacheHome = %q, want %q", got, want)
	}
	if got, want := dirs.StateHome, filepath.Join(home, ".local", "state", "skills"); got != want {
		t.Fatalf("StateHome = %q, want %q", got, want)
	}
}

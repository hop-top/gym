package xdg_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"hop.top/gym/internal/xdg"
)

type kind struct {
	name string
	env  string
	prev []string // previous XDG-style location relative to HOME
	get  func(xdg.Dirs) string
}

var kinds = []kind{
	{"config", "XDG_CONFIG_HOME", []string{".config"}, func(d xdg.Dirs) string { return d.ConfigHome }},
	{"data", "XDG_DATA_HOME", []string{".local", "share"}, func(d xdg.Dirs) string { return d.DataHome }},
	{"state", "XDG_STATE_HOME", []string{".local", "state"}, func(d xdg.Dirs) string { return d.StateHome }},
	{"cache", "XDG_CACHE_HOME", []string{".cache"}, func(d xdg.Dirs) string { return d.CacheHome }},
}

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range kinds {
		t.Setenv(k.env, "")
		os.Unsetenv(k.env)
	}
	return home
}

func mk(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

// An explicitly set XDG variable always wins, even when it points at a
// directory without gym and a previous directory exists under HOME. Real on
// every OS.
func TestAppDirsEnvSetWins(t *testing.T) {
	for _, k := range kinds {
		for _, withGym := range []bool{false, true} {
			name := k.name + "/no-gym-subdir"
			if withGym {
				name = k.name + "/gym-subdir-present"
			}
			t.Run(name, func(t *testing.T) {
				home := isolate(t)
				xdgRoot := t.TempDir()
				t.Setenv(k.env, xdgRoot)
				if withGym {
					mk(t, filepath.Join(xdgRoot, "gym"))
				}
				mk(t, filepath.Join(append([]string{home}, append(k.prev, "gym")...)...))
				d, err := xdg.AppDirs("gym")
				if err != nil {
					t.Fatal(err)
				}
				if got, want := k.get(d), filepath.Join(xdgRoot, "gym"); got != want {
					t.Fatalf("%s = %q, want %q", k.name, got, want)
				}
			})
		}
	}
}

// With XDG_* unset, an existing previous directory is found on every OS;
// without one, a fresh HOME gets kit's native location.
func TestAppDirsUnsetXDG(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name+"/old-present", func(t *testing.T) {
			home := isolate(t)
			old := filepath.Join(append([]string{home}, append(k.prev, "gym")...)...)
			mk(t, old)
			d, err := xdg.AppDirs("gym")
			if err != nil {
				t.Fatal(err)
			}
			if got := k.get(d); got != old {
				t.Fatalf("%s = %q, want previous %q", k.name, got, old)
			}
		})
		t.Run(k.name+"/fresh", func(t *testing.T) {
			home := isolate(t)
			d, err := xdg.AppDirs("gym")
			if err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(append([]string{home}, append(k.prev, "gym")...)...)
			got := k.get(d)
			if runtime.GOOS == "linux" {
				if got != old {
					t.Fatalf("%s = %q, want %q", k.name, got, old)
				}
				return
			}
			if got == old {
				t.Fatalf("%s = previous dir %q on fresh HOME, want native", k.name, got)
			}
		})
	}
}

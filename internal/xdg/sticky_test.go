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

// The "native" directory is driven through XDG_* (so the case is real on
// Linux, where kit's default equals the previous directory); the previous
// directory lives under HOME.
func TestAppDirsStickiness(t *testing.T) {
	for _, k := range kinds {
		for _, tc := range []struct {
			name        string
			native, old bool
			wantOld     bool
		}{
			{"native-missing old-present uses old", false, true, true},
			{"native-present beats old", true, true, false},
			{"neither uses native", false, false, false},
			{"native-only uses native", true, false, false},
		} {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				home := isolate(t)
				xdgRoot := filepath.Join(t.TempDir(), "xdgroot")
				mk(t, xdgRoot)
				t.Setenv(k.env, xdgRoot)
				native := filepath.Join(xdgRoot, "gym")
				old := filepath.Join(append([]string{home}, append(k.prev, "gym")...)...)
				if tc.native {
					mk(t, native)
				}
				if tc.old {
					mk(t, old)
				}
				d, err := xdg.AppDirs("gym")
				if err != nil {
					t.Fatal(err)
				}
				want := native
				if tc.wantOld {
					want = old
				}
				if got := k.get(d); got != want {
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

package xdg_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	kitxdg "hop.top/kit/go/core/xdg"

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

func prevDir(home string, k kind) string {
	return filepath.Join(append([]string{home}, append(k.prev, "gym")...)...)
}

// Any previous dir makes the whole install legacy: every kind with its
// variable unset uses its previous location, existing or not, even when
// kit's native dir exists (e.g. an interim build created it).
func TestAppDirsLegacyInstallWholeDecision(t *testing.T) {
	for _, have := range kinds {
		t.Run("only-"+have.name+"-previous-exists", func(t *testing.T) {
			home := isolate(t)
			mk(t, prevDir(home, have))
			// Pre-create kit's native dirs, as an interim build would.
			for _, f := range []func(string) (string, error){
				kitxdg.RawConfigDir, kitxdg.RawDataDir, kitxdg.RawStateDir, kitxdg.RawCacheDir,
			} {
				n, err := f("gym")
				if err != nil {
					t.Fatal(err)
				}
				mk(t, n)
			}
			d, err := xdg.AppDirs("gym")
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range kinds {
				if got, want := k.get(d), prevDir(home, k); got != want {
					t.Fatalf("%s = %q, want %q", k.name, got, want)
				}
			}
		})
	}
}

// Mixed env: the set variable wins for its kind, the rest follow the
// legacy install.
func TestAppDirsMixedEnv(t *testing.T) {
	home := isolate(t)
	mk(t, prevDir(home, kinds[1])) // data previous exists
	cfgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgRoot)
	d, err := xdg.AppDirs("gym")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.ConfigHome, filepath.Join(cfgRoot, "gym"); got != want {
		t.Fatalf("ConfigHome = %q, want %q", got, want)
	}
	for _, k := range kinds[1:] {
		if got, want := k.get(d), prevDir(home, k); got != want {
			t.Fatalf("%s = %q, want %q", k.name, got, want)
		}
	}
}

// Fresh HOME: native for all kinds. On Linux native equals the previous
// location, so the macOS-only assertion is that they differ there.
func TestAppDirsFreshHome(t *testing.T) {
	home := isolate(t)
	d, err := xdg.AppDirs("gym")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range kinds {
		got := k.get(d)
		if runtime.GOOS == "linux" {
			if got != prevDir(home, k) {
				t.Fatalf("%s = %q", k.name, got)
			}
		} else if got == prevDir(home, k) {
			t.Fatalf("%s = previous dir %q on fresh HOME, want native", k.name, got)
		}
	}
}

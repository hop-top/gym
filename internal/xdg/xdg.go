// Package xdg is a thin compatibility shim over hop.top/kit/go/core/xdg.
//
// New code SHOULD import kitxdg directly. This package preserves the
// AppDirs/Dirs/Ensure surface used across gym so call sites keep working
// during the kit-adoption migration. Slated for removal once all callers
// migrate.
package xdg

import (
	"os"
	"path/filepath"

	kitxdg "hop.top/kit/go/core/xdg"
)

// Dirs contains XDG-compliant directories for app data.
type Dirs struct {
	ConfigHome string
	DataHome   string
	CacheHome  string
	StateHome  string
}

// AppDirs resolves per-app directories via kit/core/xdg, which delegates
// to github.com/adrg/xdg for full XDG + OS-native fallback support.
//
// Installs made before the move to OS-native dirs used
// $HOME/.config/<app>, .local/share, .local/state and .cache. If any of
// those exists the install is treated as one XDG-style install and every
// kind whose XDG_* variable is unset keeps using its previous location (see
// pick). Fresh installs get kit's native dirs.
//
// Uses Raw* variants (no scope guard) since gym needs to construct paths
// without I/O side effects; Ensure is a separate step.
func AppDirs(appName string) (Dirs, error) {
	cfg, err := kitxdg.RawConfigDir(appName)
	if err != nil {
		return Dirs{}, err
	}
	data, err := kitxdg.RawDataDir(appName)
	if err != nil {
		return Dirs{}, err
	}
	cache, err := kitxdg.RawCacheDir(appName)
	if err != nil {
		return Dirs{}, err
	}
	state, err := kitxdg.RawStateDir(appName)
	if err != nil {
		return Dirs{}, err
	}
	home, _ := os.UserHomeDir()
	prev := func(parts ...string) string {
		if home == "" {
			return ""
		}
		return filepath.Join(append([]string{home}, append(parts, appName)...)...)
	}
	prevs := Dirs{
		ConfigHome: prev(".config"),
		DataHome:   prev(".local", "share"),
		CacheHome:  prev(".cache"),
		StateHome:  prev(".local", "state"),
	}
	legacy := legacyInstall(prevs)
	return Dirs{
		ConfigHome: pick(cfg, prevs.ConfigHome, os.Getenv("XDG_CONFIG_HOME") != "", legacy),
		DataHome:   pick(data, prevs.DataHome, os.Getenv("XDG_DATA_HOME") != "", legacy),
		CacheHome:  pick(cache, prevs.CacheHome, os.Getenv("XDG_CACHE_HOME") != "", legacy),
		StateHome:  pick(state, prevs.StateHome, os.Getenv("XDG_STATE_HOME") != "", legacy),
	}, nil
}

// legacyInstall reports whether any previous XDG-style gym dir exists. It
// is decided once for all kinds: on macOS kit's native config, data and
// state dirs coincide, so a per-kind check would flip to native as soon as
// Ensure created a missing sibling there.
func legacyInstall(prevs Dirs) bool {
	for _, p := range []string{prevs.ConfigHome, prevs.DataHome, prevs.CacheHome, prevs.StateHome} {
		if p != "" && isDir(p) {
			return true
		}
	}
	return false
}

// pick is the single owner of the rule for one kind: an explicitly set XDG
// variable wins (kit's dir, as before the move); else an existing
// XDG-style install keeps its previous location even if that dir is not
// created yet; else kit's native dir.
func pick(native, prev string, envSet, legacy bool) string {
	if envSet || !legacy || prev == "" {
		return native
	}
	return prev
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Ensure creates all app directories with 0750 (matches kit's MustEnsure).
func Ensure(d Dirs) error {
	for _, p := range []string{d.ConfigHome, d.DataHome, d.CacheHome, d.StateHome} {
		if err := os.MkdirAll(p, 0o750); err != nil {
			return err
		}
	}
	return nil
}

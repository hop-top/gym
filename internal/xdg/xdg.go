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
// Each directory keeps using the previous XDG-style location
// ($HOME/.config/<app>, .local/share, .local/state, .cache) when kit's
// choice does not exist yet and the previous one does, so installs made
// before the move to OS-native dirs keep their config and state.
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
	return Dirs{
		ConfigHome: sticky(cfg, appName, ".config"),
		DataHome:   sticky(data, appName, ".local", "share"),
		CacheHome:  sticky(cache, appName, ".cache"),
		StateHome:  sticky(state, appName, ".local", "state"),
	}, nil
}

// sticky is the single owner of the previous-directory rule: native (kit's
// directory) wins when it exists; otherwise an existing previous XDG-style
// directory under $HOME wins; otherwise native.
func sticky(native, appName string, prevParts ...string) string {
	if isDir(native) {
		return native
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return native
	}
	prev := filepath.Join(append([]string{home}, append(prevParts, appName)...)...)
	if isDir(prev) {
		return prev
	}
	return native
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

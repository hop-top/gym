// Package xdg is a thin compatibility shim over hop.top/kit/go/core/xdg.
//
// New code SHOULD import kitxdg directly. This package preserves the
// AppDirs/Dirs/Ensure surface used across gym so call sites keep working
// during the kit-adoption migration. Slated for removal once all callers
// migrate.
package xdg

import (
	"os"

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
		ConfigHome: cfg,
		DataHome:   data,
		CacheHome:  cache,
		StateHome:  state,
	}, nil
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

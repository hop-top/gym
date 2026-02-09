package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dirs contains XDG-compliant directories for app data.
type Dirs struct {
	ConfigHome string
	DataHome   string
	CacheHome  string
	StateHome  string
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// AppDirs resolves per-app directories following XDG and HOME fallback rules.
func AppDirs(appName string) (Dirs, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return Dirs{}, fmt.Errorf("HOME is not set")
	}

	cfgBase := envOrDefault("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dataBase := envOrDefault("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	cacheBase := envOrDefault("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	stateBase := envOrDefault("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))

	return Dirs{
		ConfigHome: filepath.Join(cfgBase, appName),
		DataHome:   filepath.Join(dataBase, appName),
		CacheHome:  filepath.Join(cacheBase, appName),
		StateHome:  filepath.Join(stateBase, appName),
	}, nil
}

// Ensure creates all app directories.
func Ensure(d Dirs) error {
	for _, p := range []string{d.ConfigHome, d.DataHome, d.CacheHome, d.StateHome} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

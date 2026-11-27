package cli

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/spf13/viper"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/xdg"
)

// cliFile is the CLI-defaults file name (cli.yaml) in .gym/ and in the user
// config dir.
const cliFile = "cli"

// A project's .gym/cli.yaml is repository content, like its config.yaml. Only
// keys that narrow what a run may do are honoured from it; every other key is
// ignored with a warning, so a key added later defaults to ignored. The user's
// cli.yaml and real flags are the operator's: unrestricted, and they win.
//
//   - learn.agents: filters the operator's configured install paths.
//   - train.no_global: skips global mirroring; honoured only when true.
//   - strict, gym.strict: tightening; honoured only when true. (Today no
//     file-sourced value reaches the strict check, so this is inert.)
//
// Ignored on purpose: learn.global (flips the install scope to the operator's
// global dirs), learn.path (installs anywhere), insecure, gate-timeout, bus,
// max-data-class, refresh-judgement, and any other key.
var projectCLIKeys = map[string]func(v any) bool{
	"learn.agents":    func(any) bool { return true },
	"train.no_global": isTrue,
	"strict":          isTrue,
	"gym.strict":      isTrue,
}

func isTrue(v any) bool { b, ok := v.(bool); return ok && b }

// loadCLIConfig reads the user cli.yaml and the allowed keys of the project
// cli.yaml into vp. The project file is read on its own and filtered before
// merging, never through vp's merged view; the user file is merged last so it
// wins over the project.
func (a *App) loadCLIConfig(vp *viper.Viper) error {
	vp.SetConfigType("yaml")

	allowed := map[string]any{}
	pv := viper.New()
	pv.SetConfigType("yaml")
	pv.SetConfigFile(filepath.Join(a.CWD, ".gym", cliFile+".yaml"))
	if err := pv.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	} else if err == nil {
		for _, k := range pv.AllKeys() {
			if ok, honour := projectCLIKeys[k]; honour && ok(pv.Get(k)) {
				allowed[k] = pv.Get(k)
				continue
			}
			a.Logger().Warnf("cli: ignoring project key %q (user config or flags only)", k)
		}
	}
	if len(allowed) > 0 {
		if err := vp.MergeConfigMap(nestKeys(allowed)); err != nil {
			return err
		}
	}

	if dirs, err := xdg.AppDirs(config.AppName); err == nil {
		uv := viper.New()
		uv.SetConfigType("yaml")
		uv.SetConfigFile(filepath.Join(dirs.ConfigHome, cliFile+".yaml"))
		if err := uv.ReadInConfig(); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		return vp.MergeConfigMap(uv.AllSettings())
	}
	return nil
}

// nestKeys turns dotted keys into the nested map viper merges.
func nestKeys(flat map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range flat {
		parts := splitDots(k)
		m := out
		for _, p := range parts[:len(parts)-1] {
			next, ok := m[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[p] = next
			}
			m = next
		}
		m[parts[len(parts)-1]] = v
	}
	return out
}

func splitDots(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

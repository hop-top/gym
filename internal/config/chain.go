package config

import (
	"os"
	"path/filepath"

	kitconfig "hop.top/kit/go/core/config"

	"hop.top/gym/internal/project"
)

// A folder scope inherits from the scopes above it. The config of every
// root in the chain (see scope.Chain) is read, never just the nearest, and
// folded outermost to innermost:
//
//   - admission.max_data_class, require_manifest, require_approval,
//     require_pin: the Clamp* functions; an inner folder can tighten, never
//     loosen.
//   - install_paths: the innermost layer that sets it replaces the list;
//     each entry must stay inside the folder that wrote it.
//   - repositories, curation, upgrade: innermost wins.
//   - every other key is ignored (see projectKeys).
//
// User and system config come first as the base; -c files and overrides
// come last and are the operator's, so they are not clamped.

// layerConfigPath is the config file of the folder at root:
// .gym/config.yaml, else a legacy .gym/config.json, else "".
func layerConfigPath(root string) string {
	dir := filepath.Join(root, project.ProjectDirName)
	for _, name := range []string{project.ProjectConfigFile, project.ProjectLegacyConfigFile} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// layerPaths are the config files of roots (innermost first) in fold
// order, outermost first. Roots without a config file contribute nothing.
func layerPaths(roots []string) []string {
	var out []string
	for i := len(roots) - 1; i >= 0; i-- {
		if p := layerConfigPath(roots[i]); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// foldAdmission folds one folder layer into acc. A layer's unset values
// ("" or false) leave acc alone; set values pass through the Clamp*
// functions so an inner folder cannot undo an outer one's tightening.
//
// admission.require_pin folds here with ClampRequirePin. Config keys that
// arrive with later work (network.mode, network.allow_hosts) fold here too,
// each with its own clamp, alongside these.
func foldAdmission(acc, layer AdmissionConfig) AdmissionConfig {
	return AdmissionConfig{
		MaxDataClass:    ClampMaxDataClass(acc.MaxDataClass, layer.MaxDataClass),
		RequireManifest: ClampManifestMode(acc.RequireManifest, layer.RequireManifest),
		RequireApproval: ClampRequireApproval(acc.RequireApproval, layer.RequireApproval),
		RequirePin:      ClampRequirePin(acc.RequirePin, layer.RequirePin),
	}
}

// LoadScoped loads the configuration for the folder scope at scopeRoot
// whose chain is roots (innermost first, from scope.Chain). Relative
// project-scope install paths resolve against scopeRoot, which need not be
// a root yet (a scope about to be created). The warnings are the ignored
// keys; the chain's own warnings are the caller's.
func LoadScoped(scopeRoot string, roots []string, paths []string, overrides map[string]any) (Config, []string, error) {
	return loadChained(layerOptions(scopeRoot), scopeRoot, roots, paths, overrides)
}

// loadChained layers defaults < system/user (from opts) < the allowed keys
// of each folder in roots, outermost to innermost < extra config files <
// key=value overrides, via kit. The folder layers are repository content,
// so they are not given to kit: each is read on its own and only its
// allowed keys are copied on (see applyProjectLayer).
func loadChained(opts kitconfig.Options, scopeRoot string, roots []string, paths []string, overrides map[string]any) (Config, []string, error) {
	defaults := defaultConfig()
	baseOpts := opts
	baseOpts.Defaults = &defaults
	baseOpts.ProjectConfigPath = ""
	baseOpts.ExtraConfigPaths = nil
	baseOpts.Overrides = nil

	var cfg Config
	if err := kitconfig.Load(&cfg, baseOpts); err != nil {
		return Config{}, nil, err
	}
	layers := layerPaths(roots)
	var warnings []string
	for _, path := range layers {
		w, err := applyProjectLayer(&cfg, path, scopeRoot)
		if err != nil {
			return Config{}, nil, err
		}
		warnings = append(warnings, w...)
	}
	if len(paths) > 0 || len(overrides) > 0 {
		var out Config
		if err := kitconfig.Load(&out, kitconfig.Options{
			Defaults:         &cfg,
			ExtraConfigPaths: paths,
			Overrides:        overrides,
		}); err != nil {
			return Config{}, nil, err
		}
		cfg = out
	}
	var err error
	if cfg.Admission, err = loadAdmission(opts, layers, paths, overrides); err != nil {
		return Config{}, nil, err
	}
	normalizePaths(&cfg, scopeRoot)
	return cfg, warnings, nil
}

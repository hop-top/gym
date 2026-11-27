package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	kitconfig "hop.top/kit/go/core/config"

	"hop.top/gym/internal/project"
)

const AppName = "gym"

// RepositoryConfig configures a registry source.
type RepositoryConfig struct {
	Name    string `json:"name" yaml:"name"`
	URL     string `json:"url" yaml:"url"`
	Curated bool   `json:"curated" yaml:"curated"`
}

// InstallPath defines where skills are installed.
type InstallPath struct {
	Name  string `json:"name" yaml:"name"`
	Path  string `json:"path" yaml:"path"`
	Scope string `json:"scope" yaml:"scope"` // global|project
}

type CurationConfig struct {
	MinStars int `json:"min_stars" yaml:"min_stars"`
}

type ValidatorConfig struct {
	Command        []string `json:"command" yaml:"command"`
	TimeoutSeconds int      `json:"timeout_seconds" yaml:"timeout_seconds"`
}

type UpgradeConfig struct {
	MirrorGlobalFromProject bool `json:"mirror_global_from_project" yaml:"mirror_global_from_project"`
}

// Config is the effective runtime configuration.
type Config struct {
	Repositories []RepositoryConfig `json:"repositories" yaml:"repositories"`
	InstallPaths []InstallPath      `json:"install_paths" yaml:"install_paths"`
	Curation     CurationConfig     `json:"curation" yaml:"curation"`
	Validator    ValidatorConfig    `json:"validator" yaml:"validator"`
	Upgrade      UpgradeConfig      `json:"upgrade" yaml:"upgrade"`
}

func defaultConfig() Config {
	return Config{
		Repositories: nil,
		InstallPaths: nil,
		Curation: CurationConfig{
			MinStars: 100,
		},
		Validator: ValidatorConfig{
			Command:        []string{"skills-ref", "test", "{path}"},
			TimeoutSeconds: 20,
		},
		Upgrade: UpgradeConfig{
			MirrorGlobalFromProject: true,
		},
	}
}

func normalizePaths(cfg *Config, cwd string) {
	home := os.Getenv("HOME")
	for i := range cfg.InstallPaths {
		p := cfg.InstallPaths[i].Path
		if strings.HasPrefix(p, "~/") {
			cfg.InstallPaths[i].Path = filepath.Join(home, p[2:])
			continue
		}
		if cfg.InstallPaths[i].Scope == "project" && !filepath.IsAbs(p) {
			cfg.InstallPaths[i].Path = filepath.Join(cwd, p)
		}
	}
}

// projectMarkers are the project config locations kit walks up from cwd.
var projectMarkers = []string{
	filepath.Join(project.ProjectDirName, project.ProjectConfigFile),
}

// layerOptions returns kit's system, user and project config locations for
// gym: /etc/gym/config.yaml, <user config dir>/config.yaml and the nearest
// .gym/config.yaml at or above cwd.
func layerOptions(cwd string) kitconfig.Options {
	return kitconfig.OptionsForToolWithMarkersFrom(cwd, AppName, projectMarkers)
}

// loadConfig layers defaults < opts' system/user/project files < extra
// config files < key=value overrides, via kit.
func loadConfig(cwd string, opts kitconfig.Options, paths []string, overrides map[string]any) (Config, error) {
	defaults := defaultConfig()
	opts.Defaults = &defaults
	opts.ExtraConfigPaths = paths
	opts.Overrides = overrides

	var cfg Config
	if err := kitconfig.Load(&cfg, opts); err != nil {
		return Config{}, err
	}
	normalizePaths(&cfg, cwd)
	return cfg, nil
}

// LoadMergedConfig loads the layered configuration. paths and overrides are
// the extra config files and key=value overrides parsed from -c/--config.
func LoadMergedConfig(cwd string, paths []string, overrides map[string]any) (Config, error) {
	return loadConfig(cwd, layerOptions(cwd), paths, overrides)
}

// loadLayer reads a single config file (no defaults, no other layers).
// ok is false when the file does not exist.
func loadLayer(path string) (cfg Config, ok bool, err error) {
	if path == "" {
		return Config{}, false, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return Config{}, false, nil
	} else if err != nil {
		return Config{}, false, err
	}
	err = kitconfig.Load(&cfg, kitconfig.Options{ExtraConfigPaths: []string{path}})
	return cfg, err == nil, err
}

func upsertInstallPath(paths []InstallPath, add InstallPath) []InstallPath {
	for i := range paths {
		if paths[i].Name == add.Name && add.Name != "" {
			paths[i] = add
			return paths
		}
		if paths[i].Path == add.Path && paths[i].Scope == add.Scope {
			paths[i] = add
			return paths
		}
	}
	return append(paths, add)
}

func upsertRepository(repos []RepositoryConfig, add RepositoryConfig) []RepositoryConfig {
	for i := range repos {
		if repos[i].URL == add.URL && add.URL != "" {
			repos[i] = add
			return repos
		}
		if repos[i].Name == add.Name && add.Name != "" {
			repos[i] = add
			return repos
		}
	}
	return append(repos, add)
}

// writeTarget is where a mutation lands: a scope plus kit options naming
// the file for that scope.
type writeTarget struct {
	scope kitconfig.Scope
	opts  kitconfig.Options
	path  string
}

// resolveConfigPath maps target ("user" or "project") to the file kit
// reads for that layer. A project target without an existing project
// config falls back to the detected project root, else cwd.
func resolveConfigPath(cwd, target string) (writeTarget, error) {
	opts := layerOptions(cwd)
	switch target {
	case "user":
		if opts.UserConfigPath == "" {
			return writeTarget{}, errors.New("cannot resolve user config path")
		}
		return writeTarget{kitconfig.ScopeUser, opts, opts.UserConfigPath}, nil
	case "project":
		if opts.ProjectConfigPath == "" {
			root, ok := project.DetectRoot(cwd)
			if !ok {
				root = cwd
			}
			opts.ProjectConfigPath = filepath.Join(root, projectMarkers[0])
		}
		return writeTarget{kitconfig.ScopeProject, opts, opts.ProjectConfigPath}, nil
	default:
		return writeTarget{}, errors.New(`target must be "user" or "project"`)
	}
}

// AddInstallPath appends or replaces an install path in user or project config.
// target must be "user" or "project".
func AddInstallPath(cwd, target string, p InstallPath) (string, error) {
	if p.Path == "" {
		return "", errors.New("install path cannot be empty")
	}
	wt, err := resolveConfigPath(cwd, target)
	if err != nil {
		return "", err
	}

	cfg, _, err := loadLayer(wt.path)
	if err != nil {
		return "", err
	}
	paths := upsertInstallPath(cfg.InstallPaths, p)
	if err := kitconfig.SetValue("install_paths", paths, wt.scope, wt.opts); err != nil {
		return "", err
	}
	return wt.path, nil
}

// AddRepository appends or replaces a repository source in user or project config.
// target must be "user" or "project".
func AddRepository(cwd, target string, r RepositoryConfig) (string, error) {
	if r.URL == "" {
		return "", errors.New("repository url cannot be empty")
	}
	wt, err := resolveConfigPath(cwd, target)
	if err != nil {
		return "", err
	}

	cfg, _, err := loadLayer(wt.path)
	if err != nil {
		return "", err
	}
	repos := upsertRepository(cfg.Repositories, r)
	if err := kitconfig.SetValue("repositories", repos, wt.scope, wt.opts); err != nil {
		return "", err
	}
	return wt.path, nil
}

// RemoveRepository removes repository entries matching selector (name or url) from user or project config.
// Returns number removed.
func RemoveRepository(cwd, target, selector string) (string, int, error) {
	if selector == "" {
		return "", 0, errors.New("selector cannot be empty")
	}
	wt, err := resolveConfigPath(cwd, target)
	if err != nil {
		return "", 0, err
	}

	cfg, _, err := loadLayer(wt.path)
	if err != nil {
		return "", 0, err
	}

	kept := make([]RepositoryConfig, 0, len(cfg.Repositories))
	removed := 0
	for _, r := range cfg.Repositories {
		if r.Name == selector || r.URL == selector {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	if err := kitconfig.SetValue("repositories", kept, wt.scope, wt.opts); err != nil {
		return "", 0, err
	}
	return wt.path, removed, nil
}

// LoadUserRepositories reads repositories from user config (if present).
func LoadUserRepositories() (string, []RepositoryConfig, bool, error) {
	path := layerOptions("").UserConfigPath
	cfg, ok, err := loadLayer(path)
	if err != nil {
		return "", nil, false, err
	}
	return path, cfg.Repositories, ok, nil
}

// LoadProjectRepositories reads repositories from nearest project config (if present).
func LoadProjectRepositories(cwd string) (string, []RepositoryConfig, bool, error) {
	path := layerOptions(cwd).ProjectConfigPath
	if path == "" {
		return "", nil, false, nil
	}
	cfg, ok, err := loadLayer(path)
	if err != nil {
		return "", nil, false, err
	}
	return path, cfg.Repositories, ok, nil
}

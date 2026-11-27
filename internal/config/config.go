package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	kitconfig "hop.top/kit/go/core/config"

	"gopkg.in/yaml.v3"

	"hop.top/gym/internal/project"
	"hop.top/gym/internal/xdg"
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
	Admission    AdmissionConfig    `json:"admission" yaml:"admission"`
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
		Admission: DefaultAdmission(),
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
// Kit tries them in order per directory, so config.yaml wins over a legacy
// config.json in the same .gym directory.
var projectMarkers = []string{
	filepath.Join(project.ProjectDirName, project.ProjectConfigFile),
	filepath.Join(project.ProjectDirName, project.ProjectLegacyConfigFile),
}

const (
	userConfigFile       = "config.yaml"
	userLegacyConfigFile = "config.json"
)

// userConfigDir is the user config directory, honoring the previous
// XDG-style location for existing installs (see internal/xdg).
func userConfigDir() (string, error) {
	dirs, err := xdg.AppDirs(AppName)
	if err != nil {
		return "", err
	}
	return dirs.ConfigHome, nil
}

// userConfigPath is the user layer file: config.yaml, else an existing
// config.json from before YAML became canonical, else the canonical path.
func userConfigPath() string {
	dir, err := userConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	if fileExists(filepath.Join(dir, userConfigFile)) {
		return filepath.Join(dir, userConfigFile)
	}
	if legacy := filepath.Join(dir, userLegacyConfigFile); fileExists(legacy) {
		return legacy
	}
	return filepath.Join(dir, userConfigFile)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// layerOptions returns kit's system, user and project config locations for
// gym: /etc/gym/config.yaml, the user config file (see userConfigPath) and
// the nearest .gym/config.yaml or .gym/config.json at or above cwd.
func layerOptions(cwd string) kitconfig.Options {
	opts := kitconfig.OptionsForToolWithMarkersFrom(cwd, AppName, projectMarkers)
	if p := userConfigPath(); p != "" {
		opts.UserConfigPath = p
	}
	return opts
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
	var err error
	if cfg.Admission, err = loadAdmission(opts, paths, overrides); err != nil {
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

// resolveConfigPath maps target ("user" or "project") to the canonical
// config.yaml for that layer. Mutations always land there; when only a
// legacy config.json exists beside it, its content is first copied over so
// existing settings survive. The legacy file is never modified. A project
// target without an existing project config falls back to the detected
// project root, else cwd.
func resolveConfigPath(cwd, target string) (writeTarget, error) {
	opts := layerOptions(cwd)
	var wt writeTarget
	switch target {
	case "user":
		if opts.UserConfigPath == "" {
			return writeTarget{}, errors.New("cannot resolve user config path")
		}
		canon := filepath.Join(filepath.Dir(opts.UserConfigPath), userConfigFile)
		opts.UserConfigPath = canon
		wt = writeTarget{kitconfig.ScopeUser, opts, canon}
	case "project":
		var canon string
		if opts.ProjectConfigPath != "" {
			canon = filepath.Join(filepath.Dir(opts.ProjectConfigPath), project.ProjectConfigFile)
		} else {
			root, ok := project.DetectRoot(cwd)
			if !ok {
				root = cwd
			}
			canon = filepath.Join(root, projectMarkers[0])
		}
		opts.ProjectConfigPath = canon
		wt = writeTarget{kitconfig.ScopeProject, opts, canon}
	default:
		return writeTarget{}, errors.New(`target must be "user" or "project"`)
	}
	if err := adoptLegacy(wt.path); err != nil {
		return writeTarget{}, err
	}
	return wt, nil
}

// adoptLegacy writes canonical from the config.json beside it when only
// the legacy file exists. The JSON is decoded and re-marshaled as YAML
// rather than byte-copied: YAML reads some valid JSON differently (surrogate
// pair escapes, "\/"). Content that is not JSON must at least be valid YAML,
// else it is refused rather than half-migrated.
func adoptLegacy(canonical string) error {
	if fileExists(canonical) {
		return nil
	}
	legacy := filepath.Join(filepath.Dir(canonical), strings.TrimSuffix(filepath.Base(canonical), ".yaml")+".json")
	raw, err := os.ReadFile(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	out, err := jsonToYAML(raw)
	if err != nil {
		return fmt.Errorf("legacy config %s: %w", legacy, err)
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o750); err != nil {
		return err
	}
	return os.WriteFile(canonical, out, 0o644)
}

func jsonToYAML(raw []byte) ([]byte, error) {
	var node yaml.Node
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		if yerr := yaml.Unmarshal(raw, &node); yerr != nil {
			return nil, yerr
		}
		return raw, nil
	}
	return yaml.Marshal(normalizeJSON(v))
}

// normalizeJSON turns json.Number into int64 or float64 so YAML emits plain
// numbers.
func normalizeJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalizeJSON(x)
		}
	case []any:
		for i, x := range t {
			t[i] = normalizeJSON(x)
		}
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	}
	return v
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

package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/hop-top/gym/internal/project"
	"github.com/hop-top/gym/internal/xdg"
)

const AppName = "gym"

// RepositoryConfig configures a registry source.
type RepositoryConfig struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Curated bool   `json:"curated"`
}

// InstallPath defines where skills are installed.
type InstallPath struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Scope string `json:"scope"` // global|project
}

type CurationConfig struct {
	MinStars int `json:"min_stars"`
}

type ValidatorConfig struct {
	Command        []string `json:"command"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type UpgradeConfig struct {
	MirrorGlobalFromProject bool `json:"mirror_global_from_project"`
}

// Config is the effective runtime configuration.
type Config struct {
	Repositories []RepositoryConfig `json:"repositories"`
	InstallPaths []InstallPath      `json:"install_paths"`
	Curation     CurationConfig     `json:"curation"`
	Validator    ValidatorConfig    `json:"validator"`
	Upgrade      UpgradeConfig      `json:"upgrade"`
}

// Source captures which config files were loaded.
type Source struct {
	HasUser     bool
	UserFile    string
	HasProject  bool
	ProjectFile string
}

type fileConfig struct {
	Repositories []RepositoryConfig `json:"repositories"`
	InstallPaths []InstallPath      `json:"install_paths"`
	Curation     *CurationConfig    `json:"curation"`
	Validator    *ValidatorConfig   `json:"validator"`
	Upgrade      *UpgradeConfig     `json:"upgrade"`
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

func merge(dst *Config, src fileConfig) {
	if len(src.Repositories) > 0 {
		dst.Repositories = append(dst.Repositories, src.Repositories...)
	}
	if len(src.InstallPaths) > 0 {
		dst.InstallPaths = append(dst.InstallPaths, src.InstallPaths...)
	}
	if src.Curation != nil {
		if src.Curation.MinStars > 0 {
			dst.Curation.MinStars = src.Curation.MinStars
		}
	}
	if src.Validator != nil {
		if len(src.Validator.Command) > 0 {
			dst.Validator.Command = append([]string(nil), src.Validator.Command...)
		}
		if src.Validator.TimeoutSeconds > 0 {
			dst.Validator.TimeoutSeconds = src.Validator.TimeoutSeconds
		}
	}
	if src.Upgrade != nil {
		dst.Upgrade.MirrorGlobalFromProject = src.Upgrade.MirrorGlobalFromProject
	}
}

func loadFile(path string) (fileConfig, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileConfig{}, false, nil
	}
	if err != nil {
		return fileConfig{}, false, err
	}
	var c fileConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return fileConfig{}, false, err
	}
	return c, true, nil
}

func writeFile(path string, cfg fileConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	blob = append(blob, '\n')
	return os.WriteFile(path, blob, 0o644)
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

// LoadMergedConfig loads user and project configuration and merges them.
func LoadMergedConfig(cwd string) (Config, Source, error) {
	cfg := defaultConfig()
	src := Source{}

	dirs, err := xdg.AppDirs(AppName)
	if err != nil {
		return Config{}, Source{}, err
	}
	userPath := filepath.Join(dirs.ConfigHome, "config.json")
	userCfg, ok, err := loadFile(userPath)
	if err != nil {
		return Config{}, Source{}, err
	}
	if ok {
		src.HasUser = true
		src.UserFile = userPath
		merge(&cfg, userCfg)
	}

	if root, ok := project.DetectRoot(cwd); ok {
		projectPath := filepath.Join(root, project.ProjectDirName, project.ProjectConfigFile)
		projectCfg, found, err := loadFile(projectPath)
		if err != nil {
			return Config{}, Source{}, err
		}
		if found {
			src.HasProject = true
			src.ProjectFile = projectPath
			merge(&cfg, projectCfg)
		}
	}

	normalizePaths(&cfg, cwd)
	return cfg, src, nil
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

func resolveConfigPath(cwd, target string) (string, error) {
	switch target {
	case "user":
		dirs, err := xdg.AppDirs(AppName)
		if err != nil {
			return "", err
		}
		return filepath.Join(dirs.ConfigHome, "config.json"), nil
	case "project":
		root, ok := project.DetectRoot(cwd)
		if !ok {
			root = cwd
		}
		if _, err := project.EnsureProjectDir(root); err != nil {
			return "", err
		}
		return filepath.Join(root, project.ProjectDirName, project.ProjectConfigFile), nil
	default:
		return "", errors.New(`target must be "user" or "project"`)
	}
}

// AddInstallPath appends or replaces an install path in user or project config.
// target must be "user" or "project".
func AddInstallPath(cwd, target string, p InstallPath) (string, error) {
	if p.Path == "" {
		return "", errors.New("install path cannot be empty")
	}
	cfgPath, err := resolveConfigPath(cwd, target)
	if err != nil {
		return "", err
	}

	cfg, _, err := loadFile(cfgPath)
	if err != nil {
		return "", err
	}
	cfg.InstallPaths = upsertInstallPath(cfg.InstallPaths, p)
	if err := writeFile(cfgPath, cfg); err != nil {
		return "", err
	}
	return cfgPath, nil
}

// AddRepository appends or replaces a repository source in user or project config.
// target must be "user" or "project".
func AddRepository(cwd, target string, r RepositoryConfig) (string, error) {
	if r.URL == "" {
		return "", errors.New("repository url cannot be empty")
	}
	cfgPath, err := resolveConfigPath(cwd, target)
	if err != nil {
		return "", err
	}

	cfg, _, err := loadFile(cfgPath)
	if err != nil {
		return "", err
	}
	cfg.Repositories = upsertRepository(cfg.Repositories, r)
	if err := writeFile(cfgPath, cfg); err != nil {
		return "", err
	}
	return cfgPath, nil
}

// RemoveRepository removes repository entries matching selector (name or url) from user or project config.
// Returns number removed.
func RemoveRepository(cwd, target, selector string) (string, int, error) {
	if selector == "" {
		return "", 0, errors.New("selector cannot be empty")
	}
	cfgPath, err := resolveConfigPath(cwd, target)
	if err != nil {
		return "", 0, err
	}

	cfg, _, err := loadFile(cfgPath)
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
	cfg.Repositories = kept
	if err := writeFile(cfgPath, cfg); err != nil {
		return "", 0, err
	}
	return cfgPath, removed, nil
}

func userConfigPath() (string, error) {
	dirs, err := xdg.AppDirs(AppName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dirs.ConfigHome, "config.json"), nil
}

func projectConfigPath(cwd string) (string, bool) {
	root, ok := project.DetectRoot(cwd)
	if !ok {
		return "", false
	}
	return filepath.Join(root, project.ProjectDirName, project.ProjectConfigFile), true
}

// LoadUserRepositories reads repositories from user config (if present).
func LoadUserRepositories() (string, []RepositoryConfig, bool, error) {
	path, err := userConfigPath()
	if err != nil {
		return "", nil, false, err
	}
	cfg, ok, err := loadFile(path)
	if err != nil {
		return "", nil, false, err
	}
	return path, cfg.Repositories, ok, nil
}

// LoadProjectRepositories reads repositories from nearest project config (if present).
func LoadProjectRepositories(cwd string) (string, []RepositoryConfig, bool, error) {
	path, ok := projectConfigPath(cwd)
	if !ok {
		return "", nil, false, nil
	}
	cfg, found, err := loadFile(path)
	if err != nil {
		return "", nil, false, err
	}
	return path, cfg.Repositories, found, nil
}

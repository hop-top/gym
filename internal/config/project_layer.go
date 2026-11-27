package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"hop.top/gym/internal/project"
)

// A project's .gym/config.yaml (or config.json) is repository content:
// whoever cloned the repository did not write it. Only the keys below are
// honoured from it; every other key is ignored with a warning, so a key
// added to Config later defaults to ignored. User and system config, and
// -c files and overrides, are the operator's and are not restricted.
//
// admission is handled by loadAdmission (tighten-only clamp), so it is
// neither merged here nor warned about.
var projectKeys = map[string]bool{
	"install_paths": true, // contained to the project tree (see sanitizeProjectPaths)
	"repositories":  true,
	"curation":      true,
	"upgrade":       true,
}

const projectAdmissionKey = "admission"

// readProjectLayer parses the project layer alone into its top-level keys.
// A missing or empty file yields no keys. JSON is valid YAML, so the legacy
// config.json parses the same way.
func readProjectLayer(path string) (map[string]any, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	return m, nil
}

// projectRootOf is the project root for a project config path
// (<root>/.gym/config.yaml).
func projectRootOf(projectConfigPath string) string {
	abs, err := filepath.Abs(projectConfigPath)
	if err != nil {
		abs = projectConfigPath
	}
	return filepath.Dir(filepath.Dir(abs))
}

func decodeKey(v any, dst any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, dst)
}

// applyProjectLayer copies the allowed keys of the project layer at path
// onto cfg and returns one warning per ignored key.
func applyProjectLayer(cfg *Config, path string) ([]string, error) {
	keys, err := readProjectLayer(path)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	root := projectRootOf(path)

	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)

	var warnings []string
	for _, k := range names {
		if k == projectAdmissionKey {
			continue
		}
		if !projectKeys[k] {
			warnings = append(warnings, fmt.Sprintf("config: ignoring project key %q (user/system/-c only)", k))
			continue
		}
		var derr error
		switch k {
		case "install_paths":
			var paths []InstallPath
			if derr = decodeKey(keys[k], &paths); derr == nil {
				kept, w := sanitizeProjectPaths(paths, root)
				warnings = append(warnings, w...)
				if len(kept) > 0 || len(paths) == 0 {
					cfg.InstallPaths = kept
				}
			}
		case "repositories":
			var repos []RepositoryConfig
			if derr = decodeKey(keys[k], &repos); derr == nil {
				cfg.Repositories = repos
			}
		case "curation":
			derr = decodeKey(keys[k], &cfg.Curation)
		case "upgrade":
			derr = decodeKey(keys[k], &cfg.Upgrade)
		}
		if derr != nil {
			return nil, fmt.Errorf("load config %s: %s: %w", path, k, derr)
		}
	}
	return warnings, nil
}

// sanitizeProjectPaths keeps the project install paths that stay inside
// root: relative, no escaping "..", no "~", and (after resolving symlinks
// along the existing prefix) strictly beneath root. Kept paths are made absolute
// against root. Each dropped entry yields a warning.
func sanitizeProjectPaths(in []InstallPath, root string) ([]InstallPath, []string) {
	var out []InstallPath
	var warnings []string
	for _, p := range in {
		abs, ok := projectPathInTree(p.Path, root)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("config: install path %s escapes project %s", p.Path, root))
			continue
		}
		p.Path = abs
		out = append(out, p)
	}
	return out, warnings
}

func projectPathInTree(p, root string) (string, bool) {
	if p == "" || strings.HasPrefix(p, "~") || filepath.IsAbs(p) ||
		strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || filepath.VolumeName(p) != "" {
		return "", false
	}
	abs := filepath.Join(root, p)
	if abs == root || !Within(root, abs) || !ContainedIn(root, abs) {
		return "", false
	}
	return abs, true
}

// adminRoot is the root relative operator-layer project install paths
// resolve against: the project root when one is found, else cwd.
func adminRoot(projectConfigPath, cwd string) string {
	if projectConfigPath != "" {
		return projectRootOf(projectConfigPath)
	}
	if root, ok := project.DetectRoot(cwd); ok {
		return root
	}
	return cwd
}

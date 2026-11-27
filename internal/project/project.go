package project

import (
	"os"
	"path/filepath"
)

const (
	ProjectDirName    = ".gym"
	ProjectConfigFile = "config.yaml"
	// ProjectLegacyConfigFile is the JSON name used before YAML became
	// canonical; it is still read, never written.
	ProjectLegacyConfigFile = "config.json"
	ProjectStateFile        = "installed.json"
)

// DetectRoot walks upward from startDir and finds the nearest project root.
// A project root is detected when .gym/config.yaml, .gym/config.json or
// .gym/installed.json exists.
func DetectRoot(startDir string) (string, bool) {
	cur, err := filepath.Abs(startDir)
	if err != nil {
		return "", false
	}

	for {
		projDir := filepath.Join(cur, ProjectDirName)
		if fileExists(filepath.Join(projDir, ProjectConfigFile)) ||
			fileExists(filepath.Join(projDir, ProjectLegacyConfigFile)) ||
			fileExists(filepath.Join(projDir, ProjectStateFile)) {
			return cur, true
		}
		next := filepath.Dir(cur)
		if next == cur {
			return "", false
		}
		cur = next
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// EnsureProjectDir makes sure .gym exists at project root.
func EnsureProjectDir(projectRoot string) (string, error) {
	p := filepath.Join(projectRoot, ProjectDirName)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}

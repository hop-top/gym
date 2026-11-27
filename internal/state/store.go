package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"hop.top/gym/internal/fsutil"
	"hop.top/gym/internal/project"
)

// InstalledSkill tracks an installed skill and origin metadata.
type InstalledSkill struct {
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	SourceURL   string    `json:"source_url"`
	Repository  string    `json:"repository,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
	InstallPath string    `json:"install_path"`
	// Digest and DataClass record what admission saw at install time;
	// DataClass lets an upgrade flag escalation.
	Digest    string `json:"digest,omitempty"`
	DataClass string `json:"data_class,omitempty"`
}

type globalStore struct {
	Skills []InstalledSkill `json:"skills"`
}

type projectStore struct {
	Skills []InstalledSkill `json:"skills"`
}

func dedupe(skills []InstalledSkill) []InstalledSkill {
	m := make(map[string]InstalledSkill, len(skills))
	for _, s := range skills {
		key := s.Name + "::" + s.InstallPath
		m[key] = s
	}
	out := make([]InstalledSkill, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].InstallPath < out[j].InstallPath
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func loadSkills(path string) ([]InstalledSkill, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s struct {
		Skills []InstalledSkill `json:"skills"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return dedupe(s.Skills), nil
}

func saveSkills(path string, skills []InstalledSkill) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(struct {
		Skills []InstalledSkill `json:"skills"`
	}{Skills: dedupe(skills)}, "", "  ")
	if err != nil {
		return err
	}
	blob = append(blob, '\n')
	return fsutil.WriteFileAtomic(path, blob, 0o644)
}

func globalPath(stateHome string) string {
	return filepath.Join(stateHome, "installed-global.json")
}

func projectPath(projectRoot string) string {
	return filepath.Join(projectRoot, project.ProjectDirName, project.ProjectStateFile)
}

func LoadGlobal(stateHome string) ([]InstalledSkill, error) {
	return loadSkills(globalPath(stateHome))
}

func SaveGlobal(stateHome string, skills []InstalledSkill) error {
	return saveSkills(globalPath(stateHome), skills)
}

func LoadProject(projectRoot string) ([]InstalledSkill, error) {
	return loadSkills(projectPath(projectRoot))
}

func SaveProject(projectRoot string, skills []InstalledSkill) error {
	if _, err := project.EnsureProjectDir(projectRoot); err != nil {
		return err
	}
	return saveSkills(projectPath(projectRoot), skills)
}

func Upsert(skills []InstalledSkill, item InstalledSkill) []InstalledSkill {
	found := false
	for i := range skills {
		if skills[i].Name == item.Name && skills[i].InstallPath == item.InstallPath {
			skills[i] = item
			found = true
			break
		}
	}
	if !found {
		skills = append(skills, item)
	}
	return dedupe(skills)
}

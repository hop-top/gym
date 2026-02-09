package ide

import (
	"os"
	"path/filepath"
)

// PathOption represents an install location option.
type PathOption struct {
	Name  string
	Path  string
	Scope string
}

type knownPath struct {
	name string
	rel  string
}

var standardGlobalPaths = []knownPath{
	{name: "claude-global", rel: ".claude/skills"},
	{name: "cursor-global", rel: ".cursor/skills"},
	{name: "gemini-global", rel: ".gemini/skills"},
	{name: "codex-global", rel: ".codex/skills"},
	{name: "windsurf-global", rel: ".windsurf/skills"},
}

var standardProjectPaths = []knownPath{
	{name: "claude-project", rel: ".claude/skills"},
	{name: "cursor-project", rel: ".cursor/skills"},
	{name: "gemini-project", rel: ".gemini/skills"},
	{name: "codex-project", rel: ".codex/skills"},
	{name: "windsurf-project", rel: ".windsurf/skills"},
}

// DetectExistingGlobalPaths returns known IDE/TDE global skill locations that already exist.
func DetectExistingGlobalPaths(home string) []PathOption {
	out := make([]PathOption, 0, len(standardGlobalPaths))
	for _, p := range standardGlobalPaths {
		abs := filepath.Join(home, p.rel)
		if isDir(abs) {
			out = append(out, PathOption{Name: p.name, Path: abs, Scope: "global"})
		}
	}
	return out
}

// StandardGlobalPathCandidates returns all known global path candidates whether or not they exist.
func StandardGlobalPathCandidates(home string) []PathOption {
	out := make([]PathOption, 0, len(standardGlobalPaths))
	for _, p := range standardGlobalPaths {
		out = append(out, PathOption{Name: p.name, Path: filepath.Join(home, p.rel), Scope: "global"})
	}
	return out
}

// StandardProjectPathCandidates returns all known project path candidates.
func StandardProjectPathCandidates(projectRoot string) []PathOption {
	out := make([]PathOption, 0, len(standardProjectPaths))
	for _, p := range standardProjectPaths {
		out = append(out, PathOption{Name: p.name, Path: filepath.Join(projectRoot, p.rel), Scope: "project"})
	}
	return out
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

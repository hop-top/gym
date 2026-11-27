package upgrade

import (
	"path/filepath"
	"sort"

	"hop.top/gym/internal/state"
)

// Input defines all policy-relevant information for upgrade planning.
type Input struct {
	InProjectWithSkills bool
	MirrorGlobal        bool
	NoGlobal            bool
	Patterns            []string
	// ProjectRoot is the scope root of ProjectSkills.
	ProjectRoot   string
	ProjectSkills []state.InstalledSkill
	GlobalSkills  []state.InstalledSkill
}

// PlanItem is a single skill upgrade operation.
type PlanItem struct {
	Skill state.InstalledSkill
	Scope string // project|global
	// Root is the scope root a project item belongs to; "" for global.
	Root string
}

func matchesAny(name string, patterns []string) bool {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	for _, p := range patterns {
		ok, err := filepath.Match(p, name)
		if err == nil && ok {
			return true
		}
		if p == name {
			return true
		}
	}
	return false
}

// Plan applies scope and mirror policy to generate an upgrade plan.
func Plan(in Input) []PlanItem {
	items := make([]PlanItem, 0)
	projectChosen := map[string]bool{}

	if in.InProjectWithSkills {
		for _, s := range in.ProjectSkills {
			if matchesAny(s.Name, in.Patterns) {
				items = append(items, PlanItem{Skill: s, Scope: "project", Root: in.ProjectRoot})
				projectChosen[s.Name] = true
			}
		}
		if in.MirrorGlobal && !in.NoGlobal {
			for _, s := range in.GlobalSkills {
				if projectChosen[s.Name] {
					items = append(items, PlanItem{Skill: s, Scope: "global"})
				}
			}
		}
		return items
	}

	for _, s := range in.GlobalSkills {
		if matchesAny(s.Name, in.Patterns) {
			items = append(items, PlanItem{Skill: s, Scope: "global"})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Skill.Name < items[j].Skill.Name
	})
	return items
}

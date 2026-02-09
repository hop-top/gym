package upgrade_test

import (
	"testing"

	"github.com/hop-top/gym/internal/state"
	"github.com/hop-top/gym/internal/upgrade"
)

func names(plans []upgrade.PlanItem) []string {
	out := make([]string, 0, len(plans))
	for _, p := range plans {
		out = append(out, p.Skill.Name+":"+p.Scope)
	}
	return out
}

func TestPlan_ProjectContextMirrorsGlobalByDefault(t *testing.T) {
	projectSkills := []state.InstalledSkill{{Name: "alpha", Version: "1.0.0"}, {Name: "beta", Version: "1.0.0"}}
	globalSkills := []state.InstalledSkill{{Name: "alpha", Version: "0.9.0"}, {Name: "gamma", Version: "1.0.0"}}

	plan := upgrade.Plan(upgrade.Input{
		InProjectWithSkills: true,
		MirrorGlobal:        true,
		NoGlobal:            false,
		Patterns:            nil,
		ProjectSkills:       projectSkills,
		GlobalSkills:        globalSkills,
	})

	got := names(plan)
	want := []string{"alpha:project", "beta:project", "alpha:global"}
	if len(got) != len(want) {
		t.Fatalf("len(plan) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("plan[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestPlan_NoGlobalDisablesMirror(t *testing.T) {
	projectSkills := []state.InstalledSkill{{Name: "alpha", Version: "1.0.0"}}
	globalSkills := []state.InstalledSkill{{Name: "alpha", Version: "0.9.0"}}

	plan := upgrade.Plan(upgrade.Input{
		InProjectWithSkills: true,
		MirrorGlobal:        true,
		NoGlobal:            true,
		ProjectSkills:       projectSkills,
		GlobalSkills:        globalSkills,
	})
	if len(plan) != 1 || plan[0].Scope != "project" {
		t.Fatalf("plan = %#v, want only project entry", plan)
	}
}

func TestPlan_OutsideProjectUsesGlobalOnly(t *testing.T) {
	projectSkills := []state.InstalledSkill{{Name: "alpha", Version: "1.0.0"}}
	globalSkills := []state.InstalledSkill{{Name: "alpha", Version: "0.9.0"}, {Name: "beta", Version: "1.0.0"}}

	plan := upgrade.Plan(upgrade.Input{
		InProjectWithSkills: false,
		MirrorGlobal:        true,
		NoGlobal:            false,
		Patterns:            []string{"a*"},
		ProjectSkills:       projectSkills,
		GlobalSkills:        globalSkills,
	})
	if len(plan) != 1 || plan[0].Skill.Name != "alpha" || plan[0].Scope != "global" {
		t.Fatalf("plan = %#v, want alpha global only", plan)
	}
}

package cli

import (
	"reflect"
	"testing"

	"hop.top/gym/internal/state"
)

func sk(names ...string) []state.InstalledSkill {
	var out []state.InstalledSkill
	for _, n := range names {
		out = append(out, state.InstalledSkill{Name: n, Version: "1"})
	}
	return out
}

// A4: a chain of three roots plus global, a name at two layers: the nearest
// holder wins and the other is kept, marked shadowed_by the winner.
func TestResolveEffective_NearestWinsOthersShadowed(t *testing.T) {
	layers := []skillLayer{
		{Scope: "project", Folder: "/h/org/unit/proj", Depth: 0, Skills: sk("lint", "a")},
		{Scope: "project", Folder: "/h/org/unit", Depth: 1, Skills: sk("b")},
		{Scope: "project", Folder: "/h/org", Depth: 2, Skills: sk("lint", "c")},
		{Scope: "global", Depth: 3, Skills: sk("lint", "c")},
	}
	got := map[string]string{}
	for _, e := range resolveEffective(layers) {
		got[e.Name+"@"+e.Folder+"/"+e.Scope] = e.ShadowedBy
	}
	want := map[string]string{
		"lint@/h/org/unit/proj/project": "",
		"a@/h/org/unit/proj/project":    "",
		"b@/h/org/unit/project":         "",
		"lint@/h/org/project":           "/h/org/unit/proj",
		"c@/h/org/project":              "",
		"lint@/global":                  "/h/org/unit/proj",
		"c@/global":                     "/h/org",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

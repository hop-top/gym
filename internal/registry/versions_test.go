package registry_test

import (
	"context"
	"reflect"
	"testing"

	"hop.top/gym/internal/registry"
)

const multiDoc = `{
	"repository": {"name": "pack", "stars": 1},
	"skills": [
		{"name": "lint", "version": "1.2.0", "source_url": "https://h/lint-120.git", "digest": "sha256:aaaa"},
		{"name": "lint", "version": "1.3.0", "source_url": "https://h/lint-130.git", "digest": "sha256:bbbb"},
		{"name": "lint", "version": "v1.2.0", "source_url": "https://h/dup.git"},
		{"name": "fmt", "version": "0.1.0", "source_url": "https://h/fmt.git"}
	]
}`

func refreshMulti(t *testing.T, old registry.Index) (registry.Index, registry.Report) {
	t.Helper()
	cfg := registry.Config{Repositories: []registry.RepositorySource{{Name: "pack", URL: "u"}}}
	idx, rep, err := registry.Refresh(context.Background(), cfg,
		fakeFetcher{byURL: map[string][]byte{"u": []byte(multiDoc)}}, old)
	if err != nil {
		t.Fatal(err)
	}
	return idx, rep
}

func TestRefreshKeepsEveryNameVersion(t *testing.T) {
	idx, _ := refreshMulti(t, registry.Index{})
	// lint 1.2.0, lint 1.3.0 (v1.2.0 is the same version as 1.2.0), fmt.
	if len(idx.Skills) != 3 {
		t.Fatalf("want 3 entries, got %d: %#v", len(idx.Skills), idx.Skills)
	}
	if got := idx.Versions("lint"); !reflect.DeepEqual(got, []string{"1.3.0", "1.2.0"}) {
		t.Fatalf("Versions = %v", got)
	}
}

func TestFindReturnsBestAndFindVersionExact(t *testing.T) {
	idx, _ := refreshMulti(t, registry.Index{})
	best, ok := idx.Find("lint")
	if !ok || best.Version != "1.3.0" || best.Digest != "sha256:bbbb" {
		t.Fatalf("Find = %#v", best)
	}
	old, ok := idx.FindVersion("lint", "1.2.0")
	if !ok || old.SourceURL != "https://h/lint-120.git" || old.Digest != "sha256:aaaa" {
		t.Fatalf("FindVersion 1.2.0 = %#v", old)
	}
	if v, ok := idx.FindVersion("lint", "v1.3.0"); !ok || v.Version != "1.3.0" {
		t.Fatalf("v-prefixed pin: %#v %v", v, ok)
	}
	if _, ok := idx.FindVersion("lint", "9.9.9"); ok {
		t.Fatal("unknown version must miss")
	}
	if _, ok := idx.FindVersion("lint", "1.2"); ok {
		t.Fatal("1.2 is not 1.2.0")
	}
	if _, ok := idx.FindVersion("nope", "1.0.0"); ok {
		t.Fatal("unknown name must miss")
	}
}

func TestFindBestWithUnsortedIndex(t *testing.T) {
	// An index file written by hand or by older code: order not guaranteed.
	idx := registry.Index{Skills: []registry.SkillEntry{
		{Name: "a", Version: "1.0.0", SourceURL: "s"},
		{Name: "a", Version: "2.0.0", SourceURL: "s"},
		{Name: "a", Version: "1.5.0", SourceURL: "s"},
	}}
	if e, _ := idx.Find("a"); e.Version != "2.0.0" {
		t.Fatalf("Find = %s", e.Version)
	}
}

func TestUpgradeReportComparesBestToBest(t *testing.T) {
	old := registry.Index{Skills: []registry.SkillEntry{
		{Name: "lint", Version: "1.0.0", SourceURL: "s"},
		{Name: "lint", Version: "1.3.0", SourceURL: "s"},
	}}
	_, rep := refreshMulti(t, old)
	if len(rep.Upgrades) != 0 {
		t.Fatalf("best 1.3.0 -> 1.3.0 is not an upgrade: %#v", rep.Upgrades)
	}
	_, rep = refreshMulti(t, registry.Index{Skills: []registry.SkillEntry{{Name: "lint", Version: "1.0.0", SourceURL: "s"}}})
	if len(rep.Upgrades) != 1 || rep.Upgrades[0].From != "1.0.0" || rep.Upgrades[0].To != "1.3.0" {
		t.Fatalf("Upgrades = %#v", rep.Upgrades)
	}
	if len(rep.NewSkills) != 1 || rep.NewSkills[0].Name != "fmt" {
		t.Fatalf("NewSkills = %#v", rep.NewSkills)
	}
}

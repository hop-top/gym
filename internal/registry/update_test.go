package registry_test

import (
	"context"
	"testing"

	"github.com/hop-top/gym/internal/registry"
)

type fakeFetcher struct {
	byURL map[string][]byte
}

func (f fakeFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	return f.byURL[url], nil
}

func TestRefreshProducesDiffForNewAndUpgradableSkills(t *testing.T) {
	oldIndex := registry.Index{
		Skills: []registry.SkillEntry{
			{Name: "alpha", Version: "1.0.0", SourceURL: "https://example.com/alpha.git"},
		},
	}

	repoDoc := `{
		"repository": {"name": "community-pack", "stars": 120},
		"skills": [
			{"name": "alpha", "version": "1.1.0", "source_url": "https://example.com/alpha.git"},
			{"name": "beta", "version": "0.1.0", "source_url": "https://example.com/beta.git"}
		]
	}`

	cfg := registry.Config{
		Repositories: []registry.RepositorySource{{
			Name:    "community-pack",
			URL:     "https://example.com/repo/skills.json",
			Curated: true,
		}},
		MinStarsForCurated: 100,
	}

	newIndex, report, err := registry.Refresh(context.Background(), cfg, fakeFetcher{byURL: map[string][]byte{
		"https://example.com/repo/skills.json": []byte(repoDoc),
	}}, oldIndex)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if len(newIndex.Skills) != 2 {
		t.Fatalf("newIndex.Skills len = %d, want 2", len(newIndex.Skills))
	}
	if len(report.NewSkills) != 1 || report.NewSkills[0].Name != "beta" {
		t.Fatalf("report.NewSkills = %#v, want beta", report.NewSkills)
	}
	if len(report.Upgrades) != 1 || report.Upgrades[0].Name != "alpha" {
		t.Fatalf("report.Upgrades = %#v, want alpha", report.Upgrades)
	}
	if !newIndex.Repositories[0].Curated {
		t.Fatalf("repository should be curated when stars >= threshold")
	}
}

func TestRefreshMarksNonCuratedWhenStarsBelowThreshold(t *testing.T) {
	cfg := registry.Config{
		Repositories: []registry.RepositorySource{{
			Name:    "small-pack",
			URL:     "https://example.com/small/skills.json",
			Curated: true,
		}},
		MinStarsForCurated: 100,
	}

	doc := `{
		"repository": {"name": "small-pack", "stars": 12},
		"skills": [{"name": "tiny", "version": "0.0.1", "source_url": "https://example.com/tiny.git"}]
	}`

	newIndex, _, err := registry.Refresh(context.Background(), cfg, fakeFetcher{byURL: map[string][]byte{
		"https://example.com/small/skills.json": []byte(doc),
	}}, registry.Index{})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if newIndex.Repositories[0].Curated {
		t.Fatalf("repository should be non-curated when below threshold")
	}
}

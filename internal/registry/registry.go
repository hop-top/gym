package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RepositorySource is a configured registry source entry.
type RepositorySource struct {
	Name    string
	URL     string
	Curated bool
}

// Config controls refresh behavior.
type Config struct {
	Repositories       []RepositorySource
	MinStarsForCurated int
}

// Fetcher loads raw bytes for a source URL.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// HTTPFetcher fetches registry docs over HTTP(S).
type HTTPFetcher struct {
	Client *http.Client
}

func (h HTTPFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("registry fetch %s failed: status %d", url, resp.StatusCode)
	}
	return ioReadAll(resp.Body)
}

// RepositoryMeta mirrors repository-level metadata from skills.json.
type RepositoryMeta struct {
	Name         string `json:"name"`
	Stars        int    `json:"stars"`
	Curated      bool   `json:"curated"`
	CuratedEarly bool   `json:"curated_early"`
}

// SkillEntry is one installable skill in an index.
type SkillEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	SourceURL   string `json:"source_url"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
	Repository  string `json:"repository,omitempty"`
	OriginURL   string `json:"origin_url,omitempty"`
	Curated     bool   `json:"curated"`
}

// skillsDoc is the canonical on-repository skills.json shape.
type skillsDoc struct {
	Repository RepositoryMeta `json:"repository"`
	Skills     []SkillEntry   `json:"skills"`
}

type IndexedRepository struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Stars   int    `json:"stars"`
	Curated bool   `json:"curated"`
}

// Index is the locally cached merged catalog.
type Index struct {
	UpdatedAt    time.Time           `json:"updated_at"`
	Repositories []IndexedRepository `json:"repositories"`
	Skills       []SkillEntry        `json:"skills"`
}

type UpgradeDiff struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Report describes the effect of an update operation.
type Report struct {
	NewSkills []SkillEntry  `json:"new_skills"`
	Upgrades  []UpgradeDiff `json:"upgrades"`
}

// Refresh fetches all configured sources and computes index/report diff.
func Refresh(ctx context.Context, cfg Config, fetcher Fetcher, old Index) (Index, Report, error) {
	if cfg.MinStarsForCurated <= 0 {
		cfg.MinStarsForCurated = 100
	}
	result := Index{UpdatedAt: time.Now().UTC()}
	report := Report{}
	bestByName := map[string]SkillEntry{}
	oldByName := map[string]SkillEntry{}
	for _, s := range old.Skills {
		oldByName[s.Name] = s
	}

	for _, src := range cfg.Repositories {
		raw, err := fetcher.Fetch(ctx, src.URL)
		if err != nil {
			return Index{}, Report{}, err
		}
		var doc skillsDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			return Index{}, Report{}, fmt.Errorf("parse %s: %w", src.URL, err)
		}

		repoName := doc.Repository.Name
		if repoName == "" {
			repoName = src.Name
		}
		curated := src.Curated
		if doc.Repository.Curated || doc.Repository.CuratedEarly {
			curated = true
		} else if curated && doc.Repository.Stars < cfg.MinStarsForCurated {
			curated = false
		}

		result.Repositories = append(result.Repositories, IndexedRepository{
			Name:    repoName,
			URL:     src.URL,
			Stars:   doc.Repository.Stars,
			Curated: curated,
		})

		for _, s := range doc.Skills {
			if s.Name == "" || s.SourceURL == "" {
				continue
			}
			s.Repository = repoName
			s.OriginURL = src.URL
			s.Curated = curated

			current, exists := bestByName[s.Name]
			if !exists || preferSkill(s, current) {
				bestByName[s.Name] = s
			}
		}
	}

	for _, s := range bestByName {
		result.Skills = append(result.Skills, s)
	}
	sort.Slice(result.Repositories, func(i, j int) bool { return result.Repositories[i].Name < result.Repositories[j].Name })
	sort.Slice(result.Skills, func(i, j int) bool { return result.Skills[i].Name < result.Skills[j].Name })

	for _, s := range result.Skills {
		oldSkill, ok := oldByName[s.Name]
		if !ok {
			report.NewSkills = append(report.NewSkills, s)
			continue
		}
		if compareVersions(s.Version, oldSkill.Version) > 0 {
			report.Upgrades = append(report.Upgrades, UpgradeDiff{Name: s.Name, From: oldSkill.Version, To: s.Version})
		}
	}
	sort.Slice(report.NewSkills, func(i, j int) bool { return report.NewSkills[i].Name < report.NewSkills[j].Name })
	sort.Slice(report.Upgrades, func(i, j int) bool { return report.Upgrades[i].Name < report.Upgrades[j].Name })

	return result, report, nil
}

func preferSkill(a, b SkillEntry) bool {
	if a.Curated != b.Curated {
		return a.Curated
	}
	cmp := compareVersions(a.Version, b.Version)
	if cmp != 0 {
		return cmp > 0
	}
	return a.Repository < b.Repository
}

// compareVersions compares semantic-ish dotted versions.
func compareVersions(a, b string) int {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	if a == b {
		return 0
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai := 0
		bi := 0
		if i < len(as) {
			ai = parsePart(as[i])
		}
		if i < len(bs) {
			bi = parsePart(bs[i])
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	if a < b {
		return -1
	}
	return 1
}

func parsePart(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

func ioReadAll(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Find resolves a skill by exact name.
func (idx Index) Find(name string) (SkillEntry, bool) {
	for _, s := range idx.Skills {
		if s.Name == name {
			return s, true
		}
	}
	return SkillEntry{}, false
}

// NewerThan returns true if a candidate skill entry is newer than version.
func NewerThan(candidateVersion, currentVersion string) bool {
	return compareVersions(candidateVersion, currentVersion) > 0
}

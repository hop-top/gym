package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"hop.top/kit/go/console/progress"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/netpolicy"
	"hop.top/gym/internal/redact"
)

// Option mutates Refresh behavior. The zero set leaves Refresh silent
// (no progress events) and behaves as before.
type Option func(*refreshOptions)

type refreshOptions struct {
	reporter progress.Reporter
}

// WithProgress wires a kit/console/progress reporter so each per-source
// fetch emits start + completion events. Pass progress.FromContext(ctx)
// at the call site to hook into the global --progress-format selection.
//
// Nil falls back to progress.Discard().
func WithProgress(r progress.Reporter) Option {
	return func(o *refreshOptions) {
		if r == nil {
			r = progress.Discard()
		}
		o.reporter = r
	}
}

// emitDone is the local helper for "phase ended" events. Mirrors the
// kit/console/progress contract that completion is signalled by an OK
// pointer (true=success, false=failure).
func emitDone(ctx context.Context, r progress.Reporter, phase, item string, ok bool) {
	flag := ok
	r.Emit(ctx, progress.Event{
		Phase: phase,
		Item:  item,
		OK:    &flag,
		At:    time.Now().UTC(),
	})
}

// RepositorySource is a configured registry source entry.
type RepositorySource struct {
	Name    string
	URL     string
	Curated bool
	// Auth is how to authenticate to URL; nil for none.
	Auth *config.AuthConfig
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

// SourceFetcher is a Fetcher that can use a source's credentials.
type SourceFetcher interface {
	FetchSource(ctx context.Context, src RepositorySource) ([]byte, error)
}

// HTTPFetcher loads registry docs: over HTTP(S) through the network policy
// (every request and redirect), or from disk when the location is a plain
// path or a file:// URL.
type HTTPFetcher struct {
	Client *http.Client
}

// Fetch loads url without credentials.
func (h HTTPFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	return h.FetchSource(ctx, RepositorySource{URL: url})
}

// maxIndexBytes bounds a registry index.
const maxIndexBytes = 64 << 20

// FetchSource loads src.URL, authenticating with src.Auth when set.
func (h HTTPFetcher) FetchSource(ctx context.Context, src RepositorySource) ([]byte, error) {
	loc := src.URL
	if p, ok := localIndexPath(loc); ok {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("registry index %s: %w", p, err)
		}
		return b, nil
	}
	if !strings.HasPrefix(loc, "http://") && !strings.HasPrefix(loc, "https://") {
		return nil, fmt.Errorf("registry %s: unsupported location (want https URL, file:// URL or a path)", redact.URL(loc))
	}
	if src.Auth != nil {
		if err := ValidateAuth(src.Auth); err != nil {
			return nil, err
		}
	}
	resp, err := Get(ctx, h.Client, loc, src.Auth, loc)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("registry fetch %s failed: status %d", redact.URL(loc), resp.StatusCode)
	}
	return ioReadAll(io.LimitReader(resp.Body, maxIndexBytes))
}

// localIndexPath reports whether loc names a file on disk: a file:// URL
// or anything that is not a URL.
func localIndexPath(loc string) (string, bool) {
	if p, ok := strings.CutPrefix(loc, "file://"); ok {
		return p, true
	}
	if !strings.Contains(loc, "://") {
		return loc, true
	}
	return "", false
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
	// Digest is the optional publisher-pinned content digest
	// (sha256:<hex>, see internal/install.Digest). Empty means unpinned.
	Digest string `json:"digest,omitempty"`
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
//
// When a kit/console/progress reporter is wired via WithProgress, Refresh
// emits one start event per source plus a completion event with OK
// reflecting fetch success. Total slot is the source count so consumers
// can render "n of N" progress.
func Refresh(ctx context.Context, cfg Config, fetcher Fetcher, old Index, opts ...Option) (Index, Report, error) {
	o := refreshOptions{reporter: progress.Discard()}
	for _, opt := range opts {
		opt(&o)
	}
	if cfg.MinStarsForCurated <= 0 {
		cfg.MinStarsForCurated = 100
	}
	result := Index{UpdatedAt: time.Now().UTC()}
	report := Report{}
	// Every (name, version) is kept so a pinned older version stays
	// reachable; the best entry per name is what Find and upgrades use.
	byVersion := map[string]SkillEntry{}
	oldByName := bestByName(old.Skills)

	var denied []error
	total := int64(len(cfg.Repositories))
	for i, src := range cfg.Repositories {
		current := int64(i + 1)
		o.reporter.Emit(ctx, progress.Event{
			Phase: "fetch",
			Item:  src.URL,
			Bytes: current,
			Total: total,
			At:    time.Now().UTC(),
		})
		var raw []byte
		var err error
		if sf, ok := fetcher.(SourceFetcher); ok {
			raw, err = sf.FetchSource(ctx, src)
		} else {
			raw, err = fetcher.Fetch(ctx, src.URL)
		}
		if err != nil {
			emitDone(ctx, o.reporter, "fetch", src.URL, false)
			var ne *netpolicy.Error
			if errors.As(err, &ne) {
				// Say every repository the policy denies, then fail.
				denied = append(denied, fmt.Errorf("repository %q: %w", src.Name, err))
				continue
			}
			return Index{}, Report{}, err
		}
		emitDone(ctx, o.reporter, "fetch", src.URL, true)
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

			key := versionKey(s)
			current, exists := byVersion[key]
			if !exists || preferSkill(s, current) {
				byVersion[key] = s
			}
		}
	}

	if len(denied) > 0 {
		return Index{}, Report{}, errors.Join(denied...)
	}
	for _, s := range byVersion {
		result.Skills = append(result.Skills, s)
	}
	sort.Slice(result.Repositories, func(i, j int) bool { return result.Repositories[i].Name < result.Repositories[j].Name })
	// By name, then best first: Find returns the first entry of a name.
	sort.Slice(result.Skills, func(i, j int) bool {
		a, b := result.Skills[i], result.Skills[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if preferSkill(a, b) != preferSkill(b, a) {
			return preferSkill(a, b)
		}
		return a.Version < b.Version
	})

	for _, s := range bestByName(result.Skills) {
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

// versionKey identifies an entry by name and version, ignoring a leading v.
func versionKey(s SkillEntry) string {
	return s.Name + "\x00" + strings.TrimPrefix(strings.TrimSpace(s.Version), "v")
}

// bestByName reduces skills (any number of versions per name) to the best
// entry of each name.
func bestByName(skills []SkillEntry) map[string]SkillEntry {
	out := map[string]SkillEntry{}
	for _, s := range skills {
		if cur, ok := out[s.Name]; !ok || preferSkill(s, cur) {
			out[s.Name] = s
		}
	}
	return out
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

// Find resolves a skill by exact name: the best entry among its versions
// (curated first, then the highest version).
func (idx Index) Find(name string) (SkillEntry, bool) {
	best, found := SkillEntry{}, false
	for _, s := range idx.Skills {
		if s.Name == name && (!found || preferSkill(s, best)) {
			best, found = s, true
		}
	}
	return best, found
}

// FindVersion resolves name at exactly version (a leading v is ignored on
// both sides). Among equal matches the best entry wins.
func (idx Index) FindVersion(name, version string) (SkillEntry, bool) {
	best, found := SkillEntry{}, false
	for _, s := range idx.Skills {
		if s.Name == name && compareVersions(s.Version, version) == 0 && (!found || preferSkill(s, best)) {
			best, found = s, true
		}
	}
	return best, found
}

// Versions lists the versions of name in the index, best first.
func (idx Index) Versions(name string) []string {
	var entries []SkillEntry
	for _, s := range idx.Skills {
		if s.Name == name {
			entries = append(entries, s)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return preferSkill(entries[i], entries[j]) })
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Version)
	}
	return out
}

// NewerThan returns true if a candidate skill entry is newer than version.
func NewerThan(candidateVersion, currentVersion string) bool {
	return compareVersions(candidateVersion, currentVersion) > 0
}

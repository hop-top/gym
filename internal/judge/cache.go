package judge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	kitxdg "hop.top/kit/go/core/xdg"

	"hop.top/gym/internal/events"
)

// DefaultMaxAge is how long a cached judgement stays fresh. Older
// entries are treated as a cache miss and re-resolved against rsx.
//
// 24 h matches the typical signal cadence of rsx (stars/dependents/CI
// move on a daily-ish basis); shorter would defeat the cache, longer
// risks acting on stale risk signals.
const DefaultMaxAge = 24 * time.Hour

// Judgement is the cached payload mirror of
// [events.CoachJudgementCompletedPayload]. Cached entries carry the
// full round-trippable shape so reads serve directly into the bus
// reply (and into `gym coach list`'s score column).
type Judgement struct {
	Repo     string                 `json:"repo"`
	Score    int                    `json:"score"`
	Verdict  string                 `json:"verdict"`
	Findings []events.PolicyFinding `json:"findings,omitempty"`
	Signals  []events.JudgeSignal   `json:"signals,omitempty"`
	CachedAt time.Time              `json:"cached_at"`
}

// Cache is a file-backed store of judgements keyed on the
// canonicalised coach URL. Concurrent reads are safe; concurrent
// writes are serialised by the OS atomic-rename pattern.
type Cache struct {
	dir    string
	maxAge time.Duration
}

// NewCache returns a Cache rooted at $XDG_CACHE_HOME/gym/judgements.
// The directory is created lazily on first write. Errors resolving
// XDG fall back to a temp-dir cache so the CLI never crashes on a
// read-only home.
func NewCache() (*Cache, error) {
	dir, err := kitxdg.RawCacheDir("gym")
	if err != nil {
		return nil, err
	}
	return &Cache{
		dir:    filepath.Join(dir, "judgements"),
		maxAge: DefaultMaxAge,
	}, nil
}

// NewCacheAt is the test/Option entry point: pass an explicit dir.
func NewCacheAt(dir string) *Cache {
	return &Cache{dir: dir, maxAge: DefaultMaxAge}
}

// WithMaxAge returns a copy with a custom freshness window. Zero
// disables expiry (entries never go stale on age alone — only on
// --refresh-judgement).
func (c *Cache) WithMaxAge(d time.Duration) *Cache {
	cp := *c
	cp.maxAge = d
	return &cp
}

// Path returns the on-disk path for a given coach URL. Exposed for
// diagnostics and tests.
func (c *Cache) Path(repoURL string) string {
	return filepath.Join(c.dir, cacheKey(repoURL)+".json")
}

// Get returns the cached judgement for repoURL, if any. Returns
// (Judgement{}, false, nil) on a cache miss; (Judgement{}, false,
// error) on read errors other than "not exist". Stale entries (older
// than maxAge) are reported as a miss without a delete — the next
// successful resolve will overwrite them.
func (c *Cache) Get(repoURL string) (Judgement, bool, error) {
	if c == nil || c.dir == "" {
		return Judgement{}, false, nil
	}
	p := c.Path(repoURL)
	raw, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Judgement{}, false, nil
		}
		return Judgement{}, false, err
	}
	var j Judgement
	if err := json.Unmarshal(raw, &j); err != nil {
		// Malformed cache file: treat as miss; next write overwrites.
		return Judgement{}, false, nil
	}
	if c.maxAge > 0 && !j.CachedAt.IsZero() {
		if time.Since(j.CachedAt) > c.maxAge {
			return Judgement{}, false, nil
		}
	}
	return j, true, nil
}

// Put writes (or overwrites) the judgement for repoURL using an
// atomic rename to avoid partial-write races between concurrent gym
// processes.
func (c *Cache) Put(repoURL string, j Judgement) error {
	if c == nil || c.dir == "" {
		return errors.New("judge: cache has no directory configured")
	}
	if j.CachedAt.IsZero() {
		j.CachedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(c.dir, 0o750); err != nil {
		return err
	}
	dst := c.Path(repoURL)
	tmp, err := os.CreateTemp(c.dir, ".judge-*.json.tmp")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&j); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// cacheKey returns sha256(canonicalURL) hex. The URL is canonicalised
// (trim whitespace, lowercase scheme+host) so the same coach reached
// via "github.com/foo/bar" and "https://github.com/foo/bar" hashes
// equally. The transformation is deliberately lossy on path case —
// GitHub repo URLs are case-insensitive and we'd rather collide than
// store one entry per spelling.
func cacheKey(repoURL string) string {
	canonical := canonicalRepoURL(repoURL)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// canonicalRepoURL normalises a coach URL for cache keying. Public so
// callers (e.g. coach list enrichment) hit the same key.
func canonicalRepoURL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "https://"):
		return low
	case strings.HasPrefix(low, "http://"):
		return "https://" + strings.TrimPrefix(low, "http://")
	case strings.HasPrefix(low, "git@github.com:"):
		return "https://github.com/" + strings.TrimPrefix(low, "git@github.com:")
	case strings.HasPrefix(low, "ssh://git@github.com/"):
		return "https://github.com/" + strings.TrimPrefix(low, "ssh://git@github.com/")
	case strings.HasPrefix(low, "github.com/"):
		return "https://" + low
	}
	// owner/repo shorthand — assume GitHub.
	if strings.Count(low, "/") == 1 && !strings.Contains(low, " ") {
		return "https://github.com/" + low
	}
	return low
}

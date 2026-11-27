package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/spf13/viper"
	"hop.top/kit/go/console/progress"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/events"
	"hop.top/gym/internal/judge"
	"hop.top/gym/internal/registry"
)

// coachHireOptions bundles the runtime knobs runCoachHire needs.
// Owned by cobra.go's RunE which reads cmd.Flags() and passes a fully
// populated value down. Plumbed as a struct (rather than a long arg
// list) so future flags don't churn every callsite.
type coachHireOptions struct {
	Global      bool
	Name        string
	Curated     bool
	NoUpdate    bool
	Force       bool
	GateTimeout time.Duration
	Insecure    bool
}

type manifestInfo struct {
	Name string
}

func tryFetchManifestInfo(ctx context.Context, manifestURL string) (manifestInfo, error) {
	rep := progress.FromContext(ctx)
	rep.Emit(ctx, progress.Event{
		Phase: "fetch-manifest",
		Item:  manifestURL,
		At:    time.Now().UTC(),
	})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := registry.Get(ctx, client, manifestURL, nil, "")
	if err != nil {
		emitFetchManifestDone(ctx, rep, manifestURL, false)
		return manifestInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		emitFetchManifestDone(ctx, rep, manifestURL, false)
		return manifestInfo{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		emitFetchManifestDone(ctx, rep, manifestURL, false)
		return manifestInfo{}, err
	}
	if _, ok := raw["skills"]; !ok {
		emitFetchManifestDone(ctx, rep, manifestURL, false)
		return manifestInfo{}, errors.New("missing skills key")
	}
	var repo struct {
		Repository struct {
			Name string `json:"name"`
		} `json:"repository"`
	}
	if b, ok := raw["repository"]; ok {
		_ = json.Unmarshal(b, &repo.Repository)
	}
	emitFetchManifestDone(ctx, rep, manifestURL, true)
	return manifestInfo{Name: strings.TrimSpace(repo.Repository.Name)}, nil
}

// emitFetchManifestDone is the local "phase ended" helper for the
// coach manifest pre-validate path. Mirrors the kit/console/progress
// contract that completion is signalled by an OK pointer.
func emitFetchManifestDone(ctx context.Context, r progress.Reporter, item string, ok bool) {
	flag := ok
	r.Emit(ctx, progress.Event{
		Phase: "fetch-manifest",
		Item:  item,
		OK:    &flag,
		At:    time.Now().UTC(),
	})
}

func isRepoShorthand(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	if strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://") {
		return false
	}
	if strings.Count(s, "/") != 1 {
		return false
	}
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/") {
		return false
	}
	parts := strings.Split(s, "/")
	return parts[0] != "" && parts[1] != ""
}

func ghRawCandidates(owner, repo string) []string {
	return []string{
		"https://raw.githubusercontent.com/" + owner + "/" + repo + "/main/skills.json",
		"https://raw.githubusercontent.com/" + owner + "/" + repo + "/master/skills.json",
	}
}

func manifestCandidates(ref string) ([]string, error) {
	if isRepoShorthand(ref) {
		parts := strings.Split(ref, "/")
		return ghRawCandidates(parts[0], parts[1]), nil
	}
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil {
			return nil, err
		}
		switch u.Host {
		case "github.com":
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 2 {
				repo := strings.TrimSuffix(parts[1], ".git")
				return ghRawCandidates(parts[0], repo), nil
			}
		}
		if strings.HasSuffix(u.Path, "/") {
			u.Path = path.Join(u.Path, "skills.json")
			return []string{u.String()}, nil
		}
		if strings.HasSuffix(u.Path, ".json") {
			return []string{ref}, nil
		}
		return nil, fmt.Errorf("expected a manifest URL ending in .json or a repo shorthand owner/repo")
	}
	return nil, fmt.Errorf("expected manifest URL or repo shorthand owner/repo")
}

func deriveSourceName(urlOrRef string) string {
	if isRepoShorthand(urlOrRef) {
		parts := strings.Split(urlOrRef, "/")
		return parts[1]
	}
	if strings.Contains(urlOrRef, "://") {
		u, err := url.Parse(urlOrRef)
		if err == nil {
			b := path.Base(u.Path)
			b = strings.TrimSuffix(b, ".json")
			if b != "" && b != "." && b != "/" {
				return b
			}
		}
	}
	return "source"
}

func (a *App) runCoachHire(ctx context.Context, opts coachHireOptions, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("coach hire requires exactly one manifest url or owner/repo")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	emitPhase(ctx, "resolve", ref)
	cands, err := manifestCandidates(ref)
	if err != nil {
		emitFail(ctx, "resolve", ref)
		return err
	}

	selected := ""
	info := manifestInfo{}
	fetchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var lastErr error
	for _, c := range cands {
		emitPhase(ctx, "fetch", c)
		mi, err := tryFetchManifestInfo(fetchCtx, c)
		if err == nil {
			emitOK(ctx, "fetch", c)
			selected = c
			info = mi
			break
		}
		emitFail(ctx, "fetch", c)
		lastErr = err
	}
	if selected == "" {
		if !opts.Force {
			return fmt.Errorf("could not fetch/validate manifest (last error: %v); pass an explicit skills.json URL or use --force", lastErr)
		}
		selected = cands[0]
	}
	coachName := strings.TrimSpace(opts.Name)
	if coachName == "" {
		if info.Name != "" {
			coachName = info.Name
		} else {
			coachName = deriveSourceName(ref)
		}
	}

	target := "project"
	if opts.Global {
		target = "user"
	}

	// Sync gate: ask the bus for a coach judgement (rsx-backed) and
	// honor the verdict per --strict / --insecure semantics. Refer to
	// internal/judge for the publisher contract.
	if err := a.gateCoachHire(ctx, opts, ref, selected); err != nil {
		return err
	}

	emitPhase(ctx, "save", coachName)
	cfgPath, err := config.AddRepository(a.CWD, target, config.RepositoryConfig{Name: coachName, URL: selected, Curated: opts.Curated})
	if err != nil {
		emitFail(ctx, "save", coachName)
		return err
	}
	emitOK(ctx, "save", coachName)
	a.printlnf("hired coach %q -> %s", coachName, selected)
	a.printlnf("updated config: %s", cfgPath)
	if opts.NoUpdate {
		return nil
	}
	return a.runUpdateCtx(ctx)
}

// gateCoachHire publishes gym.coach.repository.hire_requested and
// blocks awaiting gym.coach.judgement.completed. Behavior:
//
//   - timeout (no reply): fail-open with a WARN log by default;
//     fail-closed when --strict is set.
//   - verdict=allow: silent.
//   - verdict=warn: print findings, proceed.
//   - verdict=deny: print findings, error out — unless --insecure
//     lowers it to WARN.
//
// Bus is nil-safe: if the App has no bus (legacy callers, unit tests
// that bypass PersistentPreRunE) the gate is a no-op.
func (a *App) gateCoachHire(ctx context.Context, opts coachHireOptions, ref, manifestURL string) error {
	if a == nil || a.bus == nil {
		return nil
	}
	scope := "project"
	if opts.Global {
		scope = "global"
	}
	repoForJudge := ref
	// Prefer the canonicalised URL we resolved (the one that landed in
	// the manifest) so the cache key matches across `coach hire ref`
	// vs subsequent `coach list` lookups.
	if manifestURL != "" {
		repoForJudge = manifestURL
	}

	timeout := opts.GateTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	emitPhase(ctx, "judge", ref)
	resp, err := a.bus.RequestResponse(ctx,
		events.TopicCoachHireRequested,
		string(events.TopicCoachJudgementCompleted),
		events.CoachHireRequestedPayload{
			Repo: repoForJudge, Scope: scope, Curated: opts.Curated,
		},
		timeout,
	)
	if err != nil {
		emitFail(ctx, "judge", ref)
		if errors.Is(err, eventbus.ErrTimeout) {
			if eventbus.IsStrict(viper.GetViper()) {
				return fmt.Errorf("coach judgement timed out after %s (strict mode)", timeout)
			}
			a.Logger().Warnf("coach judgement timed out after %s; proceeding (use --strict to fail closed)", timeout)
			return nil
		}
		// Bus-level error (publisher gone, transport error). Fail-open
		// matches the rsx subprocess failure mode: warn and proceed.
		a.Logger().Warnf("coach judgement bus error: %v; proceeding", err)
		return nil
	}
	emitOK(ctx, "judge", ref)

	var verdict events.CoachJudgementCompletedPayload
	if derr := resp.Decode(&verdict); derr != nil {
		a.Logger().Warnf("decode judgement: %v; proceeding", derr)
		return nil
	}

	switch strings.ToLower(verdict.Verdict) {
	case "allow", "":
		if verdict.Score > 0 {
			a.Logger().Infof("coach %s allowed (score %d/100)", ref, verdict.Score)
		}
		return nil
	case "warn":
		a.Logger().Warnf("coach %s flagged (score %d/100)", ref, verdict.Score)
		printFindings(a, verdict.Findings)
		return nil
	case "deny":
		printFindings(a, verdict.Findings)
		if opts.Insecure {
			a.Logger().Warnf("coach %s denied (score %d/100); --insecure lowers to WARN, proceeding", ref, verdict.Score)
			return nil
		}
		return fmt.Errorf("coach %s denied by judgement (score %d/100); pass --insecure to override", ref, verdict.Score)
	default:
		a.Logger().Warnf("unknown verdict %q for %s; proceeding", verdict.Verdict, ref)
		return nil
	}
}

// printFindings is a small helper so the coach hire flow surfaces
// rsx's findings consistently with what the rest of gym shows
// elsewhere. We use the kit logger so --quiet/--no-color carry over.
func printFindings(a *App, findings []events.PolicyFinding) {
	for _, f := range findings {
		if f.Message == "" {
			a.Logger().Warnf("[%s] %s", strings.ToUpper(f.Severity), f.Rule)
			continue
		}
		a.Logger().Warnf("[%s] %s: %s", strings.ToUpper(f.Severity), f.Rule, f.Message)
	}
}

func (a *App) runCoachList() error {
	userPath, userRepos, userOK, err := config.LoadUserRepositories()
	if err != nil {
		return err
	}
	projPath, projRepos, projOK, err := config.LoadProjectRepositories(a.CWD)
	if err != nil {
		return err
	}

	cache, _ := judge.NewCache()

	if userOK {
		a.printlnf("user coaches (%s):", userPath)
		if len(userRepos) == 0 {
			a.printlnf("  - none")
		}
		for _, r := range userRepos {
			a.printlnf("  - %s%s%s: %s", r.Name, curatedFlag(r.Curated), scoreSuffix(cache, r.URL), r.URL)
		}
	} else {
		a.printlnf("user coaches: none (%s not found)", userPath)
	}

	if projOK {
		a.printlnf("project coaches (%s):", projPath)
		if len(projRepos) == 0 {
			a.printlnf("  - none")
		}
		for _, r := range projRepos {
			a.printlnf("  - %s%s%s: %s", r.Name, curatedFlag(r.Curated), scoreSuffix(cache, r.URL), r.URL)
		}
	}

	return nil
}

func curatedFlag(curated bool) string {
	if curated {
		return " curated"
	}
	return ""
}

// scoreSuffix returns " score=N/100 (verdict)" when a cached
// judgement exists for repoURL; empty string otherwise. Cache misses
// are silent — we never trigger a fresh rsx call from `coach list`
// (that's what `coach hire`'s sync gate is for).
func scoreSuffix(cache *judge.Cache, repoURL string) string {
	if cache == nil {
		return ""
	}
	j, ok, err := cache.Get(repoURL)
	if err != nil || !ok {
		return ""
	}
	if j.Verdict == "" {
		return fmt.Sprintf(" score=%d/100", j.Score)
	}
	return fmt.Sprintf(" score=%d/100 (%s)", j.Score, j.Verdict)
}

func (a *App) runCoachDismiss(global bool, noUpdate bool, selector string) error {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return errors.New("coach dismiss requires exactly one selector (name or url)")
	}
	target := "project"
	if global {
		target = "user"
	}

	cfgPath, removed, err := config.RemoveRepository(a.CWD, target, selector)
	if err != nil {
		return err
	}
	if removed == 0 {
		a.printlnf("no coaches matched %q", selector)
		return nil
	}
	a.printlnf("dismissed %d coach(es) matching %q", removed, selector)
	a.printlnf("updated config: %s", cfgPath)
	if noUpdate {
		return nil
	}
	return a.runUpdate()
}

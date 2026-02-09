package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"hop/skills/internal/config"
)

func (a *App) cmdCoach(args []string) error {
	if len(args) == 0 {
		return a.cmdCoachList()
	}

	sub := args[0]
	switch sub {
	case "hire":
		return a.cmdCoachHire(args[1:])
	case "list", "ls":
		return a.cmdCoachList()
	case "dismiss", "rm":
		return a.cmdCoachDismiss(args[1:])
	default:
		return fmt.Errorf("unknown coach subcommand %q", sub)
	}
}

type manifestInfo struct {
	Name string
}

func tryFetchManifestInfo(ctx context.Context, manifestURL string) (manifestInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return manifestInfo{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return manifestInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return manifestInfo{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return manifestInfo{}, err
	}
	if _, ok := raw["skills"]; !ok {
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
	return manifestInfo{Name: strings.TrimSpace(repo.Repository.Name)}, nil
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

func (a *App) cmdCoachHire(args []string) error {
	fs := flag.NewFlagSet("coach hire", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	globalTarget := fs.Bool("global", false, "write to global (user) config")
	globalShort := fs.Bool("g", false, "alias for --global")
	name := fs.String("name", "", "coach name")
	curated := fs.Bool("curated", false, "mark coach as curated")
	noUpdate := fs.Bool("no-update", false, "do not run gym update after hiring")
	force := fs.Bool("force", false, "hire even if manifest cannot be fetched/validated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) != 1 {
		return errors.New("coach hire requires exactly one manifest url or owner/repo")
	}
	ref := strings.TrimSpace(pos[0])
	cands, err := manifestCandidates(ref)
	if err != nil {
		return err
	}

	selected := ""
	info := manifestInfo{}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var lastErr error
	for _, c := range cands {
		mi, err := tryFetchManifestInfo(ctx, c)
		if err == nil {
			selected = c
			info = mi
			break
		}
		lastErr = err
	}
	if selected == "" {
		if !*force {
			return fmt.Errorf("could not fetch/validate manifest (last error: %v); pass an explicit skills.json URL or use --force", lastErr)
		}
		selected = cands[0]
	}

	isGlobal := *globalTarget || *globalShort

	coachName := strings.TrimSpace(*name)
	if coachName == "" {
		if info.Name != "" {
			coachName = info.Name
		} else {
			coachName = deriveSourceName(ref)
		}
	}

	target := "project"
	if isGlobal {
		target = "user"
	}

	cfgPath, err := config.AddRepository(a.CWD, target, config.RepositoryConfig{Name: coachName, URL: selected, Curated: *curated})
	if err != nil {
		return err
	}
	a.printlnf("hired coach %q -> %s", coachName, selected)
	a.printlnf("updated config: %s", cfgPath)
	if *noUpdate {
		return nil
	}
	return a.cmdUpdate(nil)
}

func (a *App) cmdCoachList() error {
	userPath, userRepos, userOK, err := config.LoadUserRepositories()
	if err != nil {
		return err
	}
	projPath, projRepos, projOK, err := config.LoadProjectRepositories(a.CWD)
	if err != nil {
		return err
	}

	if userOK {
		a.printlnf("user coaches (%s):", userPath)
		if len(userRepos) == 0 {
			a.printlnf("  - none")
		}
		for _, r := range userRepos {
			flag := ""
			if r.Curated {
				flag = " curated"
			}
			a.printlnf("  - %s%s: %s", r.Name, flag, r.URL)
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
			flag := ""
			if r.Curated {
				flag = " curated"
			}
			a.printlnf("  - %s%s: %s", r.Name, flag, r.URL)
		}
	}

	return nil
}

func (a *App) cmdCoachDismiss(args []string) error {
	fs := flag.NewFlagSet("coach dismiss", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	globalTarget := fs.Bool("global", false, "dismiss from global (user) config")
	globalShort := fs.Bool("g", false, "alias for --global")
	noUpdate := fs.Bool("no-update", false, "do not run gym update after dismissing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) != 1 {
		return errors.New("coach dismiss requires exactly one selector (name or url)")
	}
	selector := strings.TrimSpace(pos[0])

	target := "project"
	if *globalTarget || *globalShort {
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
	if *noUpdate {
		return nil
	}
	return a.cmdUpdate(nil)
}

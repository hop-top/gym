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

	"hop.top/gym/internal/config"
)

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

func (a *App) runCoachHire(global bool, name string, curated bool, noUpdate bool, force bool, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("coach hire requires exactly one manifest url or owner/repo")
	}
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
		if !force {
			return fmt.Errorf("could not fetch/validate manifest (last error: %v); pass an explicit skills.json URL or use --force", lastErr)
		}
		selected = cands[0]
	}
	coachName := strings.TrimSpace(name)
	if coachName == "" {
		if info.Name != "" {
			coachName = info.Name
		} else {
			coachName = deriveSourceName(ref)
		}
	}

	target := "project"
	if global {
		target = "user"
	}

	cfgPath, err := config.AddRepository(a.CWD, target, config.RepositoryConfig{Name: coachName, URL: selected, Curated: curated})
	if err != nil {
		return err
	}
	a.printlnf("hired coach %q -> %s", coachName, selected)
	a.printlnf("updated config: %s", cfgPath)
	if noUpdate {
		return nil
	}
	return a.runUpdate()
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

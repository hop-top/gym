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

func (a *App) cmdSources(args []string) error {
	if len(args) == 0 {
		return a.cmdSourcesList()
	}

	sub := args[0]
	switch sub {
	case "add":
		return a.cmdSourcesAdd(args[1:])
	case "list", "ls":
		return a.cmdSourcesList()
	case "remove", "rm":
		return a.cmdSourcesRemove(args[1:])
	default:
		return fmt.Errorf("unknown sources subcommand %q", sub)
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

func (a *App) cmdSourcesAdd(args []string) error {
	fs := flag.NewFlagSet("sources add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	projectTarget := fs.Bool("project", false, "write to project config")
	name := fs.String("name", "", "source name")
	curated := fs.Bool("curated", false, "mark source as curated")
	noUpdate := fs.Bool("no-update", false, "do not run gym update after adding")
	force := fs.Bool("force", false, "add even if manifest cannot be fetched/validated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) != 1 {
		return errors.New("sources add requires exactly one manifest url or owner/repo")
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

	srcName := strings.TrimSpace(*name)
	if srcName == "" {
		if info.Name != "" {
			srcName = info.Name
		} else {
			srcName = deriveSourceName(ref)
		}
	}

	target := "user"
	if *projectTarget {
		target = "project"
	}

	cfgPath, err := config.AddRepository(a.CWD, target, config.RepositoryConfig{Name: srcName, URL: selected, Curated: *curated})
	if err != nil {
		return err
	}
	a.printlnf("added source %q -> %s", srcName, selected)
	a.printlnf("updated config: %s", cfgPath)
	if *noUpdate {
		return nil
	}
	return a.cmdUpdate(nil)
}

func (a *App) cmdSourcesList() error {
	userPath, userRepos, userOK, err := config.LoadUserRepositories()
	if err != nil {
		return err
	}
	projPath, projRepos, projOK, err := config.LoadProjectRepositories(a.CWD)
	if err != nil {
		return err
	}

	if userOK {
		a.printlnf("user sources (%s):", userPath)
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
		a.printlnf("user sources: none (%s not found)", userPath)
	}

	if projOK {
		a.printlnf("project sources (%s):", projPath)
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

func (a *App) cmdSourcesRemove(args []string) error {
	fs := flag.NewFlagSet("sources remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	projectTarget := fs.Bool("project", false, "remove from project config")
	noUpdate := fs.Bool("no-update", false, "do not run gym update after removing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) != 1 {
		return errors.New("sources remove requires exactly one selector (name or url)")
	}
	selector := strings.TrimSpace(pos[0])

	target := "user"
	if *projectTarget {
		target = "project"
	}

	cfgPath, removed, err := config.RemoveRepository(a.CWD, target, selector)
	if err != nil {
		return err
	}
	if removed == 0 {
		a.printlnf("no sources matched %q", selector)
		return nil
	}
	a.printlnf("removed %d source(s) matching %q", removed, selector)
	a.printlnf("updated config: %s", cfgPath)
	if *noUpdate {
		return nil
	}
	return a.cmdUpdate(nil)
}

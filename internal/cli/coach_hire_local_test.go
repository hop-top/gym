package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/config"
)

const hireManifest = `{"repository":{"name":"acme-skills","curated":true},"skills":[]}`

// hireUserRepo returns the user-config repository entry named name.
func hireUserRepo(t *testing.T, name string) (config.RepositoryConfig, bool) {
	t.Helper()
	_, repos, _, err := config.LoadUserRepositories()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if r.Name == name {
			return r, true
		}
	}
	return config.RepositoryConfig{}, false
}

// hireErrLine is the "Error:" line of a failed run.
func hireErrLine(errs string) string {
	for _, l := range strings.Split(errs, "\n") {
		if strings.HasPrefix(l, "Error:") {
			return l
		}
	}
	return ""
}

// coach hire takes a local index file in every form update reads, offline,
// and stores a location that resolves from any working directory.
func TestCoachHireLocalForms(t *testing.T) {
	cases := []struct {
		name string
		arg  func(e lkEnv, idx string) string
		want func(e lkEnv, idx string) string
	}{
		{"absolute", func(_ lkEnv, idx string) string { return idx }, nil},
		{"dot-relative", func(e lkEnv, idx string) string {
			rel, _ := filepath.Rel(e.proj, idx)
			return "./" + filepath.ToSlash(rel)
		}, nil},
		{"parent-relative", func(e lkEnv, idx string) string {
			rel, _ := filepath.Rel(e.proj, idx)
			return "../proj/" + filepath.ToSlash(rel)
		}, nil},
		{"bare-file", func(e lkEnv, _ string) string { return "reg/skills.json" }, nil},
		{"tilde", func(e lkEnv, idx string) string {
			rel, _ := filepath.Rel(e.home, idx)
			return "~/" + filepath.ToSlash(rel)
		}, func(e lkEnv, _ string) string { return filepath.Join(e.home, "reg", "skills.json") }},
		{"file-url", func(_ lkEnv, idx string) string { return "file://" + idx },
			func(_ lkEnv, idx string) string { return "file://" + idx }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newLkEnv(t, "")
			dir := filepath.Join(e.proj, "reg")
			if tc.name == "tilde" {
				dir = filepath.Join(e.home, "reg")
			}
			writeFiles(t, dir, map[string]string{"skills.json": hireManifest})
			idx := filepath.Join(dir, "skills.json")
			want := idx
			if tc.want != nil {
				want = tc.want(e, idx)
			}
			// runGym passes --offline: a local index is not network access.
			code, out, errs := e.gym(t, "coach", "hire", "--global", tc.arg(e, idx))
			if code != 0 {
				t.Fatalf("exit %d\n%s\n%s", code, out, errs)
			}
			r, ok := hireUserRepo(t, "acme-skills")
			if !ok {
				t.Fatalf("no acme-skills entry\n%s\n%s", out, errs)
			}
			if r.URL != want {
				t.Fatalf("stored url = %q, want %q", r.URL, want)
			}
			// update (run by hire) read the index from disk.
			if !strings.Contains(out, "updated registry") {
				t.Fatalf("update did not run\n%s\n%s", out, errs)
			}
			// and resolves it from another working directory.
			other := scratchDir(t)
			if code, out, errs := runGym(t, other, "update"); code != 0 {
				t.Fatalf("update elsewhere: exit %d\n%s\n%s", code, out, errs)
			}
		})
	}
}

// --name wins; without repository.name the name derives from the file.
func TestCoachHireLocalName(t *testing.T) {
	e := newLkEnv(t, "")
	writeFiles(t, e.proj, map[string]string{"team.json": `{"skills":[]}`, "skills.json": hireManifest})
	e.mustGym(t, "coach", "hire", "--global", "--no-update", "./team.json")
	if r, ok := hireUserRepo(t, "team"); !ok || r.URL != filepath.Join(e.proj, "team.json") {
		t.Fatalf("derived name: %+v %v", r, ok)
	}
	e.mustGym(t, "coach", "hire", "--global", "--no-update", "--name", "acme", "./skills.json")
	if _, ok := hireUserRepo(t, "acme"); !ok {
		t.Fatal("--name not used")
	}
}

// A project hire stores an absolute path: update reads the project config
// from any folder of the project and resolves the path as given.
func TestCoachHireLocalProject(t *testing.T) {
	e := newLkEnv(t, "")
	writeFiles(t, e.proj, map[string]string{"reg/skills.json": hireManifest, ".gym/config.yaml": ""})
	e.mustGym(t, "coach", "hire", "--no-update", "reg/skills.json")
	_, repos, ok, err := config.LoadProjectRepositories(e.proj)
	if err != nil || !ok || len(repos) != 1 || repos[0].URL != filepath.Join(e.proj, "reg", "skills.json") {
		t.Fatalf("project repos = %+v ok=%v err=%v", repos, ok, err)
	}
	sub := filepath.Join(e.proj, "deep", "er")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runGym(t, sub, "update"); code != 0 || !strings.Contains(out, "updated registry") {
		t.Fatalf("update from subfolder: exit %d\n%s\n%s", code, out, errs)
	}
}

// A path-looking argument that does not exist is an error naming the path,
// never a GitHub lookup; owner/repo keeps its meaning when no such file
// exists; garbage keeps the usage error.
func TestCoachHireArgumentDisambiguation(t *testing.T) {
	e := newLkEnv(t, "")
	missing := map[string]string{
		"./missing.json":                   filepath.Join(e.proj, "missing.json"),
		"../missing.json":                  filepath.Join(filepath.Dir(e.proj), "missing.json"),
		"/nonexistent-gym-dir/skills.json": "/nonexistent-gym-dir/skills.json",
		"~/missing.json":                   filepath.Join(e.home, "missing.json"),
	}
	for arg, abs := range missing {
		code, _, errs := e.gym(t, "coach", "hire", "--global", "--no-update", arg)
		if code == 0 || !strings.Contains(hireErrLine(errs), abs) || strings.Contains(errs, "raw.githubusercontent.com") {
			t.Fatalf("%s: exit %d\n%s", arg, code, errs)
		}
		if _, ok := hireUserRepo(t, "missing"); ok {
			t.Fatalf("%s: hired a missing file", arg)
		}
	}
	// owner/repo with no such relative file: the GitHub shorthand (denied offline).
	code, _, errs := e.gym(t, "coach", "hire", "--global", "--no-update", "acme/skills")
	if code == 0 || !strings.Contains(errs, `offline mode forbids network access to "raw.githubusercontent.com"`) {
		t.Fatalf("shorthand: exit %d\n%s", code, errs)
	}
	// owner/repo naming an existing file: the file.
	writeFiles(t, e.proj, map[string]string{"acme/skills": hireManifest})
	e.mustGym(t, "coach", "hire", "--global", "--no-update", "acme/skills")
	if r, ok := hireUserRepo(t, "acme-skills"); !ok || r.URL != filepath.Join(e.proj, "acme", "skills") {
		t.Fatalf("existing file: %+v %v", r, ok)
	}
	// neither a path, a URL nor owner/repo.
	code, _, errs = e.gym(t, "coach", "hire", "--global", "--no-update", "garbage")
	if code == 0 || !strings.Contains(errs, "expected manifest URL or repo shorthand owner/repo") {
		t.Fatalf("garbage: exit %d\n%s", code, errs)
	}
}

// An unreadable or invalid local index fails the hire naming the path;
// --force hires it anyway.
func TestCoachHireLocalInvalid(t *testing.T) {
	e := newLkEnv(t, "")
	writeFiles(t, e.proj, map[string]string{"bad.json": "not json", "noskills.json": `{"repository":{"name":"x"}}`})
	for _, f := range []string{"bad.json", "noskills.json"} {
		p := filepath.Join(e.proj, f)
		code, _, errs := e.gym(t, "coach", "hire", "--global", "--no-update", "./"+f)
		if code == 0 || !strings.Contains(hireErrLine(errs), p) {
			t.Fatalf("%s: exit %d\n%s", f, code, errs)
		}
	}
	dir := filepath.Join(e.proj, "adir.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := e.gym(t, "coach", "hire", "--global", "--no-update", dir); code == 0 || !strings.Contains(hireErrLine(errs), dir) {
		t.Fatalf("directory: exit %d\n%s", code, errs)
	}
	e.mustGym(t, "coach", "hire", "--global", "--no-update", "--force", "--name", "bad", "./bad.json")
	if r, ok := hireUserRepo(t, "bad"); !ok || r.URL != filepath.Join(e.proj, "bad.json") {
		t.Fatalf("--force: %+v %v", r, ok)
	}
}

// Local sources pass --offline and the allowlist; remote hosts stay denied
// and are never contacted.
func TestCoachHireNetworkPolicy(t *testing.T) {
	// offline: remote denied.
	e := newLkEnv(t, "")
	code, _, errs := e.gym(t, "coach", "hire", "--global", "--no-update", "https://registry.corp.example/skills.json")
	if code == 0 || !strings.Contains(errs, `network-denied: offline mode forbids network access to "registry.corp.example"`) {
		t.Fatalf("offline remote: exit %d\n%s", code, errs)
	}
	if _, ok := hireUserRepo(t, "skills"); ok {
		t.Fatal("offline remote hired")
	}

	// allowlist: local allowed, unlisted remote denied without contact.
	srv := forbiddenServer(t)
	e2 := newLkEnv(t, "network:\n  mode: allowlist\n  allow_hosts: [registry.corp.example]\n")
	writeFiles(t, e2.proj, map[string]string{"skills.json": hireManifest})
	for _, arg := range []string{"./skills.json", "file://" + filepath.Join(e2.proj, "skills.json")} {
		if code, out, errs := runGymOpen(t, e2.proj, "coach", "hire", "--global", "--no-update", arg); code != 0 {
			t.Fatalf("allowlist local %s: exit %d\n%s\n%s", arg, code, out, errs)
		}
	}
	code, _, errs = runGymOpen(t, e2.proj, "coach", "hire", "--global", "--no-update", "--name", "remote", srv.URL+"/skills.json")
	want := fmt.Sprintf(`network-denied: host %q not in network.allow_hosts (mode allowlist)`, "127.0.0.1")
	if code == 0 || !strings.Contains(errs, want) || srv.hits.Load() != 0 {
		t.Fatalf("allowlist remote: exit %d hits %d\n%s", code, srv.hits.Load(), errs)
	}
	if _, ok := hireUserRepo(t, "remote"); ok {
		t.Fatal("allowlist remote hired")
	}
}

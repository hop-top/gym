package cli_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
)

// D8: --offline. update is denied (by design), a git source is denied
// before git runs, a directory source and the cached index work.
func TestD8OfflineMatrix(t *testing.T) {
	gitLog := fakeGitOnPath(t)
	e := newLkEnv(t, "repositories:\n  - name: corp\n    url: https://registry.corp.example/skills.json\n")
	cached := lkSkillSrc(t, filepath.Join(e.proj, "mirror"), "lint", "a")
	e.index(t, registry.SkillEntry{Name: "lint", Version: "1.0.0", SourceURL: cached})

	// update: denied, names the repository, keeps the cached index.
	code, _, errs := e.gym(t, "update")
	if code != 1 || !strings.Contains(errs, `repository "corp": network-denied: offline mode forbids network access to "registry.corp.example"`) {
		t.Fatalf("update: exit %d\n%s", code, errs)
	}
	// git source: denied, git never ran for the network.
	code, _, errs = e.gym(t, "learn", "https://git.example/o/r.git")
	if code != 1 || !strings.Contains(errs, `network-denied: offline mode forbids network access to "git.example"`) {
		t.Fatalf("git source: exit %d\n%s", code, errs)
	}
	if calls := gitNetworkCalls(t, gitLog); len(calls) != 0 {
		t.Fatalf("git ran offline: %v", calls)
	}
	// directory source: fine.
	dir := lkSkillSrc(t, filepath.Join(e.proj, "dirsrc"), "fmt", "a")
	e.mustGym(t, "learn", dir)
	// cached index: fine.
	e.mustGym(t, "learn", "lint")
	if !installed(filepath.Join(e.proj, ".gym", "skills"), "lint") || !installed(filepath.Join(e.proj, ".gym", "skills"), "fmt") {
		t.Fatal("offline installs missing")
	}
}

// D6: under allowlist update fetches a listed repository, denies an
// unlisted one without contacting it, names it, and exits 1.
func TestD6AllowlistUpdate(t *testing.T) {
	good := newLoopServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"repository":{"name":"corp"},"skills":[{"name":"lint","version":"1.0.0","source_url":"/x/lint"}]}`)
	})
	bad := forbiddenServer(t)
	e := newLkEnv(t, fmt.Sprintf("network:\n  mode: allowlist\n  allow_hosts: [%q]\nrepositories:\n  - name: corp\n    url: %s\n  - name: other\n    url: %s\n",
		good.host(t), good.URL, bad.URL))

	code, _, errs := runGymOpen(t, e.proj, "update")
	if code != 1 || !strings.Contains(errs, `repository "other": network-denied: host "127.0.0.1" not in network.allow_hosts (mode allowlist)`) {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if bad.hits.Load() != 0 {
		t.Fatal("the unlisted server was contacted")
	}
	// only the listed repository: succeeds and installs the index.
	e2 := newLkEnv(t, fmt.Sprintf("network:\n  mode: allowlist\n  allow_hosts: [%q]\nrepositories:\n  - name: corp\n    url: %s\n", good.host(t), good.URL))
	if code, out, errs := runGymOpen(t, e2.proj, "update"); code != 0 || !strings.Contains(out, "updated registry: 1 skills") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
}

// D6: archive URLs obey the allowlist; loopback is not implicit.
func TestD6AllowlistArchiveURL(t *testing.T) {
	tmp := scratchDir(t)
	arch := makeTarGz(t, tmp, "lint.tar.gz", "lint/", skillFiles("lint", "a"))
	body, _ := os.ReadFile(arch)
	good := newLoopServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	bad := forbiddenServer(t)
	e := newLkEnv(t, fmt.Sprintf("network:\n  mode: allowlist\n  allow_hosts: [%q]\n", good.host(t)))

	code, _, errs := runGymOpen(t, e.proj, "learn", bad.URL+"/lint.tar.gz")
	if code != 1 || !strings.Contains(errs, `network-denied: host "127.0.0.1" not in network.allow_hosts (mode allowlist)`) {
		t.Fatalf("unlisted: exit %d\n%s", code, errs)
	}
	if bad.hits.Load() != 0 || installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("an unlisted archive host was contacted or installed")
	}
	if code, out, errs := runGymOpen(t, e.proj, "learn", good.URL+"/lint.tar.gz"); code != 0 {
		t.Fatalf("listed: exit %d\n%s\n%s", code, out, errs)
	}
	if !installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("listed archive not installed")
	}
	if ent := e.lockEntry(t, "lint"); ent.Kind != lock.KindArchive || ent.Source != good.URL+"/lint.tar.gz" {
		t.Fatalf("lock entry = %+v", ent)
	}
}

// Loopback is not implicitly allowed: an allowlist without it denies it.
func TestLoopbackNotImplicitUnderAllowlist(t *testing.T) {
	srv := forbiddenServer(t)
	e := newLkEnv(t, "network:\n  mode: allowlist\n  allow_hosts: [registry.corp.example]\nrepositories:\n  - name: local\n    url: "+srv.URL+"\n")
	code, _, errs := runGymOpen(t, e.proj, "update")
	if code != 1 || !strings.Contains(errs, "network-denied") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d hits %d\n%s", code, srv.hits.Load(), errs)
	}
}

// A project config cannot widen the operator's network policy, in a run.
func TestProjectConfigCannotOpenTheNetwork(t *testing.T) {
	srv := forbiddenServer(t)
	e := newLkEnv(t, "network:\n  mode: allowlist\n  allow_hosts: [registry.corp.example]\nrepositories:\n  - name: local\n    url: "+srv.URL+"\n")
	writeFiles(t, e.proj, map[string]string{".gym/config.yaml": "network:\n  mode: open\n  allow_hosts: [\"" + srv.host(t) + "\"]\n"})
	code, _, errs := runGymOpen(t, e.proj, "update")
	if code != 1 || !strings.Contains(errs, "network-denied") || srv.hits.Load() != 0 {
		t.Fatalf("exit %d hits %d\n%s", code, srv.hits.Load(), errs)
	}
}

// The event bus network adapter is an egress: a denied hub stops the run
// at startup.
func TestBusAdapterDeniedAtStartup(t *testing.T) {
	e := newLkEnv(t, "network:\n  mode: allowlist\n  allow_hosts: [registry.corp.example]\n")
	t.Setenv("GYM_BUS_URL", "ws://hub.corp.example:7000/bus")
	code, _, errs := runGymOpen(t, e.proj, "paths")
	if code != 1 || !strings.Contains(errs, `network-denied: host "hub.corp.example" not in network.allow_hosts (mode allowlist)`) {
		t.Fatalf("allowlist: exit %d\n%s", code, errs)
	}
	code, _, errs = e.gym(t, "paths") // --offline
	if code != 1 || !strings.Contains(errs, `network-denied: offline mode forbids network access to "hub.corp.example"`) {
		t.Fatalf("offline: exit %d\n%s", code, errs)
	}
}

// D3 (run): a project layer's auth is ignored with a warning and no token
// reaches the server it names.
func TestD3ProjectAuthIgnoredInARun(t *testing.T) {
	srv := newLoopServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"skills":[]}`)
	})
	tok := fakeSecret(t, "d3")
	t.Setenv("GYM_TEST_PROJECT_TOKEN", tok)
	e := newLkEnv(t, "")
	writeFiles(t, e.proj, map[string]string{".gym/config.yaml": "repositories:\n  - name: evil\n    url: " + srv.URL + "\n    auth: {type: bearer, token_env: GYM_TEST_PROJECT_TOKEN}\n"})
	code, _, errs := runGymOpen(t, e.proj, "update")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if !strings.Contains(errs, `ignoring auth of repository "evil"`) {
		t.Fatalf("no warning:\n%s", errs)
	}
	if srv.hits.Load() == 0 || srv.seen.Load() != "" {
		t.Fatalf("hits %d, Authorization %q: the project's auth was used", srv.hits.Load(), srv.seen.Load())
	}
}

// D13: require_pin remote refuses an unpinned git or archive source, and
// accepts it with an operator --digest.
func TestD13RequirePinRemoteGitAndArchive(t *testing.T) {
	lkRequireGit(t)
	e := newLkEnv(t, "admission:\n  require_pin: remote\n")
	repo := filepath.Join(e.proj, "up")
	lkGitRepo(t, repo, skillFiles("up", "a"))
	arch := makeTarGz(t, filepath.Join(e.proj, "pkgs"), "pk.tar.gz", "pk/", skillFiles("pk", "a"))

	for _, tok := range []string{"file://" + repo + "#main", arch} {
		code, _, errs := e.gym(t, "learn", tok)
		if code != 1 || !strings.Contains(errs, "admission-digest-required") {
			t.Fatalf("%s: exit %d\n%s", tok, code, errs)
		}
	}
	if installed(filepath.Join(e.proj, ".gym", "skills"), "up") || installed(filepath.Join(e.proj, ".gym", "skills"), "pk") {
		t.Fatal("an unpinned remote source was installed")
	}

	// the operator's digest pins the archive.
	tree := filepath.Join(scratchDir(t), "tree")
	writeFiles(t, tree, skillFiles("pk", "a"))
	if err := os.Chmod(filepath.Join(tree, "scripts", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := install.Digest(tree)
	if err != nil {
		t.Fatal(err)
	}
	e.mustGym(t, "learn", "--digest", d, arch)
	if !installed(filepath.Join(e.proj, ".gym", "skills"), "pk") {
		t.Fatal("pinned archive not installed")
	}
	// directory sources stay exempt under remote.
	e.mustGym(t, "learn", lkSkillSrc(t, filepath.Join(e.proj, "d"), "fmt", "a"))
}

// Archive sources install from a path, produce a relative archive lock
// entry in the tree, and `gym digest` agrees with the directory digest.
func TestArchiveSourcesInstallAndDigest(t *testing.T) {
	e := newLkEnv(t, "")
	pk := filepath.Join(e.proj, "pkgs")
	tgz := makeTarGz(t, pk, "lint.tar.gz", "lint/", skillFiles("lint", "a"))
	zp := makeZip(t, pk, "fmt.zip", "", skillFiles("fmt", "a"))
	e.mustGym(t, "learn", tgz, zp)
	skills := filepath.Join(e.proj, ".gym", "skills")
	if !installed(skills, "lint") || !installed(skills, "fmt") {
		t.Fatal("archives not installed")
	}
	if ent := e.lockEntry(t, "lint"); ent.Kind != lock.KindArchive || ent.Source != "pkgs/lint.tar.gz" || ent.Digest == "" {
		t.Fatalf("lint entry = %+v", ent)
	}
	// restore from the lock reinstalls an archive.
	if err := os.RemoveAll(e.skillDir("lint")); err != nil {
		t.Fatal(err)
	}
	e.mustGym(t, "learn")
	if !installed(skills, "lint") {
		t.Fatal("archive entry did not restore")
	}

	// digest: archive == directory of the same tree, both formats.
	_, dirOut, _ := e.gym(t, "digest", e.skillDir("lint"))
	for _, a := range []string{tgz} {
		if code, out, errs := e.gym(t, "digest", a); code != 0 || out != dirOut {
			t.Fatalf("digest %s: exit %d %q want %q\n%s", a, code, out, dirOut, errs)
		}
	}
	_, dirOut2, _ := e.gym(t, "digest", e.skillDir("fmt"))
	if code, out, _ := e.gym(t, "digest", zp); code != 0 || out != dirOut2 {
		t.Fatalf("zip digest %q want %q", out, dirOut2)
	}
}

// A hostile archive fails with archive-invalid and installs nothing.
func TestHostileArchiveIsRefusedByLearnAndDigest(t *testing.T) {
	e := newLkEnv(t, "")
	bad := makeSymlinkTarGz(t, filepath.Join(e.proj, "pkgs"), "lint.tar.gz")
	code, _, errs := e.gym(t, "learn", bad)
	if code != 1 || !strings.Contains(errs, "archive-invalid: symlink entry") {
		t.Fatalf("learn: exit %d\n%s", code, errs)
	}
	if installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
		t.Fatal("hostile archive installed")
	}
	if _, err := os.Stat(e.lockPath()); err == nil {
		t.Fatal("hostile archive locked")
	}
	if code, _, errs := e.gym(t, "digest", bad); code != 1 || !strings.Contains(errs, "archive-invalid") {
		t.Fatalf("digest: exit %d\n%s", code, errs)
	}
	// an archive with a ref is refused
	if code, _, errs := e.gym(t, "learn", "file://"+bad+"#v1"); code != 1 || !strings.Contains(errs, "no #ref") {
		t.Fatalf("ref: exit %d\n%s", code, errs)
	}
}

// Registry index over file:// and over a plain path, through the CLI.
func TestD14RegistryIndexFromDisk(t *testing.T) {
	for _, form := range []string{"path", "file"} {
		e := newLkEnv(t, "")
		idx := filepath.Join(scratchDir(t), "skills.json")
		src := lkSkillSrc(t, filepath.Join(e.proj, "m"), "lint", "a")
		writeFiles(t, filepath.Dir(idx), map[string]string{"skills.json": `{"repository":{"name":"mirror"},"skills":[{"name":"lint","version":"1.0.0","source_url":"` + src + `"}]}`})
		loc := idx
		if form == "file" {
			loc = "file://" + idx
		}
		writeUserConfigFor(t, e, "repositories:\n  - name: mirror\n    url: "+loc+"\n")
		e.mustGym(t, "update")
		e.mustGym(t, "learn", "lint")
		if !installed(filepath.Join(e.proj, ".gym", "skills"), "lint") {
			t.Fatalf("%s: not installed", form)
		}
	}
}

func writeUserConfigFor(t *testing.T, e lkEnv, extra string) {
	t.Helper()
	writeUserConfig(t, e.home, extra)
}

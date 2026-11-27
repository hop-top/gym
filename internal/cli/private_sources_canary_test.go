package cli_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// D4: a unique fake token, asserted absent from stdout, stderr (also under
// -V), state, lock, the config and data dirs and the project tree, after
// runs that used it, including failing ones. Three credential paths: a
// bearer token from the environment, a bearer token from a 0600 file, and
// a git credential helper; plus a token carried in an archive URL query.
func TestD4CanaryNeverReachesAnySink(t *testing.T) {
	envTok := fakeSecret(t, "env")
	fileTok := fakeSecret(t, "file")
	helperPass := fakeSecret(t, "helper")
	queryTok := fakeSecret(t, "query")
	t.Setenv("GYM_CANARY_TOKEN", envTok)

	arch := makeTarGz(t, scratchDir(t), "x.tar.gz", "s/", skillFiles("lint", "a"))
	archBytes, _ := os.ReadFile(arch)

	var failing atomic.Bool
	// server builds a registry+archive host that wants exactly one Authorization value.
	server := func(skill string, wantAuth func(*http.Request) bool) *loopServer {
		var srv *loopServer
		srv = newLoopServer(t, func(w http.ResponseWriter, r *http.Request) {
			// An adversarial server: every refusal echoes what it was sent.
			if failing.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "boom: Authorization=%q Cookie=%q url=%s", r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.URL)
				return
			}
			if strings.HasSuffix(r.URL.Path, ".tar.gz") && r.URL.Query().Get("token") == queryTok {
				_, _ = w.Write(archBytes)
				return
			}
			if !wantAuth(r) {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprintf(w, "denied: Authorization=%q", r.Header.Get("Authorization"))
				return
			}
			switch {
			case strings.HasSuffix(r.URL.Path, "skills.json"):
				fmt.Fprintf(w, `{"repository":{"name":%q},"skills":[{"name":%q,"version":"1.0.0","source_url":"%s/%s.tar.gz"}]}`, skill, skill, srv.URL, skill)
			case strings.HasSuffix(r.URL.Path, ".tar.gz"):
				_, _ = w.Write(archBytes)
			default:
				w.WriteHeader(404)
			}
		})
		return srv
	}
	envSrv := server("lint", func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+envTok })
	fileSrv := server("fmt", func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+fileTok })
	credSrv := server("lint2", func(r *http.Request) bool {
		u, p, ok := r.BasicAuth()
		return ok && u == "svc" && p == helperPass
	})

	tokFile := filepath.Join(scratchDir(t), "corp.token")
	if err := os.WriteFile(tokFile, []byte(fileTok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(scratchDir(t), "helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n[ \"$1\" = get ] || exit 0\ncat >/dev/null\necho username=svc\necho password="+helperPass+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCfg := filepath.Join(scratchDir(t), "gitconfig")
	if err := os.WriteFile(gitCfg, []byte("[credential]\n\thelper = "+helper+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitCfg)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	e := newLkEnv(t, fmt.Sprintf(`repositories:
  - name: envrepo
    url: %s/skills.json
    auth: {type: bearer, token_env: GYM_CANARY_TOKEN}
  - name: filerepo
    url: %s/skills.json
    auth: {type: bearer, token_file: %s}
  - name: credrepo
    url: %s/skills.json
    auth: {type: git-credential}
`, envSrv.URL, fileSrv.URL, tokFile, credSrv.URL))

	var sinks []string
	run := func(wantOK bool, args ...string) {
		t.Helper()
		code, out, errs := runGymOpen(t, e.proj, append([]string{"-V"}, args...)...)
		sinks = append(sinks, out, errs)
		if (code == 0) != wantOK {
			t.Fatalf("gym %v: exit %d\n%s\n%s", args, code, out, errs)
		}
	}

	// working runs that use every credential
	run(true, "update")
	run(true, "learn", "lint", "fmt", "lint2")
	for _, n := range []string{"lint", "fmt", "lint2"} {
		if !installed(filepath.Join(e.proj, ".gym", "skills"), n) {
			t.Fatalf("%s not installed through its authenticated repository", n)
		}
	}
	// a token carried in an archive URL query: used to fetch, stored redacted
	run(true, "learn", envSrv.URL+"/viaquery.tar.gz?token="+queryTok)

	// failing runs: the server fails and echoes what it was sent
	failing.Store(true)
	run(false, "update")
	run(false, "learn", "--no-lock", "lint")
	// a token file that is too open fails by naming the path, not the value
	if err := os.Chmod(tokFile, 0o644); err != nil {
		t.Fatal(err)
	}
	failing.Store(false)
	run(false, "update")
	// an https-less remote host never gets a token, and says so without it
	t.Setenv("GYM_CANARY_TOKEN", envTok)
	writeUserConfigFor(t, e, "repositories:\n  - name: remote\n    url: http://registry.corp.example/skills.json\n    auth: {type: bearer, token_env: GYM_CANARY_TOKEN}\n")
	run(false, "update")

	all := strings.Join(sinks, "\n")
	for name, secret := range map[string]string{
		"env token": envTok, "file token": fileTok, "helper password": helperPass, "query token": queryTok,
		"basic header": base64.StdEncoding.EncodeToString([]byte("svc:" + helperPass)),
	} {
		if strings.Contains(all, secret) {
			t.Errorf("%s reached stdout/stderr", name)
		}
		if hits := walkContains(t, secret, e.home, e.proj); len(hits) > 0 {
			t.Errorf("%s was written to disk: %v", name, hits)
		}
	}
	// the query token is stored and logged redacted
	st := lkRead(t, filepath.Join(e.proj, ".gym", "installed.json"))
	lk := lkRead(t, e.lockPath())
	if !strings.Contains(st, "token=REDACTED") || !strings.Contains(lk, "token=REDACTED") {
		t.Errorf("redacted source missing\nstate: %s\nlock: %s", st, lk)
	}
	if u, err := url.Parse(envSrv.URL); err == nil && strings.Contains(lk, "viaquery.tar.gz?token="+queryTok) {
		t.Errorf("lock holds the query token for %s", u.Host)
	}
}

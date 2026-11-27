package registry_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/netpolicy"
	"hop.top/gym/internal/registry"
)

const indexBody = `{"repository":{"name":"corp"},"skills":[{"name":"lint","version":"1.0.0","source_url":"/x/lint"}]}`

// fakeToken is a unique, obviously fake secret built at run time.
func fakeToken(t *testing.T) string {
	t.Helper()
	return "faketok-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")) + fmt.Sprintf("-%d", time.Now().UnixNano())
}

// failTransport fails the test when anything is dialled.
type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("network was used: %s", r.URL)
	return nil, fmt.Errorf("must not dial")
}

// onlyTransport dials the one listed host and fails the test for any other.
type onlyTransport struct {
	t    *testing.T
	host string
}

func (o onlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != o.host {
		o.t.Errorf("network was used: %s", r.URL)
		return nil, fmt.Errorf("must not dial")
	}
	return http.DefaultTransport.RoundTrip(r)
}

func setPolicy(t *testing.T, p netpolicy.Policy) {
	t.Helper()
	t.Cleanup(netpolicy.Set(p))
}

func hostOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	return u.Host
}

// authServer serves the index only to the exact Authorization header.
func authServer(t *testing.T, want string, seen *atomic.Value) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			seen.Store(r.Header.Get("Authorization"))
		}
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, indexBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeTokenFile(t *testing.T, tok string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "corp.token")
	if err := os.WriteFile(p, []byte(tok+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// D1: a bearer token from an environment variable or a 0600 file is sent;
// a group/world-accessible file is refused.
func TestD1BearerFromEnvAndFile(t *testing.T) {
	tok := fakeToken(t)
	srv := authServer(t, "Bearer "+tok, nil)

	t.Setenv("GYM_TEST_TOKEN", tok)
	src := registry.RepositorySource{Name: "corp", URL: srv.URL, Auth: &config.AuthConfig{Type: "bearer", TokenEnv: "GYM_TEST_TOKEN"}}
	if b, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); err != nil || string(b) != indexBody {
		t.Fatalf("env token: %v %q", err, b)
	}

	good := writeTokenFile(t, tok, 0o600)
	src.Auth = &config.AuthConfig{Type: "bearer", TokenFile: good}
	if _, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); err != nil {
		t.Fatalf("file token: %v", err)
	}

	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		bad := writeTokenFile(t, tok, mode)
		src.Auth = &config.AuthConfig{Type: "bearer", TokenFile: bad}
		_, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src)
		if err == nil || err.Error() != "auth: token file "+bad+" is group/world accessible" {
			t.Fatalf("mode %o: err = %v", mode, err)
		}
		if strings.Contains(err.Error(), tok) {
			t.Fatal("token in error")
		}
	}
}

// The ownership half of the token_file rule: provable only as root, which
// can hand the file to another user.
func TestD1TokenFileNotOwnedByUser(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root to chown the token file")
	}
	tok := fakeToken(t)
	p := writeTokenFile(t, tok, 0o600)
	if err := os.Chown(p, 12345, 12345); err != nil {
		t.Skip("cannot chown: " + err.Error())
	}
	srv := authServer(t, "Bearer "+tok, nil)
	src := registry.RepositorySource{URL: srv.URL, Auth: &config.AuthConfig{Type: "bearer", TokenFile: p}}
	if _, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); err == nil || !strings.Contains(err.Error(), "is group/world accessible") {
		t.Fatalf("err = %v", err)
	}
}

func TestD1TokenIsReadPerRequest(t *testing.T) {
	first, second := fakeToken(t)+"-1", fakeToken(t)+"-2"
	var want atomic.Value
	want.Store("Bearer " + first)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want.Load().(string) {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, indexBody)
	}))
	defer srv.Close()
	p := writeTokenFile(t, first, 0o600)
	src := registry.RepositorySource{URL: srv.URL, Auth: &config.AuthConfig{Type: "bearer", TokenFile: p}}
	if _, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(second), 0o600); err != nil {
		t.Fatal(err)
	}
	want.Store("Bearer " + second)
	if _, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); err != nil {
		t.Fatalf("rotated token not picked up: %v", err)
	}
}

func TestBearerErrorsNameTheSourceNeverTheValue(t *testing.T) {
	srv := authServer(t, "x", nil)
	for name, a := range map[string]*config.AuthConfig{
		"unset env":      {Type: "bearer", TokenEnv: "GYM_TEST_UNSET_VAR"},
		"both":           {Type: "bearer", TokenEnv: "A", TokenFile: "/b"},
		"neither":        {Type: "bearer"},
		"unknown type":   {Type: "basic"},
		"missing file":   {Type: "bearer", TokenFile: filepath.Join(t.TempDir(), "nope")},
		"cred with file": {Type: "git-credential", TokenFile: "/x"},
	} {
		_, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), registry.RepositorySource{URL: srv.URL, Auth: a})
		if err == nil || !strings.HasPrefix(err.Error(), "auth: ") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// a token with a newline is refused, without echoing it
	t.Setenv("GYM_TEST_BAD", "abc\ndef")
	_, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), registry.RepositorySource{URL: srv.URL, Auth: &config.AuthConfig{Type: "bearer", TokenEnv: "GYM_TEST_BAD"}})
	if err == nil || strings.Contains(err.Error(), "abc") {
		t.Fatalf("err = %v", err)
	}
}

// D2: the credential goes to the exact host of the repository URL and is
// dropped on a redirect to another host or port, and from any redirect to
// a host the policy does not allow nothing is sent at all.
func TestD2RedirectDoesNotForwardAuthorization(t *testing.T) {
	tok := fakeToken(t)
	var gotB atomic.Value
	gotB.Store("never-called")
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotB.Store(r.Header.Get("Authorization") + "|" + r.Header.Get("Cookie"))
		fmt.Fprint(w, indexBody)
	}))
	defer other.Close()
	_, otherPort, _ := net.SplitHostPort(hostOf(t, other))

	for name, target := range map[string]string{
		"same host, other port": other.URL,
		"other host name":       "http://localhost:" + otherPort,
	} {
		gotB.Store("never-called")
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target+"/index.json", http.StatusFound)
		}))
		t.Setenv("GYM_TEST_TOKEN", tok)
		src := registry.RepositorySource{URL: first.URL, Auth: &config.AuthConfig{Type: "bearer", TokenEnv: "GYM_TEST_TOKEN"}}
		if _, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		first.Close()
		if got := gotB.Load().(string); got != "|" {
			t.Fatalf("%s: the redirect target saw %q, want no Authorization or Cookie", name, got)
		}
	}

	// A redirect inside the same host keeps the credential.
	var path2 atomic.Value

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		path2.Store(r.Header.Get("Authorization"))
		fmt.Fprint(w, indexBody)
	}))
	defer srv.Close()
	src := registry.RepositorySource{URL: srv.URL + "/start", Auth: &config.AuthConfig{Type: "bearer", TokenEnv: "GYM_TEST_TOKEN"}}
	if _, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if path2.Load() != "Bearer "+tok {
		t.Fatalf("same-host redirect lost the credential: %v", path2.Load())
	}
}

// The credential is not sent to an https-less, non-loopback host, and
// nothing is dialled to find that out.
func TestBearerNeverOverPlainHTTPToARemoteHost(t *testing.T) {
	t.Setenv("GYM_TEST_TOKEN", fakeToken(t))
	h := registry.HTTPFetcher{Client: &http.Client{Transport: failTransport{t}}}
	_, err := h.FetchSource(context.Background(), registry.RepositorySource{URL: "http://reg.corp.example/s.json", Auth: &config.AuthConfig{Type: "bearer", TokenEnv: "GYM_TEST_TOKEN"}})
	if err == nil || !strings.Contains(err.Error(), "credentials are sent only over https") {
		t.Fatalf("err = %v", err)
	}
}

// Archive URLs on another host do not get the repository's credential.
func TestGetSendsCredentialOnlyToTheRepositoryHost(t *testing.T) {
	tok := fakeToken(t)
	t.Setenv("GYM_TEST_TOKEN", tok)
	var seen atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	auth := &config.AuthConfig{Type: "bearer", TokenEnv: "GYM_TEST_TOKEN"}
	// authURL names another host: no credential.
	resp, err := registry.Get(context.Background(), nil, srv.URL+"/a.zip", auth, "https://registry.corp.example/skills.json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if seen.Load() != "" {
		t.Fatalf("credential sent to a host that is not the repository's: %v", seen.Load())
	}
	// same host: sent.
	resp, err = registry.Get(context.Background(), nil, srv.URL+"/a.zip", auth, srv.URL+"/skills.json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if seen.Load() != "Bearer "+tok {
		t.Fatalf("credential not sent to the repository host: %v", seen.Load())
	}
}

// git-credential: `git credential fill` with prompts off; Basic header.
func TestGitCredentialAuth(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	secret := fakeToken(t)
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	script := filepath.Join(t.TempDir(), "helper.sh")
	body := "#!/bin/sh\n[ \"$1\" = get ] || exit 0\ncat >/dev/null\necho username=svc\necho password=" + secret + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[credential]\n\thelper = "+script+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "svc" || p != secret {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, indexBody)
	}))
	defer srv.Close()
	src := registry.RepositorySource{URL: srv.URL, Auth: &config.AuthConfig{Type: "git-credential"}}
	if b, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); err != nil || string(b) != indexBody {
		t.Fatalf("git-credential: %v", err)
	}

	// no helper: fails without prompting and without echoing anything
	if err := os.WriteFile(cfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_ASKPASS", "echo")
	done := make(chan error, 1)
	go func() { _, err := (registry.HTTPFetcher{}).FetchSource(context.Background(), src); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.HasPrefix(err.Error(), "auth: git credential fill") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("git credential fill hung (prompt not disabled)")
	}
}

// D6 (registry): under allowlist a listed host is fetched, an unlisted one
// is denied before any connection, and so is a redirect to one.
func TestD6RegistryAllowlist(t *testing.T) {
	var hits atomic.Int32
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, indexBody)
	}))
	defer allowed.Close()
	setPolicy(t, netpolicy.Policy{Mode: netpolicy.ModeAllowlist, AllowHosts: []string{hostOf(t, allowed)}})

	if _, err := (registry.HTTPFetcher{}).Fetch(context.Background(), allowed.URL); err != nil {
		t.Fatalf("allowed host: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}

	denied := registry.HTTPFetcher{Client: &http.Client{Transport: failTransport{t}}}
	_, err := denied.Fetch(context.Background(), "https://github.com/o/skills.json")
	if want := `network-denied: host "github.com" not in network.allow_hosts (mode allowlist)`; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}

	// redirect from an allowed host to an unlisted one: stopped before dialling it
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect target was contacted")
	}))
	defer forbidden.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, forbidden.URL, http.StatusFound)
	}))
	defer redir.Close()
	netpolicy.Set(netpolicy.Policy{Mode: netpolicy.ModeAllowlist, AllowHosts: []string{hostOf(t, redir)}})
	_, err = (registry.HTTPFetcher{}).Fetch(context.Background(), redir.URL)
	var ne *netpolicy.Error
	if err == nil || !errors.As(err, &ne) {
		t.Fatalf("redirect err = %v", err)
	}
}

// Offline denies even a loopback server, and a plain path still loads.
func TestOfflineRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("contacted offline")
	}))
	defer srv.Close()
	setPolicy(t, netpolicy.Policy{Mode: netpolicy.ModeOffline, AllowHosts: []string{hostOf(t, srv)}})
	_, err := (registry.HTTPFetcher{}).Fetch(context.Background(), srv.URL)
	if err == nil || !strings.HasPrefix(err.Error(), "network-denied: offline mode forbids network access to") {
		t.Fatalf("err = %v", err)
	}
	p := filepath.Join(t.TempDir(), "skills.json")
	if err := os.WriteFile(p, []byte(indexBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := (registry.HTTPFetcher{}).Fetch(context.Background(), p); err != nil || string(b) != indexBody {
		t.Fatalf("plain path offline: %v", err)
	}
}

// Refresh names every denied repository, keeps going past each, and fails
// without returning a partial index.
func TestRefreshContinuesPastDeniedRepositories(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, indexBody) }))
	defer ok.Close()
	setPolicy(t, netpolicy.Policy{Mode: netpolicy.ModeAllowlist, AllowHosts: []string{hostOf(t, ok)}})
	cfg := registry.Config{Repositories: []registry.RepositorySource{
		{Name: "pub1", URL: "https://a.public.example/s.json"},
		{Name: "corp", URL: ok.URL},
		{Name: "pub2", URL: "https://b.public.example/s.json"},
	}}
	_, _, err := registry.Refresh(context.Background(), cfg, registry.HTTPFetcher{Client: &http.Client{Transport: onlyTransport{t, hostOf(t, ok)}}}, registry.Index{})
	if err == nil {
		t.Fatal("denied repositories did not fail the refresh")
	}
	for _, want := range []string{
		`repository "pub1": network-denied: host "a.public.example"`,
		`repository "pub2": network-denied: host "b.public.example"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

// D14: an index on disk, by plain path or file:// URL, loads.
func TestD14RegistryFromFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "skills.json")
	if err := os.WriteFile(p, []byte(indexBody), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{p, "file://" + p} {
		idx, _, err := registry.Refresh(context.Background(), registry.Config{Repositories: []registry.RepositorySource{{Name: "mirror", URL: loc}}}, registry.HTTPFetcher{Client: &http.Client{Transport: failTransport{t}}}, registry.Index{})
		if err != nil || len(idx.Skills) != 1 || idx.Skills[0].Name != "lint" {
			t.Fatalf("%s: %v %+v", loc, err, idx)
		}
	}
	if _, err := (registry.HTTPFetcher{}).Fetch(context.Background(), filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("missing index accepted")
	}
	if _, err := (registry.HTTPFetcher{}).Fetch(context.Background(), "ftp://h/s.json"); err == nil {
		t.Fatal("ftp accepted")
	}
}

func TestFetchErrorRedactsQueryToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	secret := fakeToken(t)
	_, err := (registry.HTTPFetcher{}).Fetch(context.Background(), srv.URL+"/s.json?token="+secret)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "token=REDACTED") {
		t.Fatalf("err = %v", err)
	}
	// a transport error (nothing listening) redacts too
	_, err = (registry.HTTPFetcher{Client: &http.Client{Timeout: time.Second}}).Fetch(context.Background(), "http://127.0.0.1:1/s.json?token="+secret)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
}

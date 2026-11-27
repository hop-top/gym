package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/netpolicy"
	"hop.top/gym/internal/redact"
)

// AuthError is a credential problem. Its text never holds a secret: it
// names the variable, file or host, never the value.
type AuthError struct{ Msg string }

func (e *AuthError) Error() string { return "auth: " + e.Msg }

func authErrorf(format string, args ...any) *AuthError {
	return &AuthError{Msg: fmt.Sprintf(format, args...)}
}

// scrubbed is a network error whose text has been redacted.
type scrubbed struct {
	msg string
	err error
}

func (e *scrubbed) Error() string { return e.msg }
func (e *scrubbed) Unwrap() error { return e.err }

// scrub makes err safe to print: a policy denial passes through as is, any
// other text has URL passwords and query tokens removed.
func scrub(err error) error {
	var ne *netpolicy.Error
	if errors.As(err, &ne) {
		return ne
	}
	return &scrubbed{msg: redact.Text(err.Error()), err: err}
}

func hostPort(u *url.URL) string {
	h := strings.ToLower(u.Hostname())
	p := u.Port()
	if p == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			p = "443"
		case "http":
			p = "80"
		}
	}
	return h + ":" + p
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// secureForToken: a token travels over https, or over http to loopback only.
func secureForToken(u *url.URL) bool {
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		return isLoopbackHost(u.Hostname())
	}
	return false
}

// Get performs a policy-checked GET of rawURL. The check runs before the
// request and again on every redirect. When auth is set and rawURL is on
// exactly the host (and port) of authURL, the repository URL the token was
// configured for, the request carries the credential; it is never sent to
// another host, and an Authorization or Cookie header is dropped on any
// redirect that leaves that host or leaves https.
func Get(ctx context.Context, client *http.Client, rawURL string, auth *config.AuthConfig, authURL string) (*http.Response, error) {
	if err := netpolicy.Check(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, scrub(err)
	}
	var origin *url.URL
	if auth != nil && authURL != "" {
		if au, err := url.Parse(authURL); err == nil && hostPort(au) == hostPort(req.URL) {
			if !secureForToken(req.URL) {
				return nil, authErrorf("credentials are sent only over https (or http to a loopback host), not to %s", req.URL.Host)
			}
			h, err := authHeader(ctx, auth, authURL)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", h)
			origin = req.URL
		}
	}
	c := http.Client{Timeout: 20 * time.Second}
	if client != nil {
		c = *client
	}
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if err := netpolicy.Check(r.URL.String()); err != nil {
			return err
		}
		if origin == nil || hostPort(r.URL) != hostPort(origin) || !secureForToken(r.URL) {
			r.Header.Del("Authorization")
			r.Header.Del("Cookie")
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, scrub(err)
	}
	return resp, nil
}

// authHeader resolves the Authorization header value for a request to the
// repository at repoURL. Read per request; nothing is cached or written.
func authHeader(ctx context.Context, a *config.AuthConfig, repoURL string) (string, error) {
	switch a.Type {
	case config.AuthBearer:
		tok, err := bearerToken(a)
		if err != nil {
			return "", err
		}
		return "Bearer " + tok, nil
	case config.AuthGitCredential:
		return credentialHeader(ctx, repoURL)
	}
	return "", authErrorf("unknown auth type %q (one of: bearer, git-credential)", a.Type)
}

// ValidateAuth checks an auth block without reading any secret.
func ValidateAuth(a *config.AuthConfig) error {
	switch a.Type {
	case config.AuthBearer:
		if (a.TokenEnv == "") == (a.TokenFile == "") {
			return authErrorf("bearer needs exactly one of token_env or token_file")
		}
	case config.AuthGitCredential:
		if a.TokenEnv != "" || a.TokenFile != "" {
			return authErrorf("git-credential takes no token_env or token_file")
		}
	default:
		return authErrorf("unknown auth type %q (one of: bearer, git-credential)", a.Type)
	}
	return nil
}

func bearerToken(a *config.AuthConfig) (string, error) {
	if err := ValidateAuth(a); err != nil {
		return "", err
	}
	var raw string
	if a.TokenEnv != "" {
		raw = os.Getenv(a.TokenEnv)
		if strings.TrimSpace(raw) == "" {
			return "", authErrorf("environment variable %s is empty or unset", a.TokenEnv)
		}
	} else {
		b, err := readTokenFile(a.TokenFile)
		if err != nil {
			return "", err
		}
		raw = string(b)
	}
	tok := strings.TrimSpace(raw)
	if tok == "" {
		return "", authErrorf("token is empty")
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] <= 0x20 || tok[i] >= 0x7f {
			return "", authErrorf("token holds whitespace or non-printable characters")
		}
	}
	return tok, nil
}

func readTokenFile(path string) ([]byte, error) {
	p := path
	if strings.HasPrefix(p, "~/") {
		p = home() + p[1:]
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, authErrorf("token file %s: %v", path, pathError(err))
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, authErrorf("token file %s: %v", path, pathError(err))
	}
	if !st.Mode().IsRegular() || !tokenFileTrusted(st) {
		return nil, authErrorf("token file %s is group/world accessible", path)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(&limitedReader{f: f, n: 64 << 10}); err != nil {
		return nil, authErrorf("token file %s: %v", path, pathError(err))
	}
	return buf.Bytes(), nil
}

func pathError(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

func home() string { return os.Getenv("HOME") }

type limitedReader struct {
	f *os.File
	n int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errors.New("file too large")
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.f.Read(p)
	l.n -= int64(n)
	return n, err
}

// credentialHeader asks `git credential fill` for the repository's
// credential, with prompts disabled, and builds a Basic header. The
// helper's own output is never echoed on failure.
func credentialHeader(ctx context.Context, repoURL string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil || u.Host == "" {
		return "", authErrorf("git-credential: repository URL has no host")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "core.askPass=", "-c", "credential.interactive=never", "credential", "fill")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS=")
	cmd.Stdin = strings.NewReader("protocol=" + u.Scheme + "\nhost=" + u.Host + "\n\n")
	out, err := cmd.Output()
	if err != nil {
		return "", authErrorf("git credential fill failed for %s", u.Host)
	}
	var user, pass string
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			switch k {
			case "username":
				user = v
			case "password":
				pass = v
			}
		}
	}
	if pass == "" {
		return "", authErrorf("git credential fill returned no password for %s", u.Host)
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass)), nil
}

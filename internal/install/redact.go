package install

import (
	"regexp"
	"strings"
)

// ReasonSourceCredentials rejects a source URL that embeds a password.
const ReasonSourceCredentials = "source-credentials-in-url"

// urlAuthority splits a scheme://authority URL, returning the byte range
// of the authority within raw. ok is false for non-URL strings (local
// paths, scp-style git@host:path).
func urlAuthority(raw string) (start, end int, ok bool) {
	i := strings.Index(raw, "://")
	if i <= 0 {
		return 0, 0, false
	}
	for _, c := range raw[:i] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return 0, 0, false
		}
	}
	start = i + 3
	end = len(raw)
	if j := strings.IndexAny(raw[start:], "/?#"); j >= 0 {
		end = start + j
	}
	return start, end, true
}

// urlPassword returns the index of the ':' separating user and password
// within the authority, and of the '@' ending userinfo; -1 when the URL
// has no userinfo password. Username-only userinfo is not a password.
func urlPassword(raw string) (colon, at int) {
	s, e, ok := urlAuthority(raw)
	if !ok {
		return -1, -1
	}
	a := raw[s:e]
	at = strings.LastIndex(a, "@")
	if at < 0 {
		return -1, -1
	}
	c := strings.Index(a[:at], ":")
	if c < 0 {
		return -1, -1
	}
	return s + c, s + at
}

// CheckSourceURL rejects a source URL that carries a userinfo password,
// before any network use. git@host:org/repo and ssh://git@host/... are
// fine: a username alone is not a credential.
func CheckSourceURL(raw string) error {
	if c, _ := urlPassword(raw); c >= 0 {
		return &RefusedError{Reason: ReasonSourceCredentials, Detail: "use a git credential helper or ssh key"}
	}
	return nil
}

var credParam = regexp.MustCompile(`(?i)([?&;][^=&#;\s]*(?:token|secret|pass|pwd|key|auth|sig|cred)[^=&#;\s]*=)[^&#;\s]*`)

// RedactURL strips what must not be stored or shown from a source URL: a
// userinfo password and the value of credential-looking query parameters
// (token, key, secret, password, auth, sig, ...). Anything that is not a
// URL is returned unchanged.
func RedactURL(raw string) string {
	if _, _, ok := urlAuthority(raw); !ok {
		return raw
	}
	if c, at := urlPassword(raw); c >= 0 {
		raw = raw[:c] + raw[at:]
	}
	return credParam.ReplaceAllString(raw, "${1}REDACTED")
}

// redactText replaces every occurrence of source in text with its
// redacted form (git echoes the URL it was given).
func redactText(text, source string) string {
	r := RedactURL(source)
	if r == source {
		return text
	}
	return strings.ReplaceAll(text, source, r)
}

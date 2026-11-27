// Package redact strips secrets from text that is about to be stored,
// logged or shown: URL passwords, credential-looking query parameters and
// known secret values.
package redact

import (
	"regexp"
	"strings"
)

// Mask replaces a removed secret value.
const Mask = "REDACTED"

// authority splits a scheme://authority URL, returning the byte range of
// the authority within raw. ok is false for non-URL strings (local paths,
// scp-style git@host:path).
func authority(raw string) (start, end int, ok bool) {
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

// Password returns the index of the ':' separating user and password
// within the authority, and of the '@' ending userinfo; -1 when the URL
// has no userinfo password. Username-only userinfo is not a password.
func Password(raw string) (colon, at int) {
	s, e, ok := authority(raw)
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

var credParam = regexp.MustCompile(`(?i)([?&;][^=&#;\s]*(?:token|secret|pass|pwd|key|auth|sig|cred)[^=&#;\s]*=)[^&#;\s]*`)

// URL strips what must not be stored or shown from a URL: a userinfo
// password and the value of credential-looking query parameters. Anything
// that is not a URL is returned unchanged.
func URL(raw string) string {
	if _, _, ok := authority(raw); !ok {
		return raw
	}
	if c, at := Password(raw); c >= 0 {
		raw = raw[:c] + raw[at:]
	}
	return credParam.ReplaceAllString(raw, "${1}"+Mask)
}

// Text replaces every secret in text with Mask, and every URL-shaped word
// with its redacted form.
func Text(text string, secrets ...string) string {
	for _, s := range secrets {
		if len(s) >= 4 {
			text = strings.ReplaceAll(text, s, Mask)
		}
	}
	words := strings.Fields(text)
	for _, w := range words {
		if r := URL(strings.Trim(w, `'"`)); r != strings.Trim(w, `'"`) {
			text = strings.ReplaceAll(text, strings.Trim(w, `'"`), r)
		}
	}
	return text
}

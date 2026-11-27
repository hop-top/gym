package install

import (
	"strings"

	"hop.top/gym/internal/redact"
)

// ReasonSourceCredentials rejects a source URL that embeds a password.
const ReasonSourceCredentials = "source-credentials-in-url"

// CheckSourceURL rejects a source URL that carries a userinfo password,
// before any network use. git@host:org/repo and ssh://git@host/... are
// fine: a username alone is not a credential.
func CheckSourceURL(raw string) error {
	if c, _ := redact.Password(raw); c >= 0 {
		return &RefusedError{Reason: ReasonSourceCredentials, Detail: "use a git credential helper or ssh key"}
	}
	return nil
}

// RedactURL strips what must not be stored or shown from a source URL: a
// userinfo password and the value of credential-looking query parameters
// (token, key, secret, password, auth, sig, ...). Anything that is not a
// URL is returned unchanged.
func RedactURL(raw string) string { return redact.URL(raw) }

// redactText replaces every occurrence of source in text with its
// redacted form (git echoes the URL it was given).
func redactText(text, source string) string {
	r := RedactURL(source)
	if r == source {
		return text
	}
	return strings.ReplaceAll(text, source, r)
}

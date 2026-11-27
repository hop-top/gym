// Package netpolicy is the single place gym decides whether a host may be
// contacted. Every egress calls Check with the URL it is about to use;
// nothing else in gym makes that decision.
//
// The policy is by host name, not by network path or resolved address: a
// firewall remains the guarantee. Names are compared case-insensitively and
// as given (no IDNA: list the punycode form of an internationalised name).
package netpolicy

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// Modes, in increasing order of restriction.
const (
	ModeOpen      = "open"
	ModeAllowlist = "allowlist"
	ModeOffline   = "offline"
)

// ReasonDenied is the stable rejection reason of a denied host.
const ReasonDenied = "network-denied"

// Policy is the effective network policy of a run.
type Policy struct {
	Mode       string
	AllowHosts []string
}

// Error is a denial. Its text is the operator-facing message.
type Error struct {
	Host string
	Mode string
}

func (e *Error) Error() string {
	if e.Mode == ModeOffline {
		return fmt.Sprintf("%s: offline mode forbids network access to %q", ReasonDenied, e.Host)
	}
	return fmt.Sprintf("%s: host %q not in network.allow_hosts (mode %s)", ReasonDenied, e.Host, e.Mode)
}

// ModeRank orders the modes: open < allowlist < offline.
func ModeRank(mode string) (int, bool) {
	switch mode {
	case ModeOpen:
		return 0, true
	case ModeAllowlist:
		return 1, true
	case ModeOffline:
		return 2, true
	}
	return 0, false
}

// Raise returns the stricter of two modes; an unknown or empty value loses.
func Raise(a, b string) string {
	ra, aok := ModeRank(a)
	rb, bok := ModeRank(b)
	switch {
	case !bok:
		return a
	case !aok || rb > ra:
		return b
	}
	return a
}

// current is the process-wide policy; the zero value is open.
var current atomic.Pointer[Policy]

// Set installs p as the current policy and returns a function restoring the
// previous one. Tests that call Set must defer the restore.
func Set(p Policy) (restore func()) {
	prev := current.Load()
	cp := Policy{Mode: p.Mode, AllowHosts: append([]string(nil), p.AllowHosts...)}
	current.Store(&cp)
	return func() { current.Store(prev) }
}

// Current returns the current policy.
func Current() Policy {
	if p := current.Load(); p != nil {
		return Policy{Mode: p.Mode, AllowHosts: append([]string(nil), p.AllowHosts...)}
	}
	return Policy{Mode: ModeOpen}
}

// Open reports whether the current mode is open.
func Open() bool {
	m := Current().Mode
	return m == "" || m == ModeOpen
}

// Check applies the current policy to rawURL.
func Check(rawURL string) error { return Current().Check(rawURL) }

// Check reports whether rawURL may be contacted under p. A URL with no
// network host (a path, file://) is always allowed.
func (p Policy) Check(rawURL string) error {
	if p.Mode == "" || p.Mode == ModeOpen {
		return nil
	}
	host, port, ok := HostOf(rawURL)
	if !ok {
		return nil
	}
	if p.Mode == ModeOffline {
		return &Error{Host: host, Mode: p.Mode}
	}
	for _, pat := range p.AllowHosts {
		if matches(pat, host, port) {
			return nil
		}
	}
	return &Error{Host: host, Mode: p.Mode}
}

var scpRe = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]+|\[[^\]]+\]):`)

// HostOf extracts the lower-cased host and the effective port ("" when
// unknown) of a URL or scp-like git address. ok is false for locations
// that name no network host: plain paths and file:// URLs.
func HostOf(raw string) (host, port string, ok bool) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i > 0 {
		u, err := url.Parse(raw)
		if err != nil {
			// Unparsable: fail closed with the raw text as the host.
			return strings.ToLower(raw), "", true
		}
		if strings.EqualFold(u.Scheme, "file") {
			return "", "", false
		}
		h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if h == "" {
			return "", "", false
		}
		port = u.Port()
		if port == "" {
			port = defaultPort(strings.ToLower(u.Scheme))
		}
		return h, port, true
	}
	if strings.Contains(raw, "::") && !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, ".") {
		// git remote-helper form (ext::..., transport::address): not a path.
		return strings.ToLower(raw[:strings.Index(raw, "::")]), "", true
	}
	if m := scpRe.FindStringSubmatch(raw); m != nil && !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, ".") {
		h := strings.ToLower(strings.Trim(m[1], "[]"))
		if len(h) == 1 && h[0] >= 'a' && h[0] <= 'z' {
			return "", "", false // C:\path
		}
		return h, "22", true
	}
	return "", "", false
}

func defaultPort(scheme string) string {
	switch scheme {
	case "https", "wss":
		return "443"
	case "http", "ws":
		return "80"
	case "ssh", "git+ssh", "ssh+git":
		return "22"
	case "git":
		return "9418"
	}
	return ""
}

// splitPattern splits an allow_hosts entry into host (or *.suffix) and port.
func splitPattern(pat string) (host, port string) {
	pat = strings.ToLower(strings.TrimSpace(pat))
	if strings.HasPrefix(pat, "[") {
		if i := strings.Index(pat, "]"); i > 0 {
			host = pat[1:i]
			port = strings.TrimPrefix(pat[i+1:], ":")
			return host, port
		}
	}
	if strings.Count(pat, ":") == 1 {
		i := strings.Index(pat, ":")
		return pat[:i], pat[i+1:]
	}
	return pat, ""
}

func matches(pat, host, port string) bool {
	ph, pp := splitPattern(pat)
	if pp != "" && pp != port {
		return false
	}
	ph = strings.TrimSuffix(ph, ".")
	if suffix, ok := strings.CutPrefix(ph, "*."); ok {
		return strings.HasSuffix(host, "."+suffix) && len(host) > len(suffix)+1
	}
	return ph == host
}

// ValidatePattern checks an allow_hosts entry.
func ValidatePattern(pat string) error {
	bad := func() error {
		return fmt.Errorf("invalid network.allow_hosts entry %q (want host, host:port or *.suffix)", pat)
	}
	h, p := splitPattern(pat)
	if h == "" || strings.ContainsAny(pat, "/@ \t") || strings.Contains(pat, "://") {
		return bad()
	}
	if p != "" {
		for _, c := range p {
			if c < '0' || c > '9' {
				return bad()
			}
		}
	}
	if strings.Contains(h, "*") && !(strings.HasPrefix(h, "*.") && !strings.Contains(h[2:], "*") && len(h) > 2) {
		return bad()
	}
	if net.ParseIP(h) == nil && strings.ContainsAny(h, "[]") {
		return bad()
	}
	return nil
}

// covers reports whether every host x matches is matched by y.
func covers(y, x string) bool {
	yh, yp := splitPattern(y)
	xh, xp := splitPattern(x)
	if yp != "" && yp != xp {
		return false
	}
	yh, xh = strings.TrimSuffix(yh, "."), strings.TrimSuffix(xh, ".")
	if ys, ok := strings.CutPrefix(yh, "*."); ok {
		if xs, ok := strings.CutPrefix(xh, "*."); ok {
			return xs == ys || strings.HasSuffix(xs, "."+ys)
		}
		return strings.HasSuffix(xh, "."+ys)
	}
	if strings.HasPrefix(xh, "*.") {
		return false
	}
	return yh == xh
}

// Intersect returns the allow-list that admits only hosts both a and b
// admit: each entry of one that the other covers. Sorted, de-duplicated.
func Intersect(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, x := range b {
		for _, y := range a {
			if covers(y, x) {
				add(x)
				break
			}
		}
	}
	for _, y := range a {
		for _, x := range b {
			if covers(x, y) {
				add(y)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

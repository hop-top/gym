package netpolicy

import (
	"errors"
	"reflect"
	"testing"
)

func TestCheckModes(t *testing.T) {
	allow := Policy{Mode: ModeAllowlist, AllowHosts: []string{"reg.corp.example", "*.mirror.example", "127.0.0.1:4873", "git.corp.example:2222"}}
	cases := []struct {
		name string
		p    Policy
		url  string
		deny string // expected error text, "" = allowed
	}{
		{"open allows anything", Policy{Mode: ModeOpen}, "https://github.com/x/y", ""},
		{"zero value is open", Policy{}, "https://github.com/x/y", ""},
		{"listed host", allow, "https://reg.corp.example/skills.json", ""},
		{"host is case-insensitive", allow, "https://REG.Corp.Example/skills.json", ""},
		{"unlisted host", allow, "https://github.com/x", `network-denied: host "github.com" not in network.allow_hosts (mode allowlist)`},
		{"suffix match", allow, "https://a.mirror.example/x", ""},
		{"suffix does not match the bare suffix", allow, "https://mirror.example/x", `network-denied: host "mirror.example" not in network.allow_hosts (mode allowlist)`},
		{"suffix does not match a look-alike", allow, "https://evilmirror.example/x", `network-denied: host "evilmirror.example" not in network.allow_hosts (mode allowlist)`},
		{"loopback listed with port", allow, "http://127.0.0.1:4873/skills.json", ""},
		{"loopback other port", allow, "http://127.0.0.1:9/skills.json", `network-denied: host "127.0.0.1" not in network.allow_hosts (mode allowlist)`},
		{"loopback is not implicit", Policy{Mode: ModeAllowlist}, "http://127.0.0.1:1/x", `network-denied: host "127.0.0.1" not in network.allow_hosts (mode allowlist)`},
		{"scp-like", allow, "git@github.com:org/repo.git", `network-denied: host "github.com" not in network.allow_hosts (mode allowlist)`},
		{"scp-like listed host:port does not match scp default port", allow, "git@git.corp.example:org/repo.git", `network-denied: host "git.corp.example" not in network.allow_hosts (mode allowlist)`},
		{"ssh url with port", allow, "ssh://git@git.corp.example:2222/org/repo.git", ""},
		{"file url", allow, "file:///srv/mirror", ""},
		{"plain path", allow, "/srv/mirror/skills.json", ""},
		{"relative path", allow, "./skills.json", ""},
		{"offline denies", Policy{Mode: ModeOffline}, "https://github.com/x", `network-denied: offline mode forbids network access to "github.com"`},
		{"offline denies loopback", Policy{Mode: ModeOffline, AllowHosts: []string{"127.0.0.1"}}, "http://127.0.0.1:1/x", `network-denied: offline mode forbids network access to "127.0.0.1"`},
		{"offline allows a path", Policy{Mode: ModeOffline}, "/srv/x.tgz", ""},
		{"offline allows file url", Policy{Mode: ModeOffline}, "file:///srv/x.tgz", ""},
		{"ext helper form is a host", allow, "ext::sh -c true", `network-denied: host "ext" not in network.allow_hosts (mode allowlist)`},
	}
	for _, c := range cases {
		err := c.p.Check(c.url)
		switch {
		case c.deny == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.deny != "" && (err == nil || err.Error() != c.deny):
			t.Errorf("%s: got %v, want %s", c.name, err, c.deny)
		}
		var ne *Error
		if c.deny != "" && !errors.As(err, &ne) {
			t.Errorf("%s: not a *Error", c.name)
		}
	}
}

func TestSetRestores(t *testing.T) {
	restore := Set(Policy{Mode: ModeOffline})
	if err := Check("https://x.example/"); err == nil {
		t.Fatal("offline not applied")
	}
	if Open() {
		t.Fatal("Open under offline")
	}
	restore()
	if err := Check("https://x.example/"); err != nil || !Open() {
		t.Fatalf("not restored: %v", err)
	}
}

func TestRaise(t *testing.T) {
	for _, c := range [][3]string{
		{"open", "allowlist", "allowlist"}, {"allowlist", "open", "allowlist"},
		{"offline", "allowlist", "offline"}, {"open", "", "open"}, {"open", "bogus", "open"},
	} {
		if got := Raise(c[0], c[1]); got != c[2] {
			t.Errorf("Raise(%s,%s)=%s want %s", c[0], c[1], got, c[2])
		}
	}
}

func TestIntersect(t *testing.T) {
	got := Intersect([]string{"a.example", "*.corp.example", "b.example:443"}, []string{"a.example", "x.corp.example", "c.example", "b.example"})
	want := []string{"a.example", "b.example:443", "x.corp.example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// A port-less entry is wider than a host:port entry: the narrower wins.
	got = Intersect([]string{"b.example"}, []string{"b.example:443"})
	if !reflect.DeepEqual(got, []string{"b.example:443"}) {
		t.Fatalf("got %v", got)
	}
	if got := Intersect([]string{"a.example"}, nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestValidatePattern(t *testing.T) {
	for _, ok := range []string{"host", "host:80", "*.suffix.example", "127.0.0.1:4873", "[::1]:80"} {
		if err := ValidatePattern(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "https://host", "host/path", "*", "a*.example", "*.", "host:x", "user@host"} {
		if err := ValidatePattern(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

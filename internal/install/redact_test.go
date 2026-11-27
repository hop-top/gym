package install

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://user:hunter2@host/org/repo.git":        "https://user@host/org/repo.git",
		"https://user:p@ss@host/x":                      "https://user@host/x",
		"https://:tok@host/x":                           "https://@host/x",
		"https://host/x?token=abc&ref=main":             "https://host/x?token=REDACTED&ref=main",
		"https://host/x?ref=main&access_token=abc#frag": "https://host/x?ref=main&access_token=REDACTED#frag",
		"https://host/x?API_KEY=abc&Password=p":         "https://host/x?API_KEY=REDACTED&Password=REDACTED",
		"https://u:p@host/x?sig=z":                      "https://u@host/x?sig=REDACTED",
		"git@host:org/repo":                             "git@host:org/repo",
		"ssh://git@host/org/repo.git":                   "ssh://git@host/org/repo.git",
		"ssh://git@host:22/org/repo.git":                "ssh://git@host:22/org/repo.git",
		"https://host/org/repo.git":                     "https://host/org/repo.git",
		"/local/dir":                                    "/local/dir",
		"skill-name":                                    "skill-name",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckSourceURL(t *testing.T) {
	bad := []string{"https://u:p@host/x", "http://u:p@host/x", "ssh://u:p@host/x", "https://:p@host/x"}
	for _, u := range bad {
		var re *RefusedError
		err := CheckSourceURL(u)
		if !errors.As(err, &re) || re.Reason != ReasonSourceCredentials {
			t.Errorf("%q: err = %v", u, err)
		} else if err.Error() != "source-credentials-in-url: use a git credential helper or ssh key" {
			t.Errorf("message = %q", err)
		}
	}
	for _, u := range []string{"git@host:org/repo", "ssh://git@host/org/repo", "ssh://git@host:22/x", "https://host/x?token=a", "/dir", "https://u@host/x"} {
		if err := CheckSourceURL(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
}

func TestRedactText(t *testing.T) {
	src := "https://host/x?token=SECRET"
	got := redactText("fatal: unable to access '"+src+"/'", src)
	if strings.Contains(got, "SECRET") {
		t.Fatalf("not redacted: %s", got)
	}
}

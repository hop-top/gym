package guard_test

import (
	"bytes"
	"strings"
	"testing"

	charmlog "charm.land/log/v2"

	"hop.top/gym/internal/guard"
)

const orgDenyRule = "name: org-rule\nwhen: payload.marker == \"bad\"\neffect: deny\nmessage: org rule\n"

func loadChain(t *testing.T, dirs ...files) (guard.Verdict, string, string) {
	t.Helper()
	var logs bytes.Buffer
	opts := guard.LoadOptions{Logger: charmlog.New(&logs)}
	for _, d := range dirs {
		opts.ProjectDirs = append(opts.ProjectDirs, writeDir(t, d))
	}
	ps, err := guard.Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	eng, err := guard.NewEngine(ps)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v, fs := eng.Evaluate(map[string]any{"payload": userPayload})
	var parts []string
	for _, f := range fs {
		parts = append(parts, f.Rule+":"+f.Severity)
	}
	return v, strings.Join(parts, ","), logs.String()
}

// A10: an inner folder redefining an outer folder's policy name is ignored
// with the existing warning; the outermost definition wins.
func TestLoadChain_InnerCannotRedefineOuterPolicy(t *testing.T) {
	baseV, baseF, _ := loadChain(t, files{"org.yaml": orgDenyRule})
	v, found, logs := loadChain(t,
		files{"org.yaml": orgDenyRule},
		files{"org-rule.yaml": "name: org-rule\nwhen: \"false\"\neffect: deny\n"},
	)
	if v != baseV || found != baseF {
		t.Fatalf("inner redefinition changed outcome: %v %q, want %v %q", v, found, baseV, baseF)
	}
	if !strings.Contains(found, "org-rule") {
		t.Fatalf("outer policy not effective: %q", found)
	}
	if !strings.Contains(logs, `guard: ignoring project policy "org-rule"`) {
		t.Fatalf("no warning for the ignored inner policy: %q", logs)
	}
}

// Policies add across the chain: an inner folder's new policy applies
// together with the outer folder's.
func TestLoadChain_PoliciesAdd(t *testing.T) {
	_, found, logs := loadChain(t,
		files{"org.yaml": orgDenyRule},
		files{"inner.yaml": "name: inner-rule\nwhen: payload.marker == \"bad\"\neffect: warn\n"},
	)
	if !strings.Contains(found, "org-rule:deny") || !strings.Contains(found, "inner-rule:warn") {
		t.Fatalf("findings = %q, want org-rule and inner-rule", found)
	}
	if logs != "" {
		t.Fatalf("unexpected warnings: %q", logs)
	}
}

// An inner folder's policies.yaml cannot disable an outer folder's policy.
func TestLoadChain_InnerCannotDisableOuterPolicy(t *testing.T) {
	_, found, logs := loadChain(t,
		files{"org.yaml": orgDenyRule},
		files{"policies.yaml": "disable: [org-rule]\n"},
	)
	if !strings.Contains(found, "org-rule:deny") {
		t.Fatalf("outer policy disabled by inner folder: %q", found)
	}
	if !strings.Contains(logs, "ignoring project disable") {
		t.Fatalf("no warning: %q", logs)
	}
}

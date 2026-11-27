package install_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/install"
)

const validManifest = `{"schema":"gym.skill/v1","allowed_tools":["Read","Bash(git:*)"],"data_class":"internal"}`

func wantReason(t *testing.T, err error, reason string) *install.AdmissionError {
	t.Helper()
	var ae *install.AdmissionError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v; want AdmissionError %s", err, reason)
	}
	if ae.Reason != reason {
		t.Fatalf("reason = %s (%v); want %s", ae.Reason, err, reason)
	}
	return ae
}

// Case 1 (manifest half): valid manifest parses; tools sorted.
func TestParseManifest_Valid(t *testing.T) {
	m, err := install.ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.DataClass != "internal" || len(m.AllowedTools) != 2 || m.AllowedTools[0] != "Bash(git:*)" || m.AllowedTools[1] != "Read" {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestParseManifest_EmptyToolsAndLegacyFieldsAllowed(t *testing.T) {
	_, err := install.ParseManifest([]byte(`{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"public",
		"postinstall":"echo hi","shell_hook":"x","allow_postinstall":false,"large_binary":true}`))
	if err != nil {
		t.Fatalf("legacy scan fields must stay legal: %v", err)
	}
}

// Case 2: unknown top-level key.
func TestParseManifest_UnknownKeyRejected(t *testing.T) {
	_, err := install.ParseManifest([]byte(`{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"public","extra":1}`))
	wantReason(t, err, install.ReasonManifestInvalid)
}

// Case 3: operator-only fields.
func TestParseManifest_OperatorOnlyFieldsRejected(t *testing.T) {
	for _, f := range []string{`"digest":"sha256:00"`, `"approval_state":"approved"`} {
		_, err := install.ParseManifest([]byte(`{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"public",` + f + `}`))
		ae := wantReason(t, err, install.ReasonManifestInvalid)
		if !strings.Contains(ae.Detail, "operator") {
			t.Errorf("%s: message %q does not name operator-only", f, ae.Detail)
		}
	}
}

// Case 4: missing data_class, unknown class, wrong schema, missing tools.
func TestParseManifest_RequiredFieldsAndEnums(t *testing.T) {
	for name, body := range map[string]string{
		"missing data_class":  `{"schema":"gym.skill/v1","allowed_tools":[]}`,
		"unknown class":       `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"secret"}`,
		"wrong schema":        `{"schema":"gym.skill/v2","allowed_tools":[],"data_class":"public"}`,
		"missing schema":      `{"allowed_tools":[],"data_class":"public"}`,
		"missing tools":       `{"schema":"gym.skill/v1","data_class":"public"}`,
		"null tools":          `{"schema":"gym.skill/v1","allowed_tools":null,"data_class":"public"}`,
		"tools wrong type":    `{"schema":"gym.skill/v1","allowed_tools":"Read","data_class":"public"}`,
		"class wrong type":    `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":3}`,
		"case-sensitive enum": `{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"Public"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := install.ParseManifest([]byte(body))
			wantReason(t, err, install.ReasonManifestInvalid)
		})
	}
}

// Case 5: malformed JSON, duplicate tools, token with whitespace.
func TestParseManifest_MalformedDuplicateWhitespace(t *testing.T) {
	for name, body := range map[string]string{
		"malformed json":  `{"schema":`,
		"not an object":   `[]`,
		"trailing data":   validManifest + ` {}`,
		"duplicate tools": `{"schema":"gym.skill/v1","allowed_tools":["Read","Read"],"data_class":"public"}`,
		"whitespace":      `{"schema":"gym.skill/v1","allowed_tools":["Bash(git status)"],"data_class":"public"}`,
		"empty token":     `{"schema":"gym.skill/v1","allowed_tools":[""],"data_class":"public"}`,
		"unbalanced":      `{"schema":"gym.skill/v1","allowed_tools":["Bash(git"],"data_class":"public"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := install.ParseManifest([]byte(body))
			wantReason(t, err, install.ReasonManifestInvalid)
		})
	}
}

func TestLoadManifest_AbsentAndPresent(t *testing.T) {
	root := t.TempDir()
	m, present, err := install.LoadManifest(root)
	if m != nil || present || err != nil {
		t.Fatalf("absent: %v %v %v", m, present, err)
	}
	if err := os.WriteFile(filepath.Join(root, "gym.json"), []byte(validManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	m, present, err = install.LoadManifest(root)
	if m == nil || !present || err != nil {
		t.Fatalf("present: %v %v %v", m, present, err)
	}
	// only gym.json is read: manifest.json / skill.json are not admission manifests
	root2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(root2, "manifest.json"), []byte(validManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, present, _ := install.LoadManifest(root2); present {
		t.Fatal("manifest.json treated as admission manifest")
	}
}

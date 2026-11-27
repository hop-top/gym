package install_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hop.top/gym/internal/install"
)

// Case 13 (coverage half) and 15: coverage relation.
func TestToolCovers(t *testing.T) {
	cases := []struct {
		declared, used string
		want           bool
	}{
		{"Read", "Read", true},
		{"Read", "read", false},
		{"Bash(git:*)", "Bash(git:status)", true},
		{"Bash", "Bash(git:status)", true},
		{"Bash(npm:*)", "Bash(git:status)", false},
		{"Bash(git:*)", "Bash", false},
		{"Bash(git:*)", "Bash(git:*)", true},
		{"Bash(git:status)", "Bash(git:status)", true},
		{"Bash(git:status)", "Bash(git:log)", false},
		{"Bash(git*)", "Bash(git:status)", false},
		{"Bash(git:*)", "Bash(gitx:status)", false},
		{"Read", "Write", false},
	}
	for _, c := range cases {
		if got := install.ToolCovers(c.declared, c.used); got != c.want {
			t.Errorf("ToolCovers(%q, %q) = %v; want %v", c.declared, c.used, got, c.want)
		}
	}
}

func TestUndeclaredTools(t *testing.T) {
	got := install.UndeclaredTools([]string{"Read", "Bash(git:*)"}, []string{"Read", "Bash(git:status)", "Write", "Bash(npm:*)"})
	want := []string{"Write", "Bash(npm:*)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("undeclared = %v; want %v", got, want)
	}
	if got := install.UndeclaredTools(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty undeclared = %#v; want non-nil empty", got)
	}
}

// Case 17: allowed-tools as a YAML list is a manifest-invalid error.
func TestFrontmatterTools_ListRejected(t *testing.T) {
	root := t.TempDir()
	skill := "---\nname: s\ndescription: d\nallowed-tools:\n  - Read\n  - Bash\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := install.FrontmatterTools(root)
	wantReason(t, err, install.ReasonManifestInvalid)
}

func TestFrontmatterTools_String(t *testing.T) {
	root := t.TempDir()
	skill := "---\nname: s\ndescription: d\nallowed-tools: Read Bash(git:*)\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := install.FrontmatterTools(root)
	if err != nil || !reflect.DeepEqual(got, []string{"Read", "Bash(git:*)"}) {
		t.Fatalf("tools = %v, %v", got, err)
	}
}

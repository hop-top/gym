package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/install"
)

func TestSplitRef(t *testing.T) {
	dir := t.TempDir()
	hashDir := filepath.Join(dir, "has#hash")
	if err := os.MkdirAll(hashDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ in, src, ref string }{
		{"https://h/o/r.git#v1.2.0", "https://h/o/r.git", "v1.2.0"},
		{"git@h:o/r.git#main", "git@h:o/r.git", "main"},
		{"ssh://git@h/o/r.git#abc", "ssh://git@h/o/r.git", "abc"},
		{"file:///x/r#v1", "file:///x/r", "v1"},
		{dir + "#v2", dir, "v2"},
		{hashDir, hashDir, ""}, // existing directory wins over fragment parsing
		{"https://h/o/r.git", "https://h/o/r.git", ""},
		{"https://h/o/r.git#", "https://h/o/r.git#", ""},
		{"name#frag", "name#frag", ""},
	}
	for _, c := range cases {
		src, ref := install.SplitRef(c.in)
		if src != c.src || ref != c.ref {
			t.Errorf("SplitRef(%q) = %q, %q; want %q, %q", c.in, src, ref, c.src, c.ref)
		}
	}
}

func TestValidRef(t *testing.T) {
	for ref, want := range map[string]bool{
		"v1.2.0": true, "feature/x": true, "main": true, strings.Repeat("a", 40): true,
		"": false, "-x": false, "--upload-pack=x": false, "a b": false, "a..b": false,
		"a\nb": false, "$(x)": false, "x;y": false,
	} {
		if got := install.ValidRef(ref); got != want {
			t.Errorf("ValidRef(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestIsCommitRef(t *testing.T) {
	if !install.IsCommitRef(strings.Repeat("a", 40)) || !install.IsCommitRef(strings.Repeat("F", 64)) {
		t.Fatal("40 and 64 hex are commits")
	}
	for _, s := range []string{"", "abc", strings.Repeat("a", 41), strings.Repeat("g", 40), "v1.2.0"} {
		if install.IsCommitRef(s) {
			t.Errorf("%q is not a commit ref", s)
		}
	}
}

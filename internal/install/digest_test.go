package install_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/gym/internal/install"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustDigest(t *testing.T, root string) string {
	t.Helper()
	d, err := install.Digest(root)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	return d
}

// Case 6: digest independent of creation order and mtime.
func TestDigest_StableUnderCreationOrderAndMtime(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, n := range []string{"a.txt", "sub/b.txt", "c.txt"} {
		writeTree(t, a, map[string]string{n: "x-" + n})
	}
	for _, n := range []string{"c.txt", "sub/b.txt", "a.txt"} {
		writeTree(t, b, map[string]string{n: "x-" + n})
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(b, "a.txt"), old, old); err != nil {
		t.Fatal(err)
	}
	da, db := mustDigest(t, a), mustDigest(t, b)
	if da != db {
		t.Fatalf("digest differs: %s vs %s", da, db)
	}
	if !install.ValidDigest(da) {
		t.Fatalf("digest %q not in sha256:<hex> form", da)
	}
}

// Case 7: digest changes on content byte, rename, exec bit; not on
// non-exec mode change.
func TestDigest_SensitiveToContentRenameExecBit(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"SKILL.md": "one", "run.sh": "echo"})
	base := mustDigest(t, root)

	// content byte
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("onf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustDigest(t, root); got == base {
		t.Fatal("digest unchanged after content change")
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustDigest(t, root); got != base {
		t.Fatal("digest not restored")
	}

	// non-exec mode change: unchanged
	if err := os.Chmod(filepath.Join(root, "run.sh"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mustDigest(t, root); got != base {
		t.Fatal("digest changed on non-exec mode change")
	}

	// exec bit
	if err := os.Chmod(filepath.Join(root, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := mustDigest(t, root); got == base {
		t.Fatal("digest unchanged after exec bit change")
	}
	if err := os.Chmod(filepath.Join(root, "run.sh"), 0o644); err != nil {
		t.Fatal(err)
	}

	// rename
	if err := os.Rename(filepath.Join(root, "run.sh"), filepath.Join(root, "go.sh")); err != nil {
		t.Fatal(err)
	}
	if got := mustDigest(t, root); got == base {
		t.Fatal("digest unchanged after rename")
	}
}

// Case 8: .git ignored; node_modules included.
func TestDigest_GitIgnoredNodeModulesIncluded(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"SKILL.md": "x", "node_modules/m/i.js": "1"})
	base := mustDigest(t, root)

	writeTree(t, root, map[string]string{".git/HEAD": "ref", "sub/.git/config": "c"})
	if got := mustDigest(t, root); got != base {
		t.Fatal(".git content changed the digest")
	}

	writeTree(t, root, map[string]string{"node_modules/m/i.js": "2"})
	if got := mustDigest(t, root); got == base {
		t.Fatal("node_modules change did not change the digest")
	}
}

// Case 9: symlink in tree is rejected.
func TestDigest_SymlinkRejected(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"SKILL.md": "x"})
	if err := os.Symlink("/etc/hosts", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := install.Digest(root)
	var ae *install.AdmissionError
	if !errors.As(err, &ae) || ae.Reason != install.ReasonDigestUnsupportedEntry {
		t.Fatalf("err = %v; want %s", err, install.ReasonDigestUnsupportedEntry)
	}
	if !strings.Contains(err.Error(), "link") {
		t.Errorf("error does not name the entry: %v", err)
	}
}

func TestDigestListing_Format(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"b.txt": "b", "a.txt": "a", "a/z.txt": "z"})
	if err := os.Chmod(filepath.Join(root, "b.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := install.DigestListing(root)
	if err != nil {
		t.Fatal(err)
	}
	// bytewise on relpath: "a.txt" < "a/z.txt" ('.' < '/'), which a
	// directory-order walk would emit the other way round.
	if len(files) != 3 || files[0].Path != "a.txt" || files[1].Path != "a/z.txt" || files[2].Path != "b.txt" {
		t.Fatalf("listing = %+v", files)
	}
	if !strings.HasPrefix(files[0].Line(), "- ") || !strings.HasPrefix(files[2].Line(), "x ") {
		t.Fatalf("lines = %q %q", files[0].Line(), files[2].Line())
	}
}

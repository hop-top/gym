package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic_ReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Fatalf("content = %q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Fatalf("perm = %v", fi.Mode().Perm())
	}
}

func TestWriteFileAtomic_FailureKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory at the target makes the final rename fail.
	bad := filepath.Join(dir, "d")
	if err := os.MkdirAll(filepath.Join(bad, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(bad, []byte("new"), 0o644); err == nil {
		t.Fatal("want error renaming over a non-empty directory")
	}
	if b, _ := os.ReadFile(p); string(b) != "old" {
		t.Fatalf("original changed: %q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Fatalf("temp file left behind after failure: %v", ents)
	}
}

package ide_test

import (
	"os"
	"path/filepath"
	"testing"

	"hop.top/gym/internal/ide"
)

func TestDetectExistingGlobalPaths(t *testing.T) {
	home := t.TempDir()
	mustMkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir(filepath.Join(home, ".claude", "skills"))
	mustMkdir(filepath.Join(home, ".cursor", "skills"))
	mustMkdir(filepath.Join(home, ".gemini", "skills"))

	paths := ide.DetectExistingGlobalPaths(home)
	if len(paths) < 3 {
		t.Fatalf("DetectExistingGlobalPaths() len = %d, want >= 3", len(paths))
	}

	found := map[string]bool{}
	for _, p := range paths {
		found[p.Name] = true
	}

	if !found["claude-global"] || !found["cursor-global"] || !found["gemini-global"] {
		t.Fatalf("expected claude/cursor/gemini paths, got %#v", paths)
	}
}

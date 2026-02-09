package project_test

import (
	"os"
	"path/filepath"
	"testing"

	"hop/skills/internal/project"
)

func TestDetectRootFindsNearestProjectConfig(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, ".skills")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"install_paths":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := project.DetectRoot(nested)
	if !ok {
		t.Fatalf("DetectRoot() ok = false, want true")
	}
	if got != root {
		t.Fatalf("DetectRoot() = %q, want %q", got, root)
	}
}

func TestDetectRootReturnsFalseWhenAbsent(t *testing.T) {
	d := t.TempDir()
	if got, ok := project.DetectRoot(d); ok || got != "" {
		t.Fatalf("DetectRoot() = (%q, %v), want (\"\", false)", got, ok)
	}
}

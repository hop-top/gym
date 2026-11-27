package install_test

import (
	"os"
	"path/filepath"
	"testing"

	"hop.top/gym/internal/install"
)

const largeBinarySize = 30 << 20 // > 25 MiB policy threshold

// stageSkill writes a skill dir with a manifest and, optionally, a
// sparse large binary.
func stageSkill(t *testing.T, manifest string, bigBinary bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gym.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if bigBinary {
		f, err := os.Create(filepath.Join(dir, "blob.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(largeBinarySize); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	return dir
}

func TestScanStaging_IgnoresAuthorAllowances(t *testing.T) {
	dir := stageSkill(t,
		`{"schema":"gym.skill/v1","allowed_tools":[],"data_class":"public","postinstall":"echo hi","shell_hook":"x","allow_postinstall":true,"large_binary":true}`, true)
	got := install.ScanStaging(dir)
	if got.Postinstall != "echo hi" || got.ShellHook != "x" {
		t.Errorf("descriptive fields not read: %+v", got)
	}
	if got.LargestBinaryBytes != largeBinarySize {
		t.Errorf("LargestBinaryBytes = %d; want %d", got.LargestBinaryBytes, largeBinarySize)
	}
}

// Legacy manifest names (no gym.json) still supply the descriptive
// fields.
func TestScanStaging_LegacyManifestNames(t *testing.T) {
	for _, name := range []string{"manifest.json", "skill.json"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name),
			[]byte(`{"postinstall":"echo hi","shell_hook":"x","allow_postinstall":true,"large_binary":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		got := install.ScanStaging(dir)
		if got.Postinstall != "echo hi" || got.ShellHook != "x" {
			t.Errorf("%s: descriptive fields not read: %+v", name, got)
		}
	}
}

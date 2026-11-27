package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"hop.top/gym/internal/cli"
)

// scratchDir returns a fresh temp dir inside the package directory. The
// path-allowlist built-in policy denies installs into system temp
// locations (/tmp, /var, /private/var/folders), where t.TempDir lives, so
// install destinations must be created elsewhere. Removed on cleanup.
func scratchDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	d, err := os.MkdirTemp(wd, ".scratch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// isolate points HOME and every XDG dir at temp dirs so no real
// config, approvals, state, or policies are read or written.
func isolate(t *testing.T) (home string) {
	t.Helper()
	home = scratchDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")
	return home
}

// runGym runs the CLI in-process and returns exit code, stdout, stderr.
func runGym(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	app := cli.NewApp(&out, &errb, cwd)
	code := app.Run(append([]string{"--offline"}, args...))
	return code, out.String(), errb.String()
}

func writeFiles(t *testing.T, root string, files map[string]string) {
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

// writeUserConfig writes the gym user config.yaml with a passthrough
// validator plus extra top-level YAML (e.g. an `admission:` block).
func writeUserConfig(t *testing.T, home string, extra string) {
	t.Helper()
	cfg := "validator:\n  command: [sh, -c, \"true\"]\n  timeout_seconds: 5\n" + extra
	writeFiles(t, filepath.Join(home, ".config", "gym"), map[string]string{"config.yaml": cfg})
}

// admSkill writes <parent>/<name> with SKILL.md (allowed-tools) and
// optionally gym.json, returning the directory.
func admSkill(t *testing.T, parent, name, tools, manifest string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	fm := "---\nname: " + name + "\ndescription: d\n"
	if tools != "" {
		fm += "allowed-tools: " + tools + "\n"
	}
	files := map[string]string{"SKILL.md": fm + "---\nbody\n"}
	if manifest != "" {
		files["gym.json"] = manifest
	}
	writeFiles(t, dir, files)
	return dir
}

func classManifest(class string, tools ...string) string {
	q := ""
	for i, tk := range tools {
		if i > 0 {
			q += ","
		}
		q += `"` + tk + `"`
	}
	return `{"schema":"gym.skill/v1","allowed_tools":[` + q + `],"data_class":"` + class + `"}`
}

func installed(dest, name string) bool {
	_, err := os.Stat(filepath.Join(dest, name, "SKILL.md"))
	return err == nil
}

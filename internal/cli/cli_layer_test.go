package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func loadCLI(t *testing.T, user, project string) (*viper.Viper, string) {
	t.Helper()
	home := isolateInternal(t)
	cwd := scratch(t)
	if user != "" {
		if err := os.WriteFile(filepath.Join(home, ".config", "gym", "cli.yaml"), []byte(user), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if project != "" {
		if err := os.MkdirAll(filepath.Join(cwd, ".gym"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, ".gym", "cli.yaml"), []byte(project), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb bytes.Buffer
	a := NewApp(&out, &errb, cwd)
	vp := viper.New()
	if err := a.initViper(vp); err != nil {
		t.Fatal(err)
	}
	return vp, errb.String()
}

func TestProjectCLI_TighteningKeys(t *testing.T) {
	vp, warns := loadCLI(t, "", "train:\n  no_global: true\nstrict: true\n")
	if !vp.GetBool("train.no_global") || !vp.GetBool("strict") {
		t.Fatal("tightening keys not honoured")
	}
	if strings.Contains(warns, "ignoring") {
		t.Fatalf("warned: %s", warns)
	}
	// loosening values from a project are ignored, never applied
	vp, warns = loadCLI(t, "train:\n  no_global: true\n", "train:\n  no_global: false\nstrict: false\n")
	if !vp.GetBool("train.no_global") {
		t.Fatal("project false overrode the operator's true")
	}
	if vp.GetBool("strict") || strings.Count(warns, "ignoring") != 2 {
		t.Fatalf("project strict: false must be ignored with a warning: %s", warns)
	}
}

func TestProjectCLI_UserWinsOverProject(t *testing.T) {
	vp, _ := loadCLI(t, "learn:\n  agents: [u]\n", "learn:\n  agents: [p]\n")
	if got := vp.GetStringSlice("learn.agents"); len(got) != 1 || got[0] != "u" {
		t.Fatalf("agents = %v, want user's", got)
	}
}

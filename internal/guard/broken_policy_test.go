package guard_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	charmlog "charm.land/log/v2"

	"hop.top/gym/internal/guard"
)

const goodDeny = "when: 'payload.skill == \"x\"'\neffect: deny\notherwise: allow\nseverity: deny\n"

// brokenFiles are policy dir contents that fail to read, parse, validate or
// compile. Each maps file name -> body ("symlink:<target>" makes a dangling
// link, an unreadable file even for root).
var brokenFiles = map[string]map[string]string{
	"malformed-yaml":   {"broken.yaml": "name: [unclosed\n"},
	"invalid-effect":   {"broken.yaml": "when: 'true'\neffect: maybe\n"},
	"missing-when":     {"broken.yaml": "effect: deny\n"},
	"cel-compile":      {"broken.yaml": "when: 'payload.skill =='\neffect: deny\n"},
	"unreadable":       {"broken.yaml": "symlink:/nonexistent/target"},
	"broken-overrides": {"policies.yaml": "disable: [unclosed\n"},
}

func writePolicyDir(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if target, ok := strings.CutPrefix(body, "symlink:"); ok {
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func hasPolicy(eng *guard.Engine, name string) bool {
	for _, p := range eng.Policies() {
		if p.Name == name {
			return true
		}
	}
	return false
}

func capture() (*charmlog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return charmlog.New(&buf), &buf
}

// A broken file in a project/folder policy dir is repository content: it is
// skipped with one warning naming the file and cause, and everything else
// (built-ins, the rest of that dir) still loads and gates.
func TestLoad_BrokenProjectFileIsSkippedWithWarning(t *testing.T) {
	for name, files := range brokenFiles {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writePolicyDir(t, dir, files)
			writePolicyDir(t, dir, map[string]string{"good.yaml": goodDeny})
			log, buf := capture()
			policies, err := guard.Load(guard.LoadOptions{ProjectDir: dir, Logger: log})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			eng, err := guard.NewEngine(policies)
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			if !hasPolicy(eng, "no-sudo") || !hasPolicy(eng, "good") {
				t.Errorf("other policies lost: built-in=%v sibling=%v", hasPolicy(eng, "no-sudo"), hasPolicy(eng, "good"))
			}
			file := filepath.Join(dir, "broken.yaml")
			if _, ok := files["policies.yaml"]; ok {
				file = filepath.Join(dir, "policies.yaml")
			}
			got := 0
			for _, l := range strings.Split(buf.String(), "\n") {
				if strings.Contains(l, file) {
					got++
				}
			}
			if got != 1 {
				t.Errorf("want exactly one warning line naming %s, got %d:\n%s", file, got, buf.String())
			}
			verdict, _ := eng.Evaluate(map[string]any{"payload": map[string]any{"postinstall": "sudo rm -rf /"}})
			if verdict != guard.VerdictDeny {
				t.Errorf("built-in no-sudo no longer denies: %s", verdict)
			}
		})
	}
}

// The user dir is the operator's own configuration: a broken file there
// stays a hard error (at Load, or at engine compile for a bad expression).
func TestLoad_BrokenUserFileStaysHardError(t *testing.T) {
	for name, files := range brokenFiles {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writePolicyDir(t, dir, files)
			policies, err := guard.Load(guard.LoadOptions{UserDir: dir})
			if err == nil {
				_, err = guard.NewEngine(policies)
			}
			if err == nil {
				t.Fatal("broken user policy file was tolerated")
			}
		})
	}
}

// One folder's broken file does not stop the other folders on the chain.
func TestLoad_BrokenFolderDoesNotAffectOtherFolders(t *testing.T) {
	root := t.TempDir()
	outer, inner := filepath.Join(root, "outer"), filepath.Join(root, "inner")
	writePolicyDir(t, outer, map[string]string{"broken.yaml": "name: [unclosed\n", "outer-ok.yaml": goodDeny})
	writePolicyDir(t, inner, map[string]string{"inner-ok.yaml": goodDeny})
	log, _ := capture()
	policies, err := guard.Load(guard.LoadOptions{ProjectDirs: []string{outer, inner}, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := guard.NewEngine(policies)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"outer-ok", "inner-ok", "no-sudo"} {
		if !hasPolicy(eng, n) {
			t.Errorf("policy %s missing", n)
		}
	}
}

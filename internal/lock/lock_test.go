package lock_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/lock"
)

const d1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
const d2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"

func entry(name, digest string) lock.Entry {
	return lock.Entry{Name: name, Kind: lock.KindRegistry, Source: "https://h.example/" + name + ".git",
		Version: "1.0.0", Digest: digest, Registry: "https://r.example/skills.json"}
}

// B1: two insertion orders, and a write after a re-read, are byte-identical.
func TestB1MarshalDeterministic(t *testing.T) {
	a := lock.New(lock.ScopeProject)
	a.Set(entry("zeta", d1))
	a.Set(entry("alpha", d2))
	a.Set(entry("mid", d1))
	b := lock.New(lock.ScopeProject)
	b.Set(entry("mid", d1))
	b.Set(entry("zeta", d1))
	b.Set(entry("alpha", d2))

	ba, err := a.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	bb, _ := b.Marshal()
	if !bytes.Equal(ba, bb) {
		t.Fatalf("insertion order changed the bytes:\n%s\n%s", ba, bb)
	}
	if !bytes.HasSuffix(ba, []byte("}\n")) || bytes.Contains(ba, []byte("\t")) {
		t.Fatalf("want 2-space indent and trailing newline:\n%s", ba)
	}
	if i, j := bytes.Index(ba, []byte(`"alpha"`)), bytes.Index(ba, []byte(`"zeta"`)); i > j {
		t.Fatalf("entries not sorted by name:\n%s", ba)
	}

	path := filepath.Join(t.TempDir(), ".gym", "lock.json")
	if err := lock.Save(path, a); err != nil {
		t.Fatal(err)
	}
	back, err := lock.Load(path, lock.ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Save(path, back); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(path)
	if !bytes.Equal(disk, ba) {
		t.Fatalf("write after re-read differs:\n%s\n%s", disk, ba)
	}
}

func TestMarshalEmptyAndFieldOrder(t *testing.T) {
	empty, _ := lock.New(lock.ScopeGlobal).Marshal()
	if !strings.Contains(string(empty), `"skills": []`) {
		t.Fatalf("empty lock must list skills as []:\n%s", empty)
	}
	f := lock.New(lock.ScopeProject)
	f.Set(lock.Entry{Name: "n", Kind: lock.KindGit, Source: "s", Requested: "r", Version: "v",
		Commit: strings.Repeat("a", 40), Path: "p", Registry: "g", Digest: d1})
	out := string(mustMarshal(t, f))
	last := -1
	for _, k := range []string{`"name"`, `"kind"`, `"source"`, `"requested"`, `"version"`, `"commit"`, `"path"`, `"registry"`, `"digest"`} {
		i := strings.Index(out, k)
		if i <= last {
			t.Fatalf("field %s out of order:\n%s", k, out)
		}
		last = i
	}
}

func mustMarshal(t *testing.T, f *lock.File) []byte {
	t.Helper()
	b, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// B2: unknown field, bad schema, scope vs location, and the other strictness rules.
func TestB2ParseStrict(t *testing.T) {
	good := `{"schema":"gym.lock/v1","digest_scheme":"gym.digest/v1","scope":"project","skills":[` +
		`{"name":"lint","kind":"registry","source":"https://h/x.git","version":"1","digest":"` + d1 + `"}]}`
	if _, err := lock.Parse([]byte(good), "project", "p"); err != nil {
		t.Fatalf("good lock rejected: %v", err)
	}
	cases := map[string]struct{ body, scope string }{
		"unknown top field":   {strings.Replace(good, `"scope"`, `"extra":1,"scope"`, 1), "project"},
		"unknown entry field": {strings.Replace(good, `"kind"`, `"evil":"x","kind"`, 1), "project"},
		"bad schema":          {strings.Replace(good, "gym.lock/v1", "gym.lock/v2", 1), "project"},
		"bad digest scheme":   {strings.Replace(good, "gym.digest/v1", "gym.digest/v2", 1), "project"},
		"scope vs location":   {good, "global"},
		"trailing data":       {good + "{}", "project"},
		"not json":            {"nope", "project"},
		"bad kind":            {strings.Replace(good, `"registry"`, `"ftp"`, 1), "project"},
		"bad digest":          {strings.Replace(good, d1, "sha256:zz", 1), "project"},
		"name traversal":      {strings.Replace(good, `"lint"`, `"../x"`, 1), "project"},
		"path traversal":      {strings.Replace(good, `"version"`, `"path":"../../etc","version"`, 1), "project"},
		"absolute path":       {strings.Replace(good, `"version"`, `"path":"/etc","version"`, 1), "project"},
		"bad commit":          {strings.Replace(good, `"version"`, `"commit":"abc","version"`, 1), "project"},
		"duplicate name":      {strings.Replace(good, `]}`, `,{"name":"lint","kind":"dir","source":"s","version":"1","digest":"`+d1+`"}]}`, 1), "project"},
	}
	for name, c := range cases {
		_, err := lock.Parse([]byte(c.body), c.scope, "p")
		var le *lock.Error
		if !errors.As(err, &le) || le.Reason != lock.ReasonInvalid {
			t.Errorf("%s: want lock-invalid, got %v", name, err)
		}
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := lock.Load(filepath.Join(t.TempDir(), "nope.json"), "project")
	if !lock.IsMissing(err) || !strings.Contains(err.Error(), "lock-missing: ") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
	f, err := lock.LoadOrNew(filepath.Join(t.TempDir(), "nope.json"), "global")
	if err != nil || f.Scope != "global" || len(f.Skills) != 0 {
		t.Fatalf("LoadOrNew: %v %v", f, err)
	}
}

func TestSetRemoveFind(t *testing.T) {
	f := lock.New("project")
	f.Set(entry("a", d1))
	f.Set(entry("a", d2))
	if len(f.Skills) != 1 {
		t.Fatalf("Set must replace: %v", f.Skills)
	}
	if e, ok := f.Find("a"); !ok || e.Digest != d2 {
		t.Fatalf("Find: %v %v", e, ok)
	}
	if !f.Remove("a") || f.Remove("a") {
		t.Fatal("Remove must report once")
	}
}

func TestPaths(t *testing.T) {
	if got := lock.ProjectPath("/r"); got != filepath.Join("/r", ".gym", "lock.json") {
		t.Fatal(got)
	}
	if got := lock.GlobalPath("/c/gym"); got != filepath.Join("/c/gym", "lock.json") {
		t.Fatal(got)
	}
}

func TestSourceForLock(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	inside := filepath.Join(root, "mirror", "lint")

	if s, ok := lock.SourceForLock(root, inside); !ok || s != "mirror/lint" {
		t.Fatalf("inside: %q %v", s, ok)
	}
	if s, ok := lock.SourceForLock(root, "file://"+inside); !ok || s != "mirror/lint" {
		t.Fatalf("file://: %q %v", s, ok)
	}
	if s, ok := lock.SourceForLock(root, outside); ok || s != outside {
		t.Fatalf("outside must stay absolute and not portable: %q %v", s, ok)
	}
	for _, u := range []string{"https://h/r.git", "git@h:o/r.git", "ssh://git@h/o/r"} {
		if s, ok := lock.SourceForLock(root, u); !ok || s != u {
			t.Fatalf("network source %q changed: %q %v", u, s, ok)
		}
	}
}

// B12: a lock that lives in a cloned-repo-shaped folder cannot point the
// installer at files outside it.
func TestB12ResolveSourceEscapes(t *testing.T) {
	root := t.TempDir()
	secret := t.TempDir()
	if err := os.WriteFile(filepath.Join(secret, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"absolute dir":      secret,
		"absolute file URL": "file://" + secret,
		"dotdot":            "../" + filepath.Base(secret),
		"deep dotdot":       "a/../../x",
		"symlink out":       "link",
	} {
		got, err := lock.ResolveSource(root, lock.ScopeProject, src)
		var le *lock.Error
		if !errors.As(err, &le) || le.Reason != lock.ReasonSourceEscapes || got != "" {
			t.Errorf("%s: want lock-source-escapes, got %q %v", name, got, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "mirror", "lint"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := lock.ResolveSource(root, lock.ScopeProject, "mirror/lint")
	if err != nil || got != filepath.Join(root, "mirror", "lint") {
		t.Fatalf("relative inside: %q %v", got, err)
	}
	if got, err := lock.ResolveSource(root, lock.ScopeProject, "https://h/r.git"); err != nil || got != "https://h/r.git" {
		t.Fatalf("network: %q %v", got, err)
	}
	// The global lock is the operator's own.
	if got, err := lock.ResolveSource(root, lock.ScopeGlobal, secret); err != nil || got != secret {
		t.Fatalf("global absolute: %q %v", got, err)
	}
}

func TestEnsureGitignoreOnce(t *testing.T) {
	root := t.TempDir()
	if err := lock.EnsureGitignore(root); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ".gym", ".gitignore")
	b, _ := os.ReadFile(p)
	if string(b) != "installed.json\nskills/\n" {
		t.Fatalf("got %q", b)
	}
	if err := os.WriteFile(p, []byte("custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := lock.EnsureGitignore(root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "custom\n" {
		t.Fatalf("existing .gitignore overwritten: %q", b)
	}
}

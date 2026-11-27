package cli_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/gym/internal/cli"
)

// runGymOpen is runGym without the leading --offline: the network policy is
// whatever the test's configuration says.
func runGymOpen(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	app := cli.NewApp(&out, &errb, cwd)
	code := app.Run(args)
	return code, out.String(), errb.String()
}

// skillFiles is a minimal skill tree.
func skillFiles(name, tag string) map[string]string {
	return map[string]string{
		"SKILL.md":       "---\nname: " + name + "\ndescription: d\n---\nbody " + tag + "\n",
		"scripts/run.sh": "#!/bin/sh\necho hi\n",
	}
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// makeTarGz writes dir/name holding files under prefix ("" for the root).
func makeTarGz(t *testing.T, dir, name, prefix string, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range sortedKeys(files) {
		mode := int64(0o644)
		if strings.HasSuffix(f, ".sh") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: prefix + f, Mode: mode, Size: int64(len(files[f])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[f])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return writeBlob(t, dir, name, buf.Bytes())
}

func makeZip(t *testing.T, dir, name, prefix string, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range sortedKeys(files) {
		h := &zip.FileHeader{Name: prefix + f, Method: zip.Deflate}
		if strings.HasSuffix(f, ".sh") {
			h.SetMode(0o755)
		} else {
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[f])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeBlob(t, dir, name, buf.Bytes())
}

// makeSymlinkTarGz is a hostile archive: a skill plus a symlink entry.
func makeSymlinkTarGz(t *testing.T, dir, name string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := skillFiles("lint", "x")["SKILL.md"]
	_ = tw.WriteHeader(&tar.Header{Name: "SKILL.md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(body))
	_ = tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.Close()
	_ = gz.Close()
	return writeBlob(t, dir, name, buf.Bytes())
}

func writeBlob(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeGitOnPath puts a recording `git` first on PATH: `ls-remote` goes to
// the real git, everything else is logged and succeeds without doing
// anything. It returns the log path.
func fakeGitOnPath(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "git.log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$*\" in *ls-remote*) exec " + real + " \"$@\";; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	_ = os.WriteFile(cfg, nil, 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return log
}

// gitNetworkCalls are the logged git invocations other than the local
// `ls-remote --get-url` lookup.
func gitNetworkCalls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" && !strings.Contains(l, "ls-remote --get-url") {
			out = append(out, l)
		}
	}
	return out
}

// loopServer is a loopback HTTP server that counts requests; handler may be nil.
type loopServer struct {
	*httptest.Server
	hits atomic.Int32
	seen atomic.Value // last Authorization header
}

func newLoopServer(t *testing.T, h http.HandlerFunc) *loopServer {
	t.Helper()
	ls := &loopServer{}
	ls.seen.Store("")
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.hits.Add(1)
		ls.seen.Store(r.Header.Get("Authorization"))
		if h != nil {
			h(w, r)
		}
	}))
	t.Cleanup(ls.Close)
	return ls
}

func (l *loopServer) host(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(l.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// forbiddenServer fails the test if anything reaches it.
func forbiddenServer(t *testing.T) *loopServer {
	t.Helper()
	return newLoopServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("forbidden server was contacted: %s", r.URL)
		w.WriteHeader(http.StatusTeapot)
	})
}

// fakeSecret is a unique, obviously fake secret built at run time.
func fakeSecret(t *testing.T, label string) string {
	t.Helper()
	return fmt.Sprintf("canary-%s-%d", label, time.Now().UnixNano())
}

// walkContains reports every regular file under roots whose bytes contain needle.
func walkContains(t *testing.T, needle string, roots ...string) []string {
	t.Helper()
	var hits []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			if b, rerr := os.ReadFile(p); rerr == nil && bytes.Contains(b, []byte(needle)) {
				hits = append(hits, p)
			}
			return nil
		})
	}
	return hits
}

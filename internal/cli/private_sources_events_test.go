package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/bus"

	kitupgrade "hop.top/kit/go/core/upgrade"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/netpolicy"
	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/xdg"
)

// internalConfig rewrites the isolated user config with the validator
// passthrough plus extra YAML.
func internalConfig(t *testing.T, home, extra string) {
	t.Helper()
	body := "validator:\n  command: [sh, -c, \"true\"]\n  timeout_seconds: 5\n" + extra
	if err := os.WriteFile(filepath.Join(home, ".config", "gym", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// restoreNetworkPolicy keeps a test that drives the App directly from
// leaving its policy behind.
func restoreNetworkPolicy(t *testing.T) {
	t.Helper()
	t.Cleanup(netpolicy.Set(netpolicy.Current()))
}

// network-denied during an install is published as install.rejected, for a
// git source and an archive URL, and no request event precedes it.
func TestNetworkDeniedPublishesInstallRejected(t *testing.T) {
	restoreNetworkPolicy(t)
	home := isolateInternal(t)
	internalConfig(t, home, "network:\n  mode: allowlist\n  allow_hosts: [registry.corp.example]\n")
	ba := newBusApp(t, home, true)

	for _, src := range []string{"https://git.example/o/r.git", "https://files.example/lint.tar.gz"} {
		err := ba.learn(t, src, scratch(t))
		var ne *netpolicy.Error
		if err == nil || !asNetworkError(err, &ne) {
			t.Fatalf("%s: err = %v", src, err)
		}
	}
	rej, req := ba.snapshot()
	if len(rej) != 2 || rej[0].Reason != "network-denied" || rej[1].Reason != "network-denied" || rej[0].RequestID == "" {
		t.Fatalf("rejected = %+v", rej)
	}
	if len(req) != 0 {
		t.Fatalf("a request event preceded the denial: %+v", req)
	}
}

func asNetworkError(err error, target **netpolicy.Error) bool { return errors.As(err, target) }

// D13 (events): require_pin remote without a pin publishes digest-required.
func TestD13RequirePinPublishesDigestRequired(t *testing.T) {
	home := isolateInternal(t)
	internalConfig(t, home, "admission:\n  require_pin: remote\n")
	ba := newBusApp(t, home, true)
	src := skillTree(t, "lint", "")
	dirs, _ := xdg.AppDirs(config.AppName)
	if err := registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{{Name: "lint", Version: "1.0.0", SourceURL: src}}}); err != nil {
		t.Fatal(err)
	}
	if err := ba.learn(t, "lint", scratch(t)); err == nil {
		t.Fatal("unpinned remote install accepted")
	}
	rej, _ := ba.snapshot()
	if len(rej) != 1 || rej[0].Reason != "digest-required" {
		t.Fatalf("rejected = %+v", rej)
	}
}

// source_host rides on the install-requested event: the host of a remote
// source, empty for a local one.
func TestInstallRequestedCarriesSourceHost(t *testing.T) {
	if got := sourceHostOf("https://files.corp.example:8443/x.zip"); got != "files.corp.example" {
		t.Fatalf("host = %q", got)
	}
	for _, local := range []string{"/srv/x.zip", "file:///srv/x.zip", "./x"} {
		if got := sourceHostOf(local); got != "" {
			t.Fatalf("%s: host = %q", local, got)
		}
	}
	if got := sourceHostOf("git@github.com:o/r.git"); got != "github.com" {
		t.Fatalf("scp host = %q", got)
	}

	home := isolateInternal(t)
	ba := newBusApp(t, home, true)
	if err := ba.learn(t, skillTree(t, "lint", ""), scratch(t)); err != nil {
		t.Fatal(err)
	}
	_, req := ba.snapshot()
	if len(req) != 1 || req[0].SourceHost != "" || req[0].SourceKind != "dir" {
		t.Fatalf("requested = %+v", req)
	}
}

// D4 (events): a registry install through an authenticated repository,
// with the token also carried in the source URL, never puts a token on the
// bus, on stderr, or on stdout.
func TestD4CanaryNeverReachesTheBus(t *testing.T) {
	restoreNetworkPolicy(t)
	home := isolateInternal(t)
	tok := fmt.Sprintf("canary-bus-%d", time.Now().UnixNano())
	qtok := fmt.Sprintf("canary-q-%d", time.Now().UnixNano())
	t.Setenv("GYM_CANARY_TOKEN", tok)

	// an archive the server hands out only to the bearer
	tmp := scratch(t)
	arch := filepath.Join(tmp, "lint.tar.gz")
	if err := writeSkillTarGz(arch, "lint"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(arch)
	var failing atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(500)
			fmt.Fprintf(w, "echo %q", r.Header.Get("Authorization"))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+tok {
			w.WriteHeader(401)
			fmt.Fprintf(w, "got %q", r.Header.Get("Authorization"))
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	internalConfig(t, home, "repositories:\n  - name: corp\n    url: "+srv.URL+"/skills.json\n    auth: {type: bearer, token_env: GYM_CANARY_TOKEN}\n")

	ba := newBusApp(t, home, true)
	var mu sync.Mutex
	var seen []string
	ba.app.bus.Subscribe("#", func(_ context.Context, e bus.Event) error {
		raw, _ := json.Marshal(e.Payload)
		mu.Lock()
		seen = append(seen, string(e.Topic)+" "+string(raw))
		mu.Unlock()
		return nil
	})
	dirs, _ := xdg.AppDirs(config.AppName)
	if err := registry.SaveIndex(dirs.DataHome, registry.Index{Skills: []registry.SkillEntry{{
		Name: "lint", Version: "1.0.0", OriginURL: srv.URL + "/skills.json",
		SourceURL: srv.URL + "/lint.tar.gz?token=" + qtok,
	}}}); err != nil {
		t.Fatal(err)
	}
	dest := scratch(t)
	if err := ba.learn(t, "lint", dest); err != nil {
		t.Fatalf("authenticated archive install: %v", err)
	}
	if !installedAt(dest, "lint") {
		t.Fatal("not installed")
	}
	// a failing run
	failing.Store(true)
	_ = ba.learn(t, "lint", scratch(t))

	mu.Lock()
	all := strings.Join(seen, "\n")
	mu.Unlock()
	stdout := ba.app.Stdout.(*bytes.Buffer).String()
	for name, s := range map[string]string{"bus": all, "stderr": ba.stderr.String(), "stdout": stdout} {
		if strings.Contains(s, tok) || strings.Contains(s, qtok) {
			t.Errorf("a token reached %s:\n%s", name, s)
		}
	}
	if !strings.Contains(all, "token=REDACTED") {
		t.Errorf("the source URL was not redacted on the bus:\n%s", all)
	}
	for _, hit := range []string{tok, qtok} {
		_ = filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
			// The cached registry index is the publisher's own document, which
			// this test seeded with the token in a source_url; gym needs it
			// verbatim to fetch. Everything gym itself writes is checked.
			if err == nil && d.Type().IsRegular() && d.Name() != "registry-index.json" {
				if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(hit)) {
					t.Errorf("token written to %s", p)
				}
			}
			return nil
		})
	}
}

func installedAt(dest, name string) bool {
	_, err := os.Stat(filepath.Join(dest, name, "SKILL.md"))
	return err == nil
}

// D10 (notifier): the kit upgrade notifier runs only when the network is
// open: not under allowlist, not under offline (config or flag).
func TestD10NotifierRunsOnlyWhenOpen(t *testing.T) {
	restoreNetworkPolicy(t)
	var calls int
	oldNotify, oldChecker := notifyUpgrade, newKitChecker
	notifyUpgrade = func(context.Context, *kitupgrade.Checker, interface{ Write(p []byte) (int, error) }) { calls++ }
	newKitChecker = func(string) *kitupgrade.Checker { return nil }
	defer func() { notifyUpgrade, newKitChecker = oldNotify, oldChecker }()

	run := func(extra string, args ...string) int {
		home := isolateInternal(t)
		internalConfig(t, home, extra)
		calls = 0
		var out, errb bytes.Buffer
		if code := NewApp(&out, &errb, home).Run(args); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, errb.String())
		}
		return calls
	}
	if n := run("", "paths"); n != 1 {
		t.Fatalf("open: notifier ran %d times, want 1 (the control)", n)
	}
	if n := run("network:\n  mode: allowlist\n  allow_hosts: [x.example]\n", "paths"); n != 0 {
		t.Fatalf("allowlist: notifier ran %d times", n)
	}
	if n := run("network:\n  mode: offline\n", "paths"); n != 0 {
		t.Fatalf("offline config: notifier ran %d times", n)
	}
	if n := run("", "--offline", "paths"); n != 0 {
		t.Fatalf("--offline: notifier ran %d times", n)
	}
}

// writeSkillTarGz writes a one-skill archive with a top-level directory.
func writeSkillTarGz(path, name string) error {
	var buf bytes.Buffer
	if err := buildSkillTarGz(&buf, name); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func buildSkillTarGz(w io.Writer, name string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	body := "---\nname: " + name + "\ndescription: d\n---\nbody\n"
	if err := tw.WriteHeader(&tar.Header{Name: name + "/SKILL.md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// Kit installs a guard under http.DefaultTransport that acts on an
// offline-marked context; gym replaces kit's PersistentPreRunE, so gym
// stamps the marker itself, from the flag or from the configured mode.
func TestKitOfflineMarkerIsStamped(t *testing.T) {
	restoreNetworkPolicy(t)
	home := isolateInternal(t)
	for _, c := range []struct {
		name, cfg string
		flag      bool
		want      bool
	}{
		{"open", "", false, false},
		{"flag", "", true, true},
		{"config offline", "network:\n  mode: offline\n", false, true},
		{"allowlist", "network:\n  mode: allowlist\n", false, false},
	} {
		internalConfig(t, home, c.cfg)
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().Bool("offline", false, "")
		if c.flag {
			_ = cmd.Flags().Set("offline", "true")
		}
		cmd.SetContext(context.Background())
		NewApp(&bytes.Buffer{}, &bytes.Buffer{}, home).initNetworkPolicy(cmd)
		if got := kitcli.IsOffline(cmd.Context()); got != c.want {
			t.Errorf("%s: kit offline marker = %v, want %v", c.name, got, c.want)
		}
	}
}

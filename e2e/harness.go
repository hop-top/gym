//go:build cassettes

// Package e2e is the cross-tool end-to-end test harness.
//
// Each scenario boots an in-process gym App against a memory bus, wires
// the rsx-backed judge subscriber and the CEL-backed guard subscriber,
// and either replays YAML cassettes (default) or records new ones when
// XRR_RECORD=1 is set. See README.md for the recording flow.
//
// Build tag: only compiled when `-tags cassettes` is passed. The
// regular `go test ./...` build never sees these files.
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/ai/toolspec"
	xrr "hop.top/xrr"
	xrrexec "hop.top/xrr/adapters/exec"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/judge"
)

// minimalSelfSpec returns a tiny gym ToolSpec good enough for the
// cheat-bridge lookup. The bridge only needs Name + Commands to
// answer "warmup" → "gym warmup".
func minimalSelfSpec() *toolspec.ToolSpec {
	return &toolspec.ToolSpec{
		Name:          "gym",
		SchemaVersion: "1.0",
		Commands: []toolspec.Command{
			{Name: "warmup", Safety: &toolspec.Safety{Level: toolspec.SafetyLevelSafe}},
			{Name: "learn", Safety: &toolspec.Safety{Level: toolspec.SafetyLevelCaution}},
			{Name: "train", Safety: &toolspec.Safety{Level: toolspec.SafetyLevelCaution}},
		},
	}
}

// harness bundles the moving pieces every scenario shares.
type harness struct {
	t           *testing.T
	bus         *eventbus.Bus
	judge       *judge.Judge
	guard       *guard.Guard
	session     *xrr.FileSession
	cassetteDir string
}

// boot builds the harness for a named scenario. The scenario name is
// the relative directory under cassettes/.
//
// Mode: replay by default; XRR_RECORD=1 flips to record. When recording
// is requested but the rsx binary isn't on PATH, the test calls
// t.Skipf — never fail the suite for a missing external dep.
func boot(t *testing.T, scenario string) *harness {
	t.Helper()
	t.Setenv("GYM_BUS_URL", "")
	t.Setenv("GYM_BUS_PATH", "")

	mode := xrr.ModeReplay
	if os.Getenv("XRR_RECORD") == "1" {
		mode = xrr.ModeRecord
		if _, err := lookPath("rsx"); err != nil {
			t.Skipf("XRR_RECORD=1 but rsx not on PATH: %v", err)
		}
	}
	dir := filepath.Join("cassettes", scenario)
	if mode == xrr.ModeRecord {
		_ = os.MkdirAll(dir, 0o755)
	}
	session := xrr.NewSession(mode, xrr.NewFileCassette(dir))

	b, err := eventbus.New(eventbus.Config{Adapter: eventbus.AdapterMemory})
	if err != nil {
		t.Fatalf("eventbus.New: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })

	h := &harness{
		t:           t,
		bus:         b,
		session:     session,
		cassetteDir: dir,
	}
	return h
}

// startJudge wires the rsx-backed coach judging subscriber against the
// harness's bus, with the rsx subprocess driver replaced by an
// xrr-cassette-backed CommandRunner.
func (h *harness) startJudge() {
	h.t.Helper()
	runner := h.cassetteRunner()
	resolver := judge.NewResolver(judge.WithRunner(runner))
	cache := judge.NewCacheAt(h.t.TempDir())
	h.judge = judge.New(h.bus, nil,
		judge.WithResolver(resolver),
		judge.WithCache(cache),
	)
	if err := h.judge.Start(context.Background()); err != nil {
		h.t.Fatalf("judge.Start: %v", err)
	}
	h.t.Cleanup(h.judge.Stop)
}

// startGuard wires the CEL guard subscriber with the baked-in policy
// set (no user/project overrides). Build the engine once; tests
// override the activation map to drive specific verdicts.
func (h *harness) startGuard() {
	h.t.Helper()
	policies, err := guard.Load(guard.LoadOptions{})
	if err != nil {
		h.t.Fatalf("guard.Load: %v", err)
	}
	eng, err := guard.NewEngine(policies)
	if err != nil {
		h.t.Fatalf("guard.NewEngine: %v", err)
	}
	g, err := guard.New(h.bus, guard.WithEngine(eng))
	if err != nil {
		h.t.Fatalf("guard.New: %v", err)
	}
	if err := g.Start(context.Background()); err != nil {
		h.t.Fatalf("guard.Start: %v", err)
	}
	h.guard = g
	h.t.Cleanup(g.Stop)
}

// cassetteRunner returns a judge.CommandRunner that records or replays
// rsx subprocess calls via xrr. In replay mode a cassette miss
// surfaces as the standard xrr.ErrCassetteMiss — judge then fails-open
// (per its contract) and the test asserts on that.
func (h *harness) cassetteRunner() judge.CommandRunner {
	adapter := xrrexec.NewAdapter()
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		argv := append([]string{name}, args...)
		req := &xrrexec.Request{Argv: argv, Stdin: ""}
		do := func() (xrr.Response, error) {
			// Real subprocess execution path — only used during
			// XRR_RECORD=1. Reuses os/exec via the production
			// runner so recordings match real behaviour.
			out, err := defaultRunRSX(ctx, name, args...)
			resp := &xrrexec.Response{Stdout: string(out)}
			if err != nil {
				resp.ExitCode = 1
				return resp, err
			}
			return resp, nil
		}
		resp, err := h.session.Record(ctx, adapter, req, do)
		if err != nil {
			return nil, err
		}
		// Replay returns RawResponse{Payload: map}; record returns
		// the typed *Response. Handle both.
		switch r := resp.(type) {
		case *xrrexec.Response:
			return []byte(r.Stdout), nil
		case *xrr.RawResponse:
			s, _ := r.Payload["stdout"].(string)
			return []byte(s), nil
		default:
			return nil, fmt.Errorf("e2e: unexpected response %T", resp)
		}
	}
}

// defaultRunRSX is the recording-mode shell-out. Only invoked when
// XRR_RECORD=1 actually has a rsx binary on PATH. Replay never enters
// this code path.
func defaultRunRSX(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := execCommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// execCommandContext is split out so tests can stub it. The default
// impl just shells out via os/exec.
var execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// lookPath wraps exec.LookPath so the import doesn't leak into every
// scenario file.
func lookPath(bin string) (string, error) {
	return exec.LookPath(bin)
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/serve"
)

type serveOptions struct {
	Host         string
	Port         int
	Daemon       bool
	PidFile      string
	LogFile      string
	Name         string
	Update       bool
	RefreshEvery time.Duration
	Stop         bool
	Status       bool
}

func sanitizeForFilename(s string) string {
	if s == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := b.String()
	out = strings.Trim(out, "_")
	if out == "" {
		return "default"
	}
	return out
}

func defaultPidFile(stateHome, host string, port int) string {
	id := sanitizeForFilename(host) + "-" + strconv.Itoa(port)
	return filepath.Join(stateHome, "gym-serve-"+id+".pid")
}

func defaultLogFile(stateHome, host string, port int) string {
	id := sanitizeForFilename(host) + "-" + strconv.Itoa(port)
	return filepath.Join(stateHome, "gym-serve-"+id+".log")
}

func writePID(path string, pid int) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

func readPID(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (a *App) updateIndexOnce(ctx context.Context, rt runtime) error {
	oldIdx, err := registry.LoadIndex(rt.Dirs.DataHome)
	if err != nil {
		return err
	}
	newIdx, _, err := registry.Refresh(ctx, a.registryConfigFrom(rt.Config), registry.HTTPFetcher{}, oldIdx)
	if err != nil {
		return err
	}
	return registry.SaveIndex(rt.Dirs.DataHome, newIdx)
}

func (a *App) runServe(opts serveOptions) error {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.Port == 0 {
		opts.Port = 4873
	}
	if opts.Name == "" {
		opts.Name = "gym-registry"
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return fmt.Errorf("invalid --port %d", opts.Port)
	}
	if opts.RefreshEvery > 0 {
		opts.Update = true
	}
	if (opts.Stop && opts.Status) || (opts.Daemon && opts.Stop) || (opts.Daemon && opts.Status) {
		return fmt.Errorf("--daemon, --stop, and --status are mutually exclusive")
	}

	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}

	if opts.PidFile == "" {
		opts.PidFile = defaultPidFile(rt.Dirs.StateHome, opts.Host, opts.Port)
	}
	if opts.LogFile == "" {
		opts.LogFile = defaultLogFile(rt.Dirs.StateHome, opts.Host, opts.Port)
	}

	if opts.Stop {
		msg, err := stopByPIDFile(opts.PidFile)
		if err != nil {
			return err
		}
		a.printlnf("%s", msg)
		return nil
	}
	if opts.Status {
		msg, err := statusByPIDFile(opts.PidFile)
		if err != nil {
			return err
		}
		a.printlnf("%s", msg)
		return nil
	}

	if opts.Daemon {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		childArgs := []string{"serve", "--host", opts.Host, "--port", strconv.Itoa(opts.Port), "--pidfile", opts.PidFile, "--logfile", opts.LogFile, "--name", opts.Name}
		if opts.Update {
			childArgs = append(childArgs, "--update")
		}
		if opts.RefreshEvery > 0 {
			childArgs = append(childArgs, "--refresh", opts.RefreshEvery.String())
		}

		pid, err := daemonize(exe, childArgs, opts.LogFile)
		if err != nil {
			return err
		}
		a.printlnf("started daemon pid %d", pid)
		a.printlnf("pidfile: %s", opts.PidFile)
		a.printlnf("logfile: %s", opts.LogFile)
		return nil
	}

	ctx := context.Background()
	if opts.Update {
		if err := a.updateIndexOnce(ctx, rt); err != nil {
			return err
		}
	}

	if opts.RefreshEvery > 0 {
		ticker := time.NewTicker(opts.RefreshEvery)
		defer ticker.Stop()
		go func() {
			for range ticker.C {
				if err := a.updateIndexOnce(context.Background(), rt); err != nil {
					a.eprintf("serve refresh error: %v", err)
				}
			}
		}()
	}

	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	if err := writePID(opts.PidFile, os.Getpid()); err != nil {
		return err
	}
	defer func() { _ = os.Remove(opts.PidFile) }()

	s := &http.Server{Handler: serve.Handler(serve.Options{DataHome: rt.Dirs.DataHome, Name: opts.Name})}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(ln) }()

	bound := ln.Addr().String()
	if strings.HasPrefix(bound, ":") {
		bound = "127.0.0.1" + bound
	}
	a.printlnf("serving on http://%s/skills.json", bound)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, terminationSignals()...)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		return nil
	}
}

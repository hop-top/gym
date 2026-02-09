package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

func (a *App) cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bindHost := fs.String("host", "127.0.0.1", "bind host")
	bindPort := fs.Int("port", 4873, "bind port")
	daemon := fs.Bool("daemon", false, "run in background")
	pidFile := fs.String("pidfile", "", "pid file path")
	logFile := fs.String("logfile", "", "log file path (daemon)")
	name := fs.String("name", "gym-registry", "repository.name for /skills.json")
	update := fs.Bool("update", false, "update registry index on start")
	refreshEvery := fs.Duration("refresh", 0, "periodically update registry index (e.g. 10m); implies --update")
	stop := fs.Bool("stop", false, "stop daemon using pidfile")
	status := fs.Bool("status", false, "show daemon status using pidfile")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bindPort < 0 || *bindPort > 65535 {
		return fmt.Errorf("invalid --port %d", *bindPort)
	}
	if *refreshEvery > 0 {
		*update = true
	}
	if (*stop && *status) || (*daemon && *stop) || (*daemon && *status) {
		return fmt.Errorf("--daemon, --stop, and --status are mutually exclusive")
	}

	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}

	if *pidFile == "" {
		*pidFile = defaultPidFile(rt.Dirs.StateHome, *bindHost, *bindPort)
	}
	if *logFile == "" {
		*logFile = defaultLogFile(rt.Dirs.StateHome, *bindHost, *bindPort)
	}

	if *stop {
		msg, err := stopByPIDFile(*pidFile)
		if err != nil {
			return err
		}
		a.printlnf("%s", msg)
		return nil
	}
	if *status {
		msg, err := statusByPIDFile(*pidFile)
		if err != nil {
			return err
		}
		a.printlnf("%s", msg)
		return nil
	}

	if *daemon {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		childArgs := []string{"serve", "--host", *bindHost, "--port", strconv.Itoa(*bindPort), "--pidfile", *pidFile, "--logfile", *logFile, "--name", *name}
		if *update {
			childArgs = append(childArgs, "--update")
		}
		if *refreshEvery > 0 {
			childArgs = append(childArgs, "--refresh", refreshEvery.String())
		}

		pid, err := daemonize(exe, childArgs, *logFile)
		if err != nil {
			return err
		}
		a.printlnf("started daemon pid %d", pid)
		a.printlnf("pidfile: %s", *pidFile)
		a.printlnf("logfile: %s", *logFile)
		return nil
	}

	ctx := context.Background()
	if *update {
		if err := a.updateIndexOnce(ctx, rt); err != nil {
			return err
		}
	}

	if *refreshEvery > 0 {
		ticker := time.NewTicker(*refreshEvery)
		defer ticker.Stop()
		go func() {
			for range ticker.C {
				if err := a.updateIndexOnce(context.Background(), rt); err != nil {
					a.eprintf("serve refresh error: %v", err)
				}
			}
		}()
	}

	addr := net.JoinHostPort(*bindHost, strconv.Itoa(*bindPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	if err := writePID(*pidFile, os.Getpid()); err != nil {
		return err
	}
	defer func() { _ = os.Remove(*pidFile) }()

	s := &http.Server{Handler: serve.Handler(serve.Options{DataHome: rt.Dirs.DataHome, Name: *name})}
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

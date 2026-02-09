//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func stopByPIDFile(pidFile string) (string, error) {
	pid, err := readPID(pidFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "not running (pidfile missing)", nil
		}
		return "", err
	}

	if !processRunning(pid) {
		_ = os.Remove(pidFile)
		return fmt.Sprintf("not running (stale pid %d; removed pidfile)", pid), nil
	}

	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processRunning(pid) {
			_ = os.Remove(pidFile)
			return fmt.Sprintf("stopped (pid %d)", pid), nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("timed out waiting for pid %d to stop", pid)
}

func statusByPIDFile(pidFile string) (string, error) {
	pid, err := readPID(pidFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "stopped (pidfile missing)", nil
		}
		return "", err
	}
	if processRunning(pid) {
		return fmt.Sprintf("running (pid %d)", pid), nil
	}
	return fmt.Sprintf("stopped (stale pid %d)", pid), nil
}

func daemonize(exe string, args []string, logFile string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "GYM_DAEMON_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	in, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	var out io.Writer
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		out = f
	} else {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		out = f
	}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = in

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

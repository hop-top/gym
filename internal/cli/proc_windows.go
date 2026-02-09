//go:build windows

package cli

import (
	"fmt"
	"os"
)

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func stopByPIDFile(string) (string, error) {
	return "", fmt.Errorf("serve --stop is not supported on windows")
}

func statusByPIDFile(string) (string, error) {
	return "", fmt.Errorf("serve --status is not supported on windows")
}

func daemonize(string, []string, string) (int, error) {
	return 0, fmt.Errorf("serve --daemon is not supported on windows")
}

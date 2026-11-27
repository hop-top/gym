package cheat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/ai/toolspec"
	kitxdg "hop.top/kit/go/core/xdg"
)

// AdapterFile mirrors hop.top/tip/adapter.DescribeResponse at the top
// level so tip's existing adapter machinery can recognise the shape,
// then extends it with two gym-specific keys: "spec" (the full
// ToolSpec) and "skills" (the lightweight skill specs). tip's Generic
// adapter can use the former; bespoke "tip-gym" wrappers can use the
// latter.
type AdapterFile struct {
	// --- tip describe-response shape (copied verbatim from
	// hop.top/tip/adapter/protocol.go to avoid an import cycle on tip).
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	ToolVersionCmd string   `json:"tool_version_cmd,omitempty"`
	Commands       []string `json:"commands,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`

	// --- gym extensions
	SchemaVersion string              `json:"schema_version"`
	Spec          *toolspec.ToolSpec  `json:"spec,omitempty"`
	Skills        []toolspec.ToolSpec `json:"skills,omitempty"`
	GeneratedBy   string              `json:"generated_by"`
}

// DefaultAdapterPath returns the on-disk location for gym's adapter
// file. Resolves to <tip-config>/adapters/gym.json — i.e.
// $XDG_CONFIG_HOME/tip/adapters/gym.json on Linux, the OS-native
// equivalent everywhere else.
func DefaultAdapterPath() (string, error) {
	cfg, err := kitxdg.ConfigDir("tip")
	if err != nil {
		// Fall back to the raw path so a guard rejection still produces
		// a usable export location.
		raw, rawErr := kitxdg.RawConfigDir("tip")
		if rawErr != nil {
			return "", errors.Join(err, rawErr)
		}
		cfg = raw
	}
	return filepath.Join(cfg, "adapters", "gym.json"), nil
}

// WriteTipAdapter serialises the registry into a tip-compatible JSON
// adapter file at path. Creates parent directories as needed.
//
// path may be empty; in that case DefaultAdapterPath is used. The
// resolved path is returned alongside any error.
func WriteTipAdapter(path string, registry *Registry, gymVersion string) (string, error) {
	if registry == nil {
		return "", errors.New("cheat: nil registry")
	}
	if strings.TrimSpace(path) == "" {
		p, err := DefaultAdapterPath()
		if err != nil {
			return "", err
		}
		path = p
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("cheat: mkdir %s: %w", filepath.Dir(path), err)
	}

	self := registry.Self()
	var commands []string
	if self != nil {
		for _, c := range self.Commands {
			commands = append(commands, c.Name)
		}
	}

	file := AdapterFile{
		Name:           ToolName,
		Version:        firstNonEmpty(gymVersion, "dev"),
		ToolVersionCmd: "gym version",
		Commands:       commands,
		Capabilities:   []string{"toolspec", "cheat-sheets", "skill-registry"},
		SchemaVersion:  SchemaVersion,
		Spec:           self,
		Skills:         registry.Skills(),
		GeneratedBy:    "gym cheat --tip-export",
	}
	blob, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", fmt.Errorf("cheat: marshal adapter file: %w", err)
	}
	blob = append(blob, '\n')
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		return "", fmt.Errorf("cheat: write %s: %w", path, err)
	}
	return path, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

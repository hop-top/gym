package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hop.top/gym/internal/dataclass"
)

// Manifest file names and schema.
const (
	ManifestFile = "gym.json"
	SchemaV1     = "gym.skill/v1"
)

// Manifest is the author-supplied admission declaration (gym.skill/v1).
// Closed object: unknown fields are rejected. AllowedTools is sorted.
type Manifest struct {
	Schema       string
	AllowedTools []string
	DataClass    string
}

// rawManifest is the on-disk shape. The legacy scan fields
// (postinstall, shell_hook) are read by ScanStaging; admission only
// tolerates them. allow_postinstall and large_binary are accepted for
// compatibility but ignored everywhere: an author-set allowance never
// weakens a guard policy.
type rawManifest struct {
	Schema           string    `json:"schema"`
	AllowedTools     *[]string `json:"allowed_tools"`
	DataClass        *string   `json:"data_class"`
	Postinstall      *string   `json:"postinstall"`
	ShellHook        *string   `json:"shell_hook"`
	AllowPostinstall *bool     `json:"allow_postinstall"`
	LargeBinary      *bool     `json:"large_binary"`
}

// operatorOnlyFields are decided by the operator, never the author.
var operatorOnlyFields = []string{"digest", "approval_state"}

// LoadManifest reads gym.json at the skill root. present is false when
// the file does not exist; any other problem is a manifest-invalid
// AdmissionError.
func LoadManifest(root string) (m *Manifest, present bool, err error) {
	path := filepath.Join(root, ManifestFile)
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		return nil, false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, true, admissionErrorf(ReasonManifestInvalid, "read %s: %v", ManifestFile, err)
	}
	m, err = ParseManifest(raw)
	return m, true, err
}

// ParseManifest strictly decodes and validates gym.json bytes.
func ParseManifest(raw []byte) (*Manifest, error) {
	// Also rejects trailing data after the object.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, admissionErrorf(ReasonManifestInvalid, "%s is not a JSON object: %v", ManifestFile, err)
	}
	for _, f := range operatorOnlyFields {
		if _, ok := probe[f]; ok {
			return nil, admissionErrorf(ReasonManifestInvalid,
				"field %q is set by the operator, not the author", f)
		}
	}

	var rm rawManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rm); err != nil {
		return nil, admissionErrorf(ReasonManifestInvalid, "%s: %v", ManifestFile, err)
	}

	if rm.Schema != SchemaV1 {
		return nil, admissionErrorf(ReasonManifestInvalid,
			"schema must be %q, got %q", SchemaV1, rm.Schema)
	}
	if rm.AllowedTools == nil {
		return nil, admissionErrorf(ReasonManifestInvalid, "allowed_tools is required (may be empty)")
	}
	if rm.DataClass == nil {
		return nil, admissionErrorf(ReasonManifestInvalid, "data_class is required (one of: %s)", dataclass.Names())
	}
	if !dataclass.Valid(*rm.DataClass) {
		return nil, admissionErrorf(ReasonManifestInvalid,
			"data_class %q is not one of: %s", *rm.DataClass, dataclass.Names())
	}

	tools := append([]string(nil), *rm.AllowedTools...)
	seen := map[string]bool{}
	for _, tok := range tools {
		if _, _, _, ok := parseToolToken(tok); !ok {
			return nil, admissionErrorf(ReasonManifestInvalid,
				"allowed_tools token %q is malformed (want Name or Name(arg), no whitespace)", tok)
		}
		if seen[tok] {
			return nil, admissionErrorf(ReasonManifestInvalid, "allowed_tools token %q is duplicated", tok)
		}
		seen[tok] = true
	}
	sort.Strings(tools)
	return &Manifest{Schema: rm.Schema, AllowedTools: tools, DataClass: *rm.DataClass}, nil
}

func (m *Manifest) String() string {
	return fmt.Sprintf("%s tools=[%s] class=%s", m.Schema, strings.Join(m.AllowedTools, " "), m.DataClass)
}

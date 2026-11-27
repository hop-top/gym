// Package approvals is the operator-local record of skill approval
// decisions. Approvals are bound to the exact content digest, live in
// the user config directory (never a project tree, never a skill's own
// files), and are only ever written by the operator through
// `gym guard approve` / `gym guard revoke`.
package approvals

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"hop.top/gym/internal/config"
	"hop.top/gym/internal/xdg"
)

// State is the resolved approval state of a skill at a digest.
type State string

// Approval states.
const (
	Unreviewed State = "unreviewed"
	Approved   State = "approved"
	Revoked    State = "revoked"
)

// ValidState reports whether s is a state name.
func ValidState(s string) bool {
	switch State(s) {
	case Unreviewed, Approved, Revoked:
		return true
	}
	return false
}

// FileName is the approvals file inside the gym user config directory.
const FileName = "approvals.json"

// Record is one operator decision. A revoked record with an empty
// Digest revokes the skill name for every digest; with a Digest it
// revokes that digest.
type Record struct {
	Skill     string `json:"skill"`
	SourceURL string `json:"source_url,omitempty"`
	Digest    string `json:"digest,omitempty"`
	State     State  `json:"state"`
	By        string `json:"by"`
	At        string `json:"at"`
}

type file struct {
	Approvals []Record `json:"approvals"`
}

// Store reads and writes one approvals file.
type Store struct{ Path string }

// DefaultPath resolves the approvals file through internal/xdg.
func DefaultPath() (string, error) {
	dirs, err := xdg.AppDirs(config.AppName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dirs.ConfigHome, FileName), nil
}

// Open returns the store at path, or the default user-level one when
// path is empty.
func Open(path string) (*Store, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	return &Store{Path: path}, nil
}

func (s *Store) load() ([]Record, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("approvals: parse %s: %w", s.Path, err)
	}
	return f.Approvals, nil
}

func (s *Store) save(recs []Record) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(file{Approvals: recs}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".approvals-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(blob, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// Records returns every stored decision.
func (s *Store) Records() ([]Record, error) { return s.load() }

func revokes(r Record, skill, digest string) bool {
	if r.State != Revoked {
		return false
	}
	if r.Digest == "" {
		return r.Skill == skill
	}
	return r.Digest == digest
}

// Lookup resolves the state of skill at digest: revoked beats approved
// beats unreviewed. A malformed file is an error (fail closed).
func (s *Store) Lookup(skill, digest string) (State, error) {
	recs, err := s.load()
	if err != nil {
		return "", err
	}
	approved := false
	for _, r := range recs {
		if revokes(r, skill, digest) {
			return Revoked, nil
		}
		if r.State == Approved && r.Skill == skill && r.Digest == digest {
			approved = true
		}
	}
	if approved {
		return Approved, nil
	}
	return Unreviewed, nil
}

// Approve records approval of skill at digest, lifting any revocation
// that would still block it (name-level for skill, digest-level for
// digest).
func (s *Store) Approve(skill, sourceURL, digest, by string, at time.Time) error {
	recs, err := s.load()
	if err != nil {
		return err
	}
	kept := recs[:0:0]
	for _, r := range recs {
		if revokes(r, skill, digest) {
			continue
		}
		if r.State == Approved && r.Skill == skill && r.Digest == digest {
			continue
		}
		kept = append(kept, r)
	}
	kept = append(kept, Record{
		Skill: skill, SourceURL: sourceURL, Digest: digest,
		State: Approved, By: by, At: at.UTC().Format(time.RFC3339),
	})
	return s.save(kept)
}

// Revoke records revocation: of skill at digest when digest is set,
// else of the skill name for every digest. Matching approvals are
// dropped.
func (s *Store) Revoke(skill, digest, by string, at time.Time) error {
	recs, err := s.load()
	if err != nil {
		return err
	}
	kept := recs[:0:0]
	for _, r := range recs {
		if r.State == Approved && r.Skill == skill && (digest == "" || r.Digest == digest) {
			continue
		}
		if r.State == Revoked && r.Skill == skill && r.Digest == digest {
			continue
		}
		kept = append(kept, r)
	}
	kept = append(kept, Record{
		Skill: skill, Digest: digest,
		State: Revoked, By: by, At: at.UTC().Format(time.RFC3339),
	})
	return s.save(kept)
}

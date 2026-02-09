package registry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func indexPath(dataHome string) string {
	return filepath.Join(dataHome, "registry-index.json")
}

func LoadIndex(dataHome string) (Index, error) {
	b, err := os.ReadFile(indexPath(dataHome))
	if errors.Is(err, os.ErrNotExist) {
		return Index{}, nil
	}
	if err != nil {
		return Index{}, err
	}
	var idx Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return Index{}, err
	}
	return idx, nil
}

func SaveIndex(dataHome string, idx Index) error {
	if err := os.MkdirAll(filepath.Dir(indexPath(dataHome)), 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	blob = append(blob, '\n')
	return os.WriteFile(indexPath(dataHome), blob, 0o644)
}

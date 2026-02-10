package warmup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Metadata represents parsed SKILL.md frontmatter.
type Metadata struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  string            `yaml:"allowed-tools,omitempty"`
}

// ExtractMetadata parses YAML frontmatter from SKILL.md in the given directory.
func ExtractMetadata(skillPath string) (*Metadata, error) {
	skillFile := filepath.Join(skillPath, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	frontmatter, err := extractFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("extract frontmatter: %w", err)
	}

	var meta Metadata
	if err := yaml.Unmarshal(frontmatter, &meta); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}

	if meta.Name == "" {
		return nil, errors.New("frontmatter missing required field: name")
	}
	if meta.Description == "" {
		return nil, errors.New("frontmatter missing required field: description")
	}

	return &meta, nil
}

// extractFrontmatter extracts YAML between --- delimiters.
func extractFrontmatter(data []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var inFrontmatter bool
	var frontmatter []string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			// Second --- ends frontmatter
			break
		}

		if inFrontmatter {
			frontmatter = append(frontmatter, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(frontmatter) == 0 {
		return nil, errors.New("no YAML frontmatter found")
	}

	return []byte(strings.Join(frontmatter, "\n")), nil
}

package warmup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/state"
	"hop.top/gym/internal/warmup"
)

func TestExtractMetadata(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantName    string
		wantDesc    string
		wantErr     bool
	}{
		{
			name: "valid frontmatter",
			content: `---
name: test-skill
description: A test skill for testing
license: MIT
---

# Test Skill

Body content here.`,
			wantName: "test-skill",
			wantDesc: "A test skill for testing",
			wantErr:  false,
		},
		{
			name: "minimal frontmatter",
			content: `---
name: minimal
description: Minimal skill
---

Content`,
			wantName: "minimal",
			wantDesc: "Minimal skill",
			wantErr:  false,
		},
		{
			name: "missing name",
			content: `---
description: Missing name field
---

Content`,
			wantErr: true,
		},
		{
			name: "missing description",
			content: `---
name: no-desc
---

Content`,
			wantErr: true,
		},
		{
			name: "no frontmatter",
			content: `# Just Markdown

No YAML frontmatter here.`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory with SKILL.md
			tmpDir := t.TempDir()
			skillFile := filepath.Join(tmpDir, "SKILL.md")
			if err := os.WriteFile(skillFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			// Extract metadata
			meta, err := warmup.ExtractMetadata(tmpDir)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if meta.Name != tt.wantName {
				t.Errorf("name = %q, want %q", meta.Name, tt.wantName)
			}

			if meta.Description != tt.wantDesc {
				t.Errorf("description = %q, want %q", meta.Description, tt.wantDesc)
			}
		})
	}
}

func TestGenerateXML(t *testing.T) {
	skills := []warmup.SkillInfo{
		{
			Name:        "skill-one",
			Description: "First test skill",
			Scope:       "global",
		},
		{
			Name:        "skill-two",
			Description: "Second test skill with <special> & \"chars\"",
			Scope:       "project",
		},
	}

	xml := warmup.GenerateXML(skills)

	// Check structure
	if !strings.Contains(xml, "<available_skills>") {
		t.Error("missing opening tag")
	}
	if !strings.Contains(xml, "</available_skills>") {
		t.Error("missing closing tag")
	}

	// Check skill entries
	if !strings.Contains(xml, "<name>skill-one</name>") {
		t.Error("missing skill-one name")
	}
	if !strings.Contains(xml, "<description>First test skill</description>") {
		t.Error("missing skill-one description")
	}
	if !strings.Contains(xml, "<location>global</location>") {
		t.Error("missing skill-one location")
	}

	// Check XML escaping
	if !strings.Contains(xml, "&lt;special&gt;") {
		t.Error("< and > not escaped")
	}
	if !strings.Contains(xml, "&amp;") {
		t.Error("& not escaped")
	}
	if !strings.Contains(xml, "&quot;") {
		t.Error("\" not escaped")
	}
}

func TestGenerateJSON(t *testing.T) {
	skills := []warmup.SkillInfo{
		{
			Name:        "json-skill",
			Description: "Test skill",
			Version:     "1.0.0",
			Scope:       "global",
			InstallPath: "/path/to/skill",
		},
	}

	json, err := warmup.GenerateJSON(skills)
	if err != nil {
		t.Fatalf("GenerateJSON failed: %v", err)
	}

	// Check JSON structure
	if !strings.Contains(json, `"name": "json-skill"`) {
		t.Error("missing name field")
	}
	if !strings.Contains(json, `"description": "Test skill"`) {
		t.Error("missing description field")
	}
	if !strings.Contains(json, `"version": "1.0.0"`) {
		t.Error("missing version field")
	}
	if !strings.Contains(json, `"scope": "global"`) {
		t.Error("missing scope field")
	}
}

func TestGenerateAgentsMD(t *testing.T) {
	skills := []warmup.SkillInfo{
		{
			Name:        "global-skill",
			Description: "A global skill",
			Scope:       "global",
		},
		{
			Name:        "project-skill",
			Description: "A project skill",
			Scope:       "project",
		},
	}

	md := warmup.GenerateAgentsMD(skills)

	// Check headers
	if !strings.Contains(md, "# Available Skills") {
		t.Error("missing main header")
	}
	if !strings.Contains(md, "## Global Skills") {
		t.Error("missing global skills section")
	}
	if !strings.Contains(md, "## Project Skills") {
		t.Error("missing project skills section")
	}

	// Check skill entries
	if !strings.Contains(md, "- **global-skill**: A global skill") {
		t.Error("missing global skill entry")
	}
	if !strings.Contains(md, "- **project-skill**: A project skill") {
		t.Error("missing project skill entry")
	}
}

func TestBuildSkillInfo(t *testing.T) {
	// Create temp dir with test SKILL.md
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillContent := `---
name: test-skill
description: Extracted from SKILL.md
version: "2.0.0"
---

# Test Skill
Body content.`

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	installed := []state.InstalledSkill{
		{
			Name:        "test-skill",
			Version:     "1.0.0",
			InstallPath: skillDir,
		},
	}

	infos := warmup.BuildSkillInfo(installed, "global")

	if len(infos) != 1 {
		t.Fatalf("expected 1 skill info, got %d", len(infos))
	}

	info := infos[0]
	if info.Name != "test-skill" {
		t.Errorf("name = %q, want test-skill", info.Name)
	}
	if info.Description != "Extracted from SKILL.md" {
		t.Errorf("description = %q, want 'Extracted from SKILL.md'", info.Description)
	}
	if info.Scope != "global" {
		t.Errorf("scope = %q, want global", info.Scope)
	}
}

func TestBuildSkillInfo_FallbackDescription(t *testing.T) {
	// Test with non-existent skill path (metadata extraction fails)
	installed := []state.InstalledSkill{
		{
			Name:        "missing-skill",
			Version:     "1.0.0",
			InstallPath: "/nonexistent/path",
		},
	}

	infos := warmup.BuildSkillInfo(installed, "project")

	if len(infos) != 1 {
		t.Fatalf("expected 1 skill info, got %d", len(infos))
	}

	info := infos[0]
	if info.Description != "No description available" {
		t.Errorf("expected fallback description, got %q", info.Description)
	}
}

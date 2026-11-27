package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/gym/internal/cli"
)

func TestWarmupCommand_XML(t *testing.T) {
	// Setup test environment
	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".local", "state", "gym")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	// Create test skill directory with SKILL.md
	skillDir := filepath.Join(tmpHome, ".claude", "skills", "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillContent := `---
name: test-skill
description: A test skill for warmup
---

# Test Skill
Body.`

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create installed-global.json
	installedState := `{
  "skills": [
    {
      "name": "test-skill",
      "version": "1.0.0",
      "source_url": "https://example.com/test-skill",
      "installed_at": "2024-02-10T12:00:00Z",
      "install_path": "` + skillDir + `"
    }
  ]
}`

	if err := os.WriteFile(filepath.Join(stateDir, "installed-global.json"), []byte(installedState), 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	// Set environment
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmpHome, ".local", "state"))
	t.Setenv("HOME", tmpHome)

	// Run warmup command
	var stdout, stderr bytes.Buffer
	app := cli.NewApp(&stdout, &stderr, tmpHome)
	exitCode := app.Run([]string{"warmup", "--format", "xml", "--scope", "global"})

	if exitCode != 0 {
		t.Fatalf("command failed with exit code %d: %s", exitCode, stderr.String())
	}

	output := stdout.String()

	// Verify XML structure
	if !strings.Contains(output, "<available_skills>") {
		t.Error("missing <available_skills> opening tag")
	}
	if !strings.Contains(output, "</available_skills>") {
		t.Error("missing </available_skills> closing tag")
	}
	if !strings.Contains(output, "<name>test-skill</name>") {
		t.Error("missing skill name in XML")
	}
	if !strings.Contains(output, "<description>A test skill for warmup</description>") {
		t.Error("missing skill description in XML")
	}
	if !strings.Contains(output, "<location>global</location>") {
		t.Error("missing location in XML")
	}
}

func TestWarmupCommand_JSON(t *testing.T) {
	// Setup test environment
	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".local", "state", "gym")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	// Create test skill
	skillDir := filepath.Join(tmpHome, ".claude", "skills", "json-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillContent := `---
name: json-skill
description: JSON format test
---

Body.`

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create state
	installedState := `{
  "skills": [
    {
      "name": "json-skill",
      "version": "2.0.0",
      "source_url": "https://example.com/json-skill",
      "installed_at": "2024-02-10T12:00:00Z",
      "install_path": "` + skillDir + `"
    }
  ]
}`

	if err := os.WriteFile(filepath.Join(stateDir, "installed-global.json"), []byte(installedState), 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	// Set environment
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmpHome, ".local", "state"))
	t.Setenv("HOME", tmpHome)

	// Run warmup command
	var stdout, stderr bytes.Buffer
	app := cli.NewApp(&stdout, &stderr, tmpHome)
	exitCode := app.Run([]string{"warmup", "--format", "json", "--scope", "global"})

	if exitCode != 0 {
		t.Fatalf("command failed with exit code %d: %s", exitCode, stderr.String())
	}

	// Parse JSON output
	var skills []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &skills); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	skill := skills[0]
	if skill["name"] != "json-skill" {
		t.Errorf("name = %v, want json-skill", skill["name"])
	}
	if skill["description"] != "JSON format test" {
		t.Errorf("description = %v, want 'JSON format test'", skill["description"])
	}
	if skill["version"] != "2.0.0" {
		t.Errorf("version = %v, want 2.0.0", skill["version"])
	}
	if skill["scope"] != "global" {
		t.Errorf("scope = %v, want global", skill["scope"])
	}
}

func TestWarmupCommand_AgentsMD(t *testing.T) {
	// Setup test environment
	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".local", "state", "gym")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	// Create test skill
	skillDir := filepath.Join(tmpHome, ".claude", "skills", "md-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillContent := `---
name: md-skill
description: Markdown format test
---

Body.`

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create state
	installedState := `{
  "skills": [
    {
      "name": "md-skill",
      "version": "1.0.0",
      "source_url": "https://example.com/md-skill",
      "installed_at": "2024-02-10T12:00:00Z",
      "install_path": "` + skillDir + `"
    }
  ]
}`

	if err := os.WriteFile(filepath.Join(stateDir, "installed-global.json"), []byte(installedState), 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	// Set environment
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmpHome, ".local", "state"))
	t.Setenv("HOME", tmpHome)

	// Run warmup command
	var stdout, stderr bytes.Buffer
	app := cli.NewApp(&stdout, &stderr, tmpHome)
	exitCode := app.Run([]string{"warmup", "--format", "agents-md", "--scope", "global"})

	if exitCode != 0 {
		t.Fatalf("command failed with exit code %d: %s", exitCode, stderr.String())
	}

	output := stdout.String()

	// Verify markdown structure
	if !strings.Contains(output, "# Available Skills") {
		t.Error("missing main header")
	}
	if !strings.Contains(output, "## Global Skills") {
		t.Error("missing global skills section")
	}
	if !strings.Contains(output, "- **md-skill**: Markdown format test") {
		t.Error("missing skill entry")
	}
}

func TestWarmupCommand_OutputFile(t *testing.T) {
	// Setup test environment
	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".local", "state", "gym")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	// Create test skill
	skillDir := filepath.Join(tmpHome, ".claude", "skills", "file-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillContent := `---
name: file-skill
description: File output test
---

Body.`

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create state
	installedState := `{
  "skills": [
    {
      "name": "file-skill",
      "version": "1.0.0",
      "source_url": "https://example.com/file-skill",
      "installed_at": "2024-02-10T12:00:00Z",
      "install_path": "` + skillDir + `"
    }
  ]
}`

	if err := os.WriteFile(filepath.Join(stateDir, "installed-global.json"), []byte(installedState), 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	// Set environment
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmpHome, ".local", "state"))
	t.Setenv("HOME", tmpHome)

	// Run warmup command with output file
	outputFile := filepath.Join(tmpHome, "output.xml")
	var stdout, stderr bytes.Buffer
	app := cli.NewApp(&stdout, &stderr, tmpHome)
	exitCode := app.Run([]string{"warmup", "--format", "xml", "-o", outputFile})

	if exitCode != 0 {
		t.Fatalf("command failed with exit code %d: %s", exitCode, stderr.String())
	}

	// Verify file was created
	if _, err := os.Stat(outputFile); err != nil {
		t.Fatalf("output file not created: %v", err)
	}

	// Verify file contents
	content, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	if !strings.Contains(string(content), "<available_skills>") {
		t.Error("output file missing XML content")
	}
	if !strings.Contains(string(content), "<name>file-skill</name>") {
		t.Error("output file missing skill name")
	}

	// Verify stdout message
	if !strings.Contains(stdout.String(), "wrote xml to") {
		t.Error("missing success message in stdout")
	}
}

func TestWarmupCommand_NoSkills(t *testing.T) {
	// Setup test environment with no skills
	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".local", "state", "gym")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	// Create empty state file
	if err := os.WriteFile(filepath.Join(stateDir, "installed-global.json"), []byte(`{"skills":[]}`), 0644); err != nil {
		t.Fatalf("failed to write empty state file: %v", err)
	}

	// Set environment
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmpHome, ".local", "state"))
	t.Setenv("HOME", tmpHome)

	// Run warmup command
	var stdout, stderr bytes.Buffer
	app := cli.NewApp(&stdout, &stderr, tmpHome)
	exitCode := app.Run([]string{"warmup", "--format", "xml"})

	// Should fail with no skills
	if exitCode == 0 {
		t.Error("expected command to fail with no skills")
	}

	if !strings.Contains(stderr.String(), "no skills installed") {
		t.Errorf("expected 'no skills installed' error, got: %s", stderr.String())
	}
}

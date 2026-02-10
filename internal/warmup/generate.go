package warmup

import (
	"encoding/json"
	"fmt"
	"strings"

	"hop.top/gym/internal/state"
)

// SkillInfo combines installed skill data with its metadata.
type SkillInfo struct {
	Name        string
	Description string
	Version     string
	Scope       string // "global" or "project"
	InstallPath string
	Metadata    *Metadata
}

// GenerateXML creates <available_skills> XML block for Claude Code.
func GenerateXML(skills []SkillInfo) string {
	var buf strings.Builder
	buf.WriteString("<available_skills>\n")
	for _, s := range skills {
		buf.WriteString("  <skill>\n")
		buf.WriteString(fmt.Sprintf("    <name>%s</name>\n", xmlEscape(s.Name)))
		buf.WriteString(fmt.Sprintf("    <description>%s</description>\n", xmlEscape(s.Description)))
		buf.WriteString(fmt.Sprintf("    <location>%s</location>\n", xmlEscape(s.Scope)))
		buf.WriteString("  </skill>\n")
	}
	buf.WriteString("</available_skills>\n")
	return buf.String()
}

// GenerateJSON creates JSON array of skill metadata.
func GenerateJSON(skills []SkillInfo) (string, error) {
	type jsonSkill struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version,omitempty"`
		Scope       string `json:"scope"`
		InstallPath string `json:"install_path,omitempty"`
	}

	output := make([]jsonSkill, 0, len(skills))
	for _, s := range skills {
		output = append(output, jsonSkill{
			Name:        s.Name,
			Description: s.Description,
			Version:     s.Version,
			Scope:       s.Scope,
			InstallPath: s.InstallPath,
		})
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

// GenerateAgentsMD creates AGENTS.md format for multi-agent compatibility.
func GenerateAgentsMD(skills []SkillInfo) string {
	var buf strings.Builder
	buf.WriteString("# Available Skills\n\n")
	buf.WriteString("Skills available for use by agents. Use `/skill <name>` to load a skill on-demand.\n\n")

	globalSkills := []SkillInfo{}
	projectSkills := []SkillInfo{}

	for _, s := range skills {
		if s.Scope == "global" {
			globalSkills = append(globalSkills, s)
		} else {
			projectSkills = append(projectSkills, s)
		}
	}

	if len(globalSkills) > 0 {
		buf.WriteString("## Global Skills\n\n")
		for _, s := range globalSkills {
			buf.WriteString(fmt.Sprintf("- **%s**: %s\n", s.Name, s.Description))
		}
		buf.WriteString("\n")
	}

	if len(projectSkills) > 0 {
		buf.WriteString("## Project Skills\n\n")
		for _, s := range projectSkills {
			buf.WriteString(fmt.Sprintf("- **%s**: %s\n", s.Name, s.Description))
		}
		buf.WriteString("\n")
	}

	return buf.String()
}

// BuildSkillInfo creates SkillInfo from installed skills with metadata extraction.
func BuildSkillInfo(installed []state.InstalledSkill, scope string) []SkillInfo {
	result := make([]SkillInfo, 0, len(installed))

	for _, skill := range installed {
		info := SkillInfo{
			Name:        skill.Name,
			Version:     skill.Version,
			Scope:       scope,
			InstallPath: skill.InstallPath,
		}

		// Try to extract fresh metadata from SKILL.md
		if skill.InstallPath != "" {
			if meta, err := ExtractMetadata(skill.InstallPath); err == nil {
				info.Metadata = meta
				// Use fresh metadata description
				if meta.Description != "" {
					info.Description = meta.Description
				}
			}
		}

		// Fallback if no description extracted
		if info.Description == "" {
			info.Description = "No description available"
		}

		result = append(result, info)
	}

	return result
}

// xmlEscape escapes special XML characters.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

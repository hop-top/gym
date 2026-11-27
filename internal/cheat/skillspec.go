package cheat

import (
	"hop.top/kit/go/ai/toolspec"

	"hop.top/gym/internal/state"
	"hop.top/gym/internal/warmup"
)

// SkillSpecExtras captures optional ToolSpec-bearing fields a SKILL.md
// frontmatter may surface. Today's gym manifest doesn't standardise
// these names; the extractor below pulls them best-effort from the free
// "metadata" map so authors can opt in incrementally.
type SkillSpecExtras struct {
	// Commands lists CLI invocations the skill exposes/recommends.
	Commands []string
	// Examples lists narrative usage hints.
	Examples []string
}

// BuildSkillSpecs derives one lightweight ToolSpec per installed skill.
// Each spec captures: name, description, optional commands/examples.
// Skills whose SKILL.md cannot be read are still represented (name +
// stub description) so the registry stays consistent with installed.json.
func BuildSkillSpecs(installed []state.InstalledSkill) []toolspec.ToolSpec {
	out := make([]toolspec.ToolSpec, 0, len(installed))
	for _, sk := range installed {
		spec := toolspec.ToolSpec{
			Name:          sk.Name,
			SchemaVersion: SchemaVersion,
		}
		desc := ""
		var extras SkillSpecExtras
		if sk.InstallPath != "" {
			if meta, err := warmup.ExtractMetadata(sk.InstallPath); err == nil && meta != nil {
				desc = meta.Description
				extras = extractExtras(meta)
			}
		}
		root := toolspec.Command{
			Name: sk.Name,
			Safety: &toolspec.Safety{
				Level: toolspec.SafetyLevelSafe,
			},
			Intent: &toolspec.Intent{
				Domain:   "skill",
				Category: "agent-skill",
			},
		}
		if desc != "" {
			// kit/ai/toolspec.Command has no description field; keep the
			// description in Intent.Tags so it round-trips through JSON.
			root.Intent.Tags = []string{desc}
		}
		for _, cmd := range extras.Commands {
			root.Children = append(root.Children, toolspec.Command{
				Name: cmd,
				Safety: &toolspec.Safety{
					Level: toolspec.SafetyLevelCaution,
				},
			})
		}
		spec.Commands = []toolspec.Command{root}
		if len(extras.Examples) > 0 {
			spec.Workflows = []toolspec.Workflow{{
				Name:  "examples",
				Steps: extras.Examples,
			}}
		}
		out = append(out, spec)
	}
	return out
}

// extractExtras pulls optional commands/examples lists from a SKILL.md
// frontmatter. The current Metadata schema (see internal/warmup) flat-
// types the "metadata" block as map[string]string, so we accept a
// comma-separated list under the "commands" / "examples" keys.
func extractExtras(meta *warmup.Metadata) SkillSpecExtras {
	if meta == nil {
		return SkillSpecExtras{}
	}
	var ex SkillSpecExtras
	if v, ok := meta.Metadata["commands"]; ok {
		ex.Commands = splitList(v)
	}
	if v, ok := meta.Metadata["examples"]; ok {
		ex.Examples = splitList(v)
	}
	return ex
}

// splitList splits a comma- or newline-separated list, trimming
// surrounding whitespace and dropping empties.
func splitList(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		switch r {
		case ',', '\n':
			if t := trim(cur); t != "" {
				out = append(out, t)
			}
			cur = ""
		default:
			cur += string(r)
		}
	}
	if t := trim(cur); t != "" {
		out = append(out, t)
	}
	return out
}

// trim strips ASCII whitespace from both ends without pulling in
// unicode tables — list entries are CLI tokens.
func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

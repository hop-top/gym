package install

import (
	"strings"
	"unicode"

	"hop.top/gym/internal/warmup"
)

// parseToolToken splits an Agent Skills tool token, `Name` or
// `Name(arg)`. ok is false for empty tokens, whitespace inside the
// token, or unbalanced parentheses.
func parseToolToken(tok string) (name, arg string, hasArg, ok bool) {
	if tok == "" || strings.IndexFunc(tok, unicode.IsSpace) >= 0 {
		return "", "", false, false
	}
	i := strings.IndexByte(tok, '(')
	if i < 0 {
		if strings.ContainsAny(tok, "()") {
			return "", "", false, false
		}
		return tok, "", false, true
	}
	if i == 0 || !strings.HasSuffix(tok, ")") {
		return "", "", false, false
	}
	name, arg = tok[:i], tok[i+1:len(tok)-1]
	if strings.ContainsAny(name, "()") || strings.ContainsAny(arg, "()") {
		return "", "", false, false
	}
	return name, arg, true, true
}

// ToolCovers reports whether declared covers used.
//
//   - Same name, declared without an argument: covers any use of that
//     name (`Bash` covers `Bash(git:status)`).
//   - Declared `Name(prefix:*)` covers `Name(prefix:` + anything. The
//     wildcard is only a trailing `*` after `:`.
//   - Otherwise exact, case-sensitive.
func ToolCovers(declared, used string) bool {
	dn, da, dHas, ok := parseToolToken(declared)
	if !ok {
		return false
	}
	un, ua, uHas, ok := parseToolToken(used)
	if !ok || dn != un {
		return false
	}
	if !dHas {
		return true
	}
	if !uHas {
		return false
	}
	if strings.HasSuffix(da, ":*") {
		return strings.HasPrefix(ua, strings.TrimSuffix(da, "*"))
	}
	return da == ua
}

// UndeclaredTools returns the used tokens no declared token covers,
// in input order.
func UndeclaredTools(declared, used []string) []string {
	out := []string{}
	for _, u := range used {
		covered := false
		for _, d := range declared {
			if ToolCovers(d, u) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, u)
		}
	}
	return out
}

// FrontmatterTools returns the SKILL.md `allowed-tools` tokens
// (space-separated string per the Agent Skills spec). A frontmatter
// that does not parse, such as `allowed-tools` given as a YAML list,
// is a manifest-invalid AdmissionError.
func FrontmatterTools(skillRoot string) ([]string, error) {
	meta, err := warmup.ExtractMetadata(skillRoot)
	if err != nil {
		return nil, admissionErrorf(ReasonManifestInvalid, "SKILL.md: %v", err)
	}
	return strings.Fields(meta.AllowedTools), nil
}

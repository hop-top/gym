package guard

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// builtinFS embeds the baked-in policy YAMLs. Filename (sans .yaml)
// becomes the default policy name when the file omits it.
//
//go:embed policies/*.yaml
var builtinFS embed.FS

// rawPolicy mirrors the on-disk YAML. Internal — not exported.
type rawPolicy struct {
	Name      string `yaml:"name"`
	When      string `yaml:"when"`
	Effect    string `yaml:"effect"`
	Otherwise string `yaml:"otherwise"`
	Severity  string `yaml:"severity"`
	Message   string `yaml:"message"`
}

// rawOverrides is the shape of the policies.yaml override file.
type rawOverrides struct {
	Disable []string `yaml:"disable"`
	Relax   []string `yaml:"relax"`
}

// LoadOptions bundles the override directories. Empty entries skip.
type LoadOptions struct {
	UserDir    string // typically $XDG_CONFIG_HOME/gym/policies
	ProjectDir string // typically <root>/.gym/policies
}

// Load reads built-in policies, then user and project overrides, and
// returns the effective policy set. Override precedence:
//
//  1. baked-in defaults (one .yaml per policy under policies/)
//  2. user dir (.yaml redefines existing names; new files add policies)
//  3. project dir (same rules as user; project wins on conflict)
//  4. policies.yaml in user/project dir applies disable/relax to the
//     accumulated set.
func Load(opts LoadOptions) ([]Policy, error) {
	policies, err := loadBuiltins()
	if err != nil {
		return nil, err
	}

	if opts.UserDir != "" {
		extra, err := loadDir(opts.UserDir)
		if err != nil {
			return nil, fmt.Errorf("guard: user overrides: %w", err)
		}
		policies = mergePolicies(policies, extra)
		if ov, ok, err := loadOverrides(opts.UserDir); err != nil {
			return nil, fmt.Errorf("guard: user policies.yaml: %w", err)
		} else if ok {
			policies = applyOverrides(policies, ov)
		}
	}

	if opts.ProjectDir != "" {
		extra, err := loadDir(opts.ProjectDir)
		if err != nil {
			return nil, fmt.Errorf("guard: project overrides: %w", err)
		}
		policies = mergePolicies(policies, extra)
		if ov, ok, err := loadOverrides(opts.ProjectDir); err != nil {
			return nil, fmt.Errorf("guard: project policies.yaml: %w", err)
		} else if ok {
			policies = applyOverrides(policies, ov)
		}
	}

	sort.SliceStable(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })
	return policies, nil
}

func loadBuiltins() ([]Policy, error) {
	out := make([]Policy, 0)
	entries, err := fs.ReadDir(builtinFS, "policies")
	if err != nil {
		return nil, fmt.Errorf("guard: read builtins: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		// Skip any policies.yaml that ships with the embed (none today).
		if e.Name() == "policies.yaml" {
			continue
		}
		raw, err := fs.ReadFile(builtinFS, "policies/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("guard: read builtin %s: %w", e.Name(), err)
		}
		p, err := parsePolicyBytes(raw, "builtin:"+e.Name(), nameFromFile(e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func loadDir(dir string) ([]Policy, error) {
	st, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Policy, 0)
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) || e.Name() == "policies.yaml" {
			continue
		}
		full := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		p, err := parsePolicyBytes(raw, full, nameFromFile(e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func parsePolicyBytes(data []byte, source, defaultName string) (Policy, error) {
	var rp rawPolicy
	if err := yaml.Unmarshal(data, &rp); err != nil {
		return Policy{}, fmt.Errorf("guard: parse %s: %w", source, err)
	}
	if rp.Name == "" {
		rp.Name = defaultName
	}
	if rp.Name == "" {
		return Policy{}, fmt.Errorf("guard: %s: name required", source)
	}
	if rp.When == "" {
		return Policy{}, fmt.Errorf("guard: %s: when required", source)
	}
	eff, err := parseEffect(rp.Effect, EffectDeny)
	if err != nil {
		return Policy{}, fmt.Errorf("guard: %s: effect: %w", source, err)
	}
	oth, err := parseEffect(rp.Otherwise, EffectAllow)
	if err != nil {
		return Policy{}, fmt.Errorf("guard: %s: otherwise: %w", source, err)
	}
	sev := parseSeverity(rp.Severity, severityFromEffect(eff))
	return Policy{
		Name:      rp.Name,
		When:      rp.When,
		Effect:    eff,
		Otherwise: oth,
		Severity:  sev,
		Message:   rp.Message,
		Source:    source,
	}, nil
}

func parseEffect(s string, def Effect) (Effect, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return def, nil
	case "allow":
		return EffectAllow, nil
	case "warn":
		return EffectWarn, nil
	case "deny":
		return EffectDeny, nil
	default:
		return "", fmt.Errorf("invalid effect %q (want allow|warn|deny)", s)
	}
}

func parseSeverity(s string, def Severity) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return SeverityAllow
	case "warn":
		return SeverityWarn
	case "deny":
		return SeverityDeny
	default:
		return def
	}
}

func severityFromEffect(e Effect) Severity {
	switch e {
	case EffectAllow:
		return SeverityAllow
	case EffectWarn:
		return SeverityWarn
	case EffectDeny:
		return SeverityDeny
	}
	return SeverityDeny
}

func loadOverrides(dir string) (rawOverrides, bool, error) {
	path := filepath.Join(dir, "policies.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return rawOverrides{}, false, nil
		}
		return rawOverrides{}, false, err
	}
	var ov rawOverrides
	if err := yaml.Unmarshal(raw, &ov); err != nil {
		return rawOverrides{}, false, err
	}
	return ov, true, nil
}

func applyOverrides(policies []Policy, ov rawOverrides) []Policy {
	disabled := map[string]bool{}
	for _, n := range ov.Disable {
		disabled[strings.TrimSpace(n)] = true
	}
	relaxed := map[string]bool{}
	for _, n := range ov.Relax {
		relaxed[strings.TrimSpace(n)] = true
	}
	out := make([]Policy, 0, len(policies))
	for _, p := range policies {
		if disabled[p.Name] {
			continue
		}
		if relaxed[p.Name] {
			if p.Effect == EffectDeny {
				p.Effect = EffectWarn
			}
			if p.Severity == SeverityDeny {
				p.Severity = SeverityWarn
			}
		}
		out = append(out, p)
	}
	return out
}

func mergePolicies(base, extra []Policy) []Policy {
	idx := map[string]int{}
	for i, p := range base {
		idx[p.Name] = i
	}
	out := append([]Policy(nil), base...)
	for _, p := range extra {
		if i, ok := idx[p.Name]; ok {
			out[i] = p
			continue
		}
		idx[p.Name] = len(out)
		out = append(out, p)
	}
	return out
}

func isYAML(name string) bool {
	low := strings.ToLower(name)
	return strings.HasSuffix(low, ".yaml") || strings.HasSuffix(low, ".yml")
}

func nameFromFile(name string) string {
	low := strings.ToLower(name)
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(low, ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

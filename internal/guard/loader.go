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

	charmlog "charm.land/log/v2"
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
	// ProjectDirs are the .gym/policies dirs of a folder-scope chain,
	// outermost first. They apply in that order, then ProjectDir.
	ProjectDirs []string
	// Logger receives one warning per ignored project entry. May be nil.
	Logger *charmlog.Logger
}

// Load reads built-in policies, then user and project overrides, and
// returns the effective policy set. Trust model: the user dir is the
// operator's and may redefine, disable or relax anything. The project
// dir is repository content and may only add policies; entries that
// would weaken a built-in or user policy are ignored with a warning:
//
//  1. baked-in defaults (one .yaml per policy under policies/)
//  2. user dir (.yaml redefines existing names; new files add policies;
//     policies.yaml applies disable/relax to the accumulated set)
//  3. project dirs, outermost first: new-name .yaml files add policies; a
//     file whose policy name collides with a built-in, user or outer
//     folder's policy is ignored (the outermost definition wins);
//     policies.yaml disable/relax apply only to names defined by that
//     same dir.
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
			policies = applyOverrides(policies, ov, nil)
		}
	}

	dirs := append([]string(nil), opts.ProjectDirs...)
	if opts.ProjectDir != "" {
		dirs = append(dirs, opts.ProjectDir)
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		var err error
		policies, err = loadProjectDir(policies, dir, opts.Logger)
		if err != nil {
			return nil, err
		}
	}

	sort.SliceStable(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })
	return policies, nil
}

// loadProjectDir applies one folder's policy dir on top of policies. The
// dir is repository content: it adds new names only.
func loadProjectDir(policies []Policy, dir string, logger *charmlog.Logger) ([]Policy, error) {
	extra, err := loadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("guard: project overrides: %w", err)
	}
	trusted := map[string]bool{}
	for _, p := range policies {
		trusted[p.Name] = true
	}
	own := map[string]bool{}
	var add []Policy
	for _, p := range extra {
		if trusted[p.Name] {
			warnf(logger, "guard: ignoring project policy %q (%s): would redefine built-in/user policy", p.Name, p.Source)
			continue
		}
		own[p.Name] = true
		add = append(add, p)
	}
	policies = mergePolicies(policies, add)
	if ov, ok, err := loadOverrides(dir); err != nil {
		return nil, fmt.Errorf("guard: project policies.yaml: %w", err)
	} else if ok {
		path := filepath.Join(dir, "policies.yaml")
		policies = applyOverrides(policies, ov, func(kind, name string) bool {
			if own[name] {
				return true
			}
			warnf(logger, "guard: ignoring project %s %q (%s): policy is not project-defined", kind, name, path)
			return false
		})
	}
	return policies, nil
}

func warnf(l *charmlog.Logger, format string, args ...any) {
	if l != nil {
		l.Warnf(format, args...)
	}
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

// applyOverrides applies disable/relax. allow, when non-nil, is asked
// per entry (kind "disable" or "relax") and may veto it.
func applyOverrides(policies []Policy, ov rawOverrides, allow func(kind, name string) bool) []Policy {
	ok := func(kind, name string) bool { return allow == nil || allow(kind, name) }
	disabled := map[string]bool{}
	for _, n := range ov.Disable {
		n = strings.TrimSpace(n)
		if ok("disable", n) {
			disabled[n] = true
		}
	}
	relaxed := map[string]bool{}
	for _, n := range ov.Relax {
		n = strings.TrimSpace(n)
		if ok("relax", n) {
			relaxed[n] = true
		}
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

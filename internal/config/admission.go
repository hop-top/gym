package config

import (
	"fmt"

	kitconfig "hop.top/kit/go/core/config"

	"hop.top/gym/internal/dataclass"
)

// Manifest requirement modes for `admission.require_manifest`.
const (
	ManifestOff    = "off"
	ManifestWarn   = "warn"
	ManifestReject = "reject"
)

// AdmissionConfig tunes the install-time admission stage.
type AdmissionConfig struct {
	// MaxDataClass is the highest data class a skill may declare.
	MaxDataClass string `json:"max_data_class" yaml:"max_data_class"`
	// RequireApproval turns an unreviewed skill into a deny.
	RequireApproval bool `json:"require_approval" yaml:"require_approval"`
	// RequireManifest is off|warn|reject for skills without gym.json.
	RequireManifest string `json:"require_manifest" yaml:"require_manifest"`
}

// AdmissionOverrides is one config layer's `admission` block; nil
// fields are unset.
type AdmissionOverrides struct {
	MaxDataClass    *string `json:"max_data_class,omitempty"`
	RequireApproval *bool   `json:"require_approval,omitempty"`
	RequireManifest *string `json:"require_manifest,omitempty"`
}

// DefaultAdmission is the effective admission config with nothing set.
func DefaultAdmission() AdmissionConfig {
	return AdmissionConfig{
		MaxDataClass:    dataclass.Default,
		RequireManifest: ManifestWarn,
	}
}

func manifestRank(mode string) (int, bool) {
	switch mode {
	case ManifestOff:
		return 0, true
	case ManifestWarn:
		return 1, true
	case ManifestReject:
		return 2, true
	}
	return 0, false
}

// ValidateMaxDataClass checks a ceiling value (config or flag).
func ValidateMaxDataClass(v string) error {
	if !dataclass.Valid(v) {
		return fmt.Errorf("invalid admission.max_data_class %q (one of: %s)", v, dataclass.Names())
	}
	return nil
}

// ClampMaxDataClass resolves the effective ceiling from the user-level
// value (already defaulted) and the project-level value ("" when
// unset). Project config travels with a clone, so it may only lower the
// ceiling, never raise it.
func ClampMaxDataClass(user, project string) string {
	if project == "" {
		return user
	}
	p, pok := dataclass.Rank(project)
	u, uok := dataclass.Rank(user)
	if pok && uok && p < u {
		return project
	}
	return user
}

// ClampManifestMode is the same rule for require_manifest: a project
// may only raise the requirement (off < warn < reject).
func ClampManifestMode(user, project string) string {
	p, pok := manifestRank(project)
	u, uok := manifestRank(user)
	if pok && uok && p > u {
		return project
	}
	return user
}

// ClampRequireApproval: a project may only turn approval on.
func ClampRequireApproval(user, project bool) bool { return user || project }

// ResolveAdmission layers the user block fully over the defaults, then
// clamps the project block so it can only tighten. Values are
// validated.
func ResolveAdmission(user, project *AdmissionOverrides) (AdmissionConfig, error) {
	out := DefaultAdmission()
	if err := user.validate(); err != nil {
		return AdmissionConfig{}, err
	}
	if err := project.validate(); err != nil {
		return AdmissionConfig{}, err
	}
	if user != nil {
		if user.MaxDataClass != nil {
			out.MaxDataClass = *user.MaxDataClass
		}
		if user.RequireApproval != nil {
			out.RequireApproval = *user.RequireApproval
		}
		if user.RequireManifest != nil {
			out.RequireManifest = *user.RequireManifest
		}
	}
	if project != nil {
		if project.MaxDataClass != nil {
			out.MaxDataClass = ClampMaxDataClass(out.MaxDataClass, *project.MaxDataClass)
		}
		if project.RequireApproval != nil {
			out.RequireApproval = ClampRequireApproval(out.RequireApproval, *project.RequireApproval)
		}
		if project.RequireManifest != nil {
			out.RequireManifest = ClampManifestMode(out.RequireManifest, *project.RequireManifest)
		}
	}
	return out, nil
}

func (o *AdmissionOverrides) validate() error {
	if o == nil {
		return nil
	}
	if o.MaxDataClass != nil {
		if err := ValidateMaxDataClass(*o.MaxDataClass); err != nil {
			return err
		}
	}
	if o.RequireManifest != nil {
		if _, ok := manifestRank(*o.RequireManifest); !ok {
			return fmt.Errorf("invalid admission.require_manifest %q (one of: off, warn, reject)", *o.RequireManifest)
		}
	}
	return nil
}

// validate checks resolved admission values. allowUnset accepts empty
// strings, for a layer loaded without defaults where "" means unset.
func (a AdmissionConfig) validate(allowUnset bool) error {
	if a.MaxDataClass != "" || !allowUnset {
		if err := ValidateMaxDataClass(a.MaxDataClass); err != nil {
			return err
		}
	}
	if a.RequireManifest != "" || !allowUnset {
		if _, ok := manifestRank(a.RequireManifest); !ok {
			return fmt.Errorf("invalid admission.require_manifest %q (one of: off, warn, reject)", a.RequireManifest)
		}
	}
	return nil
}

// loadAdmission resolves the admission block through kit, layer by layer:
//
//  1. defaults + system + user (project layers cleared) -> base
//  2. each folder layer alone, no defaults ("" / false = unset), folded
//     outermost to innermost into base via the Clamp* functions
//  3. extra config files and key=value overrides applied after the
//     clamp, from the clamped value as defaults
//
// layers are config file paths, outermost first. Operator-supplied layers
// (step 3) are trusted and may raise or lower. The path selection in opts
// is the one the base loader uses, so legacy config.json discovery and the
// previous-dir rule apply here too.
func loadAdmission(opts kitconfig.Options, layers []string, paths []string, overrides map[string]any) (AdmissionConfig, error) {
	defaults := defaultConfig()
	baseOpts := opts
	baseOpts.Defaults = &defaults
	baseOpts.ProjectConfigPath = ""
	baseOpts.ExtraConfigPaths = nil
	baseOpts.Overrides = nil
	var base Config
	if err := kitconfig.Load(&base, baseOpts); err != nil {
		return AdmissionConfig{}, err
	}
	if err := base.Admission.validate(false); err != nil {
		return AdmissionConfig{}, err
	}

	clamped := base.Admission
	for _, path := range layers {
		keys, err := readProjectLayer(path)
		if err != nil {
			return AdmissionConfig{}, err
		}
		var layer Config
		if v, ok := keys[projectAdmissionKey]; ok {
			if err := decodeKey(v, &layer.Admission); err != nil {
				return AdmissionConfig{}, fmt.Errorf("load config %s: %w", path, err)
			}
		}
		if err := layer.Admission.validate(true); err != nil {
			return AdmissionConfig{}, fmt.Errorf("%s: %w", path, err)
		}
		clamped = foldAdmission(clamped, layer.Admission)
	}

	var out Config
	tailOpts := kitconfig.Options{
		Defaults:         &Config{Admission: clamped},
		ExtraConfigPaths: paths,
		Overrides:        overrides,
	}
	if err := kitconfig.Load(&out, tailOpts); err != nil {
		return AdmissionConfig{}, err
	}
	if err := out.Admission.validate(false); err != nil {
		return AdmissionConfig{}, err
	}
	return out.Admission, nil
}

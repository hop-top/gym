package install

import (
	"fmt"

	"hop.top/gym/internal/approvals"
	"hop.top/gym/internal/config"
	"hop.top/gym/internal/dataclass"
	"hop.top/gym/internal/registry"
)

// Digest statuses reported in AdmissionResult.DigestStatus.
const (
	DigestMatch    = "match"
	DigestUnpinned = "unpinned"
)

// ApprovalRequiredMissing is the approval_state fact emitted when
// admission.require_approval is on and the skill is unreviewed. It lets
// a deny policy fire without editing policies (a project-level policy
// override cannot weaken an operator config this way).
const ApprovalRequiredMissing = "required-missing"

// AdmissionInput is everything Admit needs. It is read-only: nothing
// is copied or written, so install, upgrade, and `gym guard test` share
// one definition.
type AdmissionInput struct {
	SkillPath string
	Entry     registry.SkillEntry
	Admission config.AdmissionConfig

	// PreviousDataClass is the installed version's class on upgrade.
	PreviousDataClass string

	// ApprovalsPath overrides the user-level approvals file.
	ApprovalsPath string

	// ApprovalState and ExpectDigest simulate operator state
	// (`gym guard test`). Empty means: use the real store / registry.
	ApprovalState string
	ExpectDigest  string
}

// AdmissionResult carries the facts the guard policies adjudicate.
type AdmissionResult struct {
	ManifestPresent bool
	ManifestMode    string // off|warn|reject

	Digest       string
	DigestStatus string // match|unpinned (mismatch is a hard error)
	DigestSource string // registry|operator|""

	DeclaredTools   []string
	UndeclaredTools []string

	DataClass          string
	MaxDataClass       string
	DataClassOver      bool
	DataClassEscalated bool

	ApprovalState string // unreviewed|approved|required-missing
}

// ExpectedDigest returns the pinned digest for entry and where the pin
// came from. Only the registry backend exists today; a lockfile backend
// slots in ahead of it (lockfile wins over registry).
func ExpectedDigest(entry registry.SkillEntry) (digest, source string) {
	if entry.Digest != "" {
		return entry.Digest, "registry"
	}
	return "", ""
}

// Admit computes admission facts for the staged skill at in.SkillPath.
// Hard integrity failures come back as *AdmissionError:
// manifest-invalid, digest-mismatch, digest-unsupported-entry,
// approval-revoked. Everything else is reported in the result for the
// guard policies to adjudicate.
func Admit(in AdmissionInput) (AdmissionResult, error) {
	res := AdmissionResult{
		ManifestMode: in.Admission.RequireManifest,
		MaxDataClass: in.Admission.MaxDataClass,
	}
	if res.ManifestMode == "" {
		res.ManifestMode = config.ManifestWarn
	}
	if res.MaxDataClass == "" {
		res.MaxDataClass = dataclass.Default
	}
	if err := config.ValidateMaxDataClass(res.MaxDataClass); err != nil {
		return res, err
	}

	digest, err := Digest(in.SkillPath)
	if err != nil {
		return res, err
	}
	res.Digest = digest

	expected, source := ExpectedDigest(in.Entry)
	if in.ExpectDigest != "" {
		expected, source = in.ExpectDigest, "operator"
	}
	switch {
	case expected == "":
		res.DigestStatus = DigestUnpinned
	case expected == digest:
		res.DigestStatus, res.DigestSource = DigestMatch, source
	default:
		return res, admissionErrorf(ReasonDigestMismatch,
			"%s pin %s, computed %s", source, expected, digest)
	}

	m, present, err := LoadManifest(in.SkillPath)
	if err != nil {
		return res, err
	}
	res.ManifestPresent = present
	res.DeclaredTools = []string{}
	res.UndeclaredTools = []string{}
	if present {
		used, err := FrontmatterTools(in.SkillPath)
		if err != nil {
			return res, err
		}
		res.DeclaredTools = m.AllowedTools
		res.UndeclaredTools = UndeclaredTools(m.AllowedTools, used)
		res.DataClass = m.DataClass
		res.DataClassOver = dataclass.Over(m.DataClass, res.MaxDataClass)
		res.DataClassEscalated = in.PreviousDataClass != "" &&
			dataclass.Over(m.DataClass, in.PreviousDataClass)
	}

	state, err := resolveApproval(in, digest)
	if err != nil {
		return res, err
	}
	if state == approvals.Revoked {
		return res, admissionErrorf(ReasonApprovalRevoked,
			"%s is revoked at %s", in.Entry.Name, digest)
	}
	res.ApprovalState = string(state)
	if state == approvals.Unreviewed && in.Admission.RequireApproval {
		res.ApprovalState = ApprovalRequiredMissing
	}
	return res, nil
}

func resolveApproval(in AdmissionInput, digest string) (approvals.State, error) {
	if in.ApprovalState != "" {
		if !approvals.ValidState(in.ApprovalState) {
			return "", fmt.Errorf("invalid approval state %q (one of: unreviewed, approved, revoked)", in.ApprovalState)
		}
		return approvals.State(in.ApprovalState), nil
	}
	st, err := approvals.Open(in.ApprovalsPath)
	if err != nil {
		return "", err
	}
	return st.Lookup(in.Entry.Name, digest)
}

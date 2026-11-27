package install

import (
	"fmt"

	"hop.top/gym/internal/approvals"
	"hop.top/gym/internal/config"
	"hop.top/gym/internal/dataclass"
	"hop.top/gym/internal/lock"
	"hop.top/gym/internal/registry"
)

// Digest statuses reported in AdmissionResult.DigestStatus.
const (
	DigestMatch    = "match"
	DigestUnpinned = "unpinned"
	// DigestUnpinnedRequired: no pin, and admission.require_pin demands one
	// for this source kind.
	DigestUnpinnedRequired = "unpinned-required"
)

// Digest pin sources reported in AdmissionResult.DigestSource.
const (
	PinSourceRegistry = "registry"
	PinSourceLockfile = "lockfile"
	PinSourceOperator = "operator"
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

	// ApprovalState simulates operator state (`gym guard test`). Empty
	// means: use the real store.
	ApprovalState string
	// ExpectDigest is the operator's own pin (`learn --digest`,
	// `guard test --expect-digest`). It outranks the lock and the registry.
	ExpectDigest string

	// Lock is the lock entry honoured by this install, nil when none applies.
	Lock *lock.Entry
	// SourceKind is registry|git|dir|archive; it decides whether
	// admission.require_pin applies. Empty counts as dir.
	SourceKind string
	// Requested is what the operator pinned (version or ref); Commit the
	// git commit staged. Both are facts for the policies.
	Requested string
	Commit    string
}

// AdmissionResult carries the facts the guard policies adjudicate.
type AdmissionResult struct {
	ManifestPresent bool
	ManifestMode    string // off|warn|reject

	Digest       string
	DigestStatus string // match|unpinned|unpinned-required (mismatch is a hard error)
	DigestSource string // registry|lockfile|operator|""
	// DigestPinConflict: lock and registry pin different digests for the
	// same version; the lock won.
	DigestPinConflict bool

	// SourceKind, Requested, Resolved (the commit) and LockPresent describe
	// where the skill came from.
	SourceKind  string
	Requested   string
	Resolved    string
	LockPresent bool

	DeclaredTools   []string
	UndeclaredTools []string

	DataClass          string
	MaxDataClass       string
	DataClassOver      bool
	DataClassEscalated bool

	ApprovalState string // unreviewed|approved|required-missing
}

// ExpectedDigest returns the pinned digest for entry and where the pin
// came from: the lock entry first, then the registry entry's digest
// (source "lockfile" / "registry"); none gives "". The operator's own pin
// outranks both and is applied by Admit.
//
// conflict is true only when lock and registry both pin, differ, and
// describe the same version: a registry that has moved on to a newer
// version is not a conflict. The lock wins either way.
func ExpectedDigest(entry registry.SkillEntry, lk *lock.Entry) (digest, source string, conflict bool) {
	if lk != nil && lk.Digest != "" {
		conflict = entry.Digest != "" && entry.Digest != lk.Digest && lk.Version == entry.Version
		return lk.Digest, PinSourceLockfile, conflict
	}
	if entry.Digest != "" {
		return entry.Digest, PinSourceRegistry, false
	}
	return "", "", false
}

// pinRequired reports whether require_pin demands a pin for kind.
func pinRequired(mode, kind string) bool {
	switch mode {
	case config.PinAll:
		return true
	case config.PinRemote:
		return kind == lock.KindRegistry || kind == lock.KindGit || kind == lock.KindArchive
	}
	return false
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

	expected, source, conflict := ExpectedDigest(in.Entry, in.Lock)
	if in.ExpectDigest != "" {
		// The operator's pin decides; the lock/registry disagreement is moot.
		expected, source, conflict = in.ExpectDigest, PinSourceOperator, false
	}
	res.DigestPinConflict = conflict
	res.SourceKind, res.Requested, res.Resolved = in.SourceKind, in.Requested, in.Commit
	res.LockPresent = in.Lock != nil
	switch {
	case expected == "":
		res.DigestStatus = DigestUnpinned
		if pinRequired(in.Admission.RequirePin, in.SourceKind) {
			res.DigestStatus = DigestUnpinnedRequired
		}
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

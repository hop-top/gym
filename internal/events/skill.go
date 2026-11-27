package events

import "hop.top/kit/go/runtime/bus"

// Skill install/upgrade lifecycle topics.
const (
	TopicSkillInstallRequested bus.Topic = "gym.skill.install.requested"
	TopicSkillInstallCompleted bus.Topic = "gym.skill.install.completed"
	TopicSkillInstallRejected  bus.Topic = "gym.skill.install.rejected"

	TopicSkillUpgradePlanned bus.Topic = "gym.skill.upgrade.planned"
	TopicSkillUpgradeApplied bus.Topic = "gym.skill.upgrade.applied"
	TopicSkillUpgradeFailed  bus.Topic = "gym.skill.upgrade.failed"
)

// SkillInstallRequestedPayload is published when an install begins.
// internal/guard replies with policy.guard.install.evaluated
// correlated by RequestID; the install path blocks on the verdict.
//
// The trailing optional fields (Postinstall, ShellHook, Scan,
// LargestBinaryBytes) carry the
// staging-dir scan results that the CEL guard activates on: the install pipeline
// scans the cloned/staged manifest before publishing and forwards the
// fields here. Subscribers tolerate them being empty (zero values).
//
// The admission fields (ManifestPresent .. ApprovalState) carry the
// facts computed by the install admission stage: manifest presence and
// requirement mode, content digest and pin status, declared vs
// undeclared tools, data class vs the operator ceiling, and approval
// state. Ranks stay in Go; the policies see computed booleans. Folder is
// the scope root of a project scope install (empty for global).
type SkillInstallRequestedPayload struct {
	Skill              string   `json:"skill"`
	Version            string   `json:"version,omitempty"`
	SourceURL          string   `json:"source_url"`
	Scope              string   `json:"scope"`
	Folder             string   `json:"folder,omitempty"`
	Paths              []string `json:"paths,omitempty"`
	Postinstall        string   `json:"postinstall,omitempty"`
	ShellHook          string   `json:"shell_hook,omitempty"`
	Scan               string   `json:"scan,omitempty"`
	LargestBinaryBytes int64    `json:"largest_binary_bytes,omitempty"`
	ManifestPresent    bool     `json:"manifest_present,omitempty"`
	ManifestMode       string   `json:"manifest_mode,omitempty"`
	Digest             string   `json:"digest,omitempty"`
	DigestStatus       string   `json:"digest_status,omitempty"`
	DigestSource       string   `json:"digest_source,omitempty"`
	DeclaredTools      []string `json:"declared_tools,omitempty"`
	UndeclaredTools    []string `json:"undeclared_tools,omitempty"`
	DataClass          string   `json:"data_class,omitempty"`
	MaxDataClass       string   `json:"max_data_class,omitempty"`
	DataClassOver      bool     `json:"data_class_over,omitempty"`
	DataClassEscalated bool     `json:"data_class_escalated,omitempty"`
	ApprovalState      string   `json:"approval_state,omitempty"`
	RequestID          string   `json:"request_id"`
}

// SkillInstallCompletedPayload is published after a successful install.
type SkillInstallCompletedPayload struct {
	Skill       string `json:"skill"`
	Version     string `json:"version"`
	Scope       string `json:"scope"`
	Folder      string `json:"folder,omitempty"`
	InstallPath string `json:"install_path"`
	RequestID   string `json:"request_id,omitempty"`
}

// SkillInstallRejectedPayload is published once per install or upgrade
// attempt that ends rejected: a hard admission failure
// (manifest-invalid, digest-mismatch, digest-unsupported-entry,
// approval-revoked), a validator failure (validator-failed), or a
// policy deny (admission-* reasons, else the denying rule name).
// Reason is the stable rejection reason; RequestID is the id the
// attempt used for its gym.skill.install.requested event.
type SkillInstallRejectedPayload struct {
	Skill     string `json:"skill"`
	Scope     string `json:"scope"`
	Reason    string `json:"reason"`
	RequestID string `json:"request_id,omitempty"`
}

// UpgradePlanItem captures a single skill's planned upgrade. Mirrors
// the public shape of internal/upgrade.PlanItem so subscribers can
// reason about it without importing that package.
type UpgradePlanItem struct {
	Skill   string `json:"skill"`
	Scope   string `json:"scope"`
	From    string `json:"from"`
	To      string `json:"to"`
	Source  string `json:"source,omitempty"`
	Curated bool   `json:"curated,omitempty"`
}

// SkillUpgradePlannedPayload is published when `gym train` (or the
// upgrade planner) has decided what to upgrade. Subscribers can veto
// individual items via policy.guard.install.evaluated.
//
// The trailing optional scan fields mirror the install-side scan
// envelope so the same CEL policies fire on upgrade payloads
// (no-curl-bash, no-secret-leak, no-large-binary, etc.). The
// upgrade planner stages each item, scans the staging dir, and
// surfaces the worst-offender values at the plan envelope —
// guard's payloadFromUpgrade activation forwards them.
type SkillUpgradePlannedPayload struct {
	Items []UpgradePlanItem `json:"items"`
	// Folder is the scope root of the plan's project items; empty when the
	// plan holds only global items.
	Folder             string   `json:"folder,omitempty"`
	Postinstall        string   `json:"postinstall,omitempty"`
	ShellHook          string   `json:"shell_hook,omitempty"`
	Scan               string   `json:"scan,omitempty"`
	LargestBinaryBytes int64    `json:"largest_binary_bytes,omitempty"`
	ManifestPresent    bool     `json:"manifest_present,omitempty"`
	ManifestMode       string   `json:"manifest_mode,omitempty"`
	Digest             string   `json:"digest,omitempty"`
	DigestStatus       string   `json:"digest_status,omitempty"`
	DigestSource       string   `json:"digest_source,omitempty"`
	DeclaredTools      []string `json:"declared_tools,omitempty"`
	UndeclaredTools    []string `json:"undeclared_tools,omitempty"`
	DataClass          string   `json:"data_class,omitempty"`
	MaxDataClass       string   `json:"max_data_class,omitempty"`
	DataClassOver      bool     `json:"data_class_over,omitempty"`
	DataClassEscalated bool     `json:"data_class_escalated,omitempty"`
	ApprovalState      string   `json:"approval_state,omitempty"`
	RequestID          string   `json:"request_id"`
}

// SkillUpgradeAppliedPayload is published per-skill after a successful
// upgrade.
type SkillUpgradeAppliedPayload struct {
	Skill     string `json:"skill"`
	Scope     string `json:"scope"`
	From      string `json:"from"`
	To        string `json:"to"`
	RequestID string `json:"request_id,omitempty"`
}

// SkillUpgradeFailedPayload is published per-skill when an upgrade
// errored out.
type SkillUpgradeFailedPayload struct {
	Skill     string `json:"skill"`
	Scope     string `json:"scope"`
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

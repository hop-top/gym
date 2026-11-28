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

	TopicSkillUninstallRequested bus.Topic = "gym.skill.uninstall.requested"
	TopicSkillUninstallCompleted bus.Topic = "gym.skill.uninstall.completed"
	TopicSkillUninstallFailed    bus.Topic = "gym.skill.uninstall.failed"
)

// SkillInstallRequestedPayload is published when an install begins.
// internal/guard replies with policy.guard.install.evaluated
// correlated by RequestID; the install path blocks on the verdict.
//
// The trailing optional fields (Postinstall, ShellHook, Scan, Scripts,
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
// digest_source is registry|lockfile|operator; digest_status is
// match|unpinned|unpinned-required.
type SkillInstallRequestedPayload struct {
	Skill     string `json:"skill"`
	Version   string `json:"version,omitempty"`
	SourceURL string `json:"source_url"`
	Scope     string `json:"scope"`
	Folder    string `json:"folder,omitempty"`
	// PolicyRoot is the scope root whose policy chain gates the request:
	// the scope being operated on, which for a global item upgraded
	// alongside a project scope is that project scope, not Folder. It
	// selects the policy set and is never offered to policies. Empty: the
	// command's own chain.
	PolicyRoot         string   `json:"policy_root,omitempty"`
	Paths              []string `json:"paths,omitempty"`
	Postinstall        string   `json:"postinstall,omitempty"`
	ShellHook          string   `json:"shell_hook,omitempty"`
	Scan               string   `json:"scan,omitempty"`
	Scripts            []string `json:"scripts,omitempty"`
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
	// Where the skill came from: registry|git|dir|archive, what the operator pinned
	// (version or ref), the git commit staged, whether a lock entry applied,
	// and whether lock and registry disagreed about its digest.
	SourceKind string `json:"source_kind,omitempty"`
	// SourceHost is the network host the source is fetched from; empty
	// for a local source.
	SourceHost        string `json:"source_host,omitempty"`
	Requested         string `json:"requested,omitempty"`
	Resolved          string `json:"resolved,omitempty"`
	LockPresent       bool   `json:"lock_present,omitempty"`
	DigestPinConflict bool   `json:"digest_pin_conflict,omitempty"`
	RequestID         string `json:"request_id"`
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
	Folder string `json:"folder,omitempty"`
	// PolicyRoot is the scope root whose policy chain gates the plan (see
	// SkillInstallRequestedPayload.PolicyRoot).
	PolicyRoot         string   `json:"policy_root,omitempty"`
	Postinstall        string   `json:"postinstall,omitempty"`
	ShellHook          string   `json:"shell_hook,omitempty"`
	Scan               string   `json:"scan,omitempty"`
	Scripts            []string `json:"scripts,omitempty"`
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
	// Where the skill came from: registry|git|dir|archive, what the operator pinned
	// (version or ref), the git commit staged, whether a lock entry applied,
	// and whether lock and registry disagreed about its digest.
	SourceKind string `json:"source_kind,omitempty"`
	// SourceHost is the network host the source is fetched from; empty
	// for a local source.
	SourceHost        string `json:"source_host,omitempty"`
	Requested         string `json:"requested,omitempty"`
	Resolved          string `json:"resolved,omitempty"`
	LockPresent       bool   `json:"lock_present,omitempty"`
	DigestPinConflict bool   `json:"digest_pin_conflict,omitempty"`
	RequestID         string `json:"request_id"`
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

// SkillUninstallRequestedPayload is published when `gym unlearn` starts
// removing a skill. Informational: the guard does not gate uninstall.
// Folder is the scope root (empty for global scope); Paths are the
// install directories about to be removed.
type SkillUninstallRequestedPayload struct {
	Skill     string   `json:"skill"`
	Scope     string   `json:"scope"`
	Folder    string   `json:"folder,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	RequestID string   `json:"request_id"`
}

// SkillUninstallCompletedPayload is published once a skill's records and
// directories are all gone. Paths are the directories removed.
type SkillUninstallCompletedPayload struct {
	Skill     string   `json:"skill"`
	Scope     string   `json:"scope"`
	Folder    string   `json:"folder,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	RequestID string   `json:"request_id"`
}

// SkillUninstallFailedPayload is published when at least one record of a
// skill could not be removed. Reason is the stable reason of the first
// failure (install-path-untrusted, skill-modified, remove-failed, ...).
type SkillUninstallFailedPayload struct {
	Skill     string `json:"skill"`
	Scope     string `json:"scope"`
	Reason    string `json:"reason"`
	RequestID string `json:"request_id"`
}

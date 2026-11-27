# ADR-0002: Skill Manifest Admission (schema, digest, tools, data class, approval)

**Status**: Accepted  
**Date**: 2026-11-27  
**Deciders**: Idea Crafters team  

## Context

Install-time checks today:

- Validator: `skills-ref validate {path}` (`internal/install/install.go`, `defaultValidator`). Checks SKILL.md frontmatter only. Out of scope here except that admission must not depend on it.
- Guard: CEL policies on `gym.skill.install.requested` (ADR-0001). Policies key on `postinstall`, `shell_hook`, `scan`, `largest_binary_bytes`, `paths`. All heuristics over strings the install pipeline lifts from staging (`internal/install/scan.go`).
- Nothing verifies: manifest schema, content integrity, tool declarations, data classification, approval.

Gaps found while reading the code:

1. `ScanStaging` read `gym.json | manifest.json | skill.json` from the skill itself, including `allow_postinstall` (and `large_binary`). Author could switch off the `no-postinstall-script` / `no-large-binary` policies. Same self-approval hazard this ADR must not repeat. Closed: `ScanStaging` no longer reads either field; they are ignored and the payload keys stay always false.
2. `gym train` gates the plan before staging (`gateUpgradePlan`: skill, scope, from, source only), then calls `InstallSkill` with no `Gate`. Upgrades never see scan data or any content-derived fact.
3. `stageSource` returns a local directory source as-is: no copy. Validate, scan, gate, copy read the live tree at four different times (TOCTOU).
4. Gate fails open on missing bus and on timeout unless `--strict` (`internal/guard/gate.go`). `--insecure` lowers deny to warn.
5. `gym.skill.install.rejected` has a topic, payload, and a contract doc, but no production publisher. Only the e2e test publishes it.
6. `App.Run` returns 1 for every error. No exit-code taxonomy.
7. `gym guard test <target>` parses a JSON manifest file and evaluates it raw as the payload. It runs neither staging scan nor validator, and a skill directory without a `gym.json`-style file errors out.
8. Project-level policy overrides (`<root>/.gym/policies`) can disable or relax built-in policies (package doc, `internal/guard/doc.go`). A cloned repository can weaken its own admission. Since closed: project policies are add-only.

Requirement: skill validation verifies a declarative manifest schema, a SHA-256 content digest, declared `allowed_tools` with no undeclared tools, a `data_class` not above the operator's ceiling, and `approval_state`.

No spec exists for this manifest in gym. Prior art consulted for vocabulary only:

- Agent Skills spec: SKILL.md frontmatter fields `name`, `description`, `license`, `compatibility`, `metadata` (string-to-string map), `allowed-tools` (space-separated string, experimental, e.g. `Bash(git:*) Read`). Fixed field list.
- Data-classification vocabularies: an ordered ladder `public, internal, confidential, personal, special_category`, where the effective level is the higher of the type and the record.
- Closed object shapes: unknown un-namespaced fields rejected, lists sorted and de-duplicated.
- Admission practice: digests are SHA-256 over canonical bytes; self-attestation is prohibited for approval decisions.
- gym has no data-class ceiling today. This ADR defines it.

## Decision

Add an **admission stage** to the install path. Go code computes facts from the staged tree and local operator state; hard integrity failures are Go errors; everything else is adjudicated by built-in CEL policies fed the computed facts, so existing override, `--strict`, `--insecure`, and `gym guard test` semantics apply.

Summary of decisions (detail per section below):

| # | Topic | Decision |
|---|-------|----------------|
| 1 | Manifest | Sidecar `gym.json` (already scanned) with `schema` version; strict Go decode; standard `allowed-tools` stays in SKILL.md and must agree |
| 2 | Digest | Sorted file-list digest `sha256:<hex>`; expected value from lockfile pin, else registry entry; never inside the covered content |
| 3 | Tools | Declared set must cover SKILL.md `allowed-tools`; script scanning is advisory only |
| 4 | Data class | Five-level ladder; ceiling from config, default `internal`; project config may only lower |
| 5 | Approval | `unreviewed|approved|revoked`; operator-local store keyed by name+digest; never author-settable |
| 6 | Enforcement | New `admission` stage in `internal/install`; integrity hard-fails in Go; rest via policies; per-skill gate on `train` |
| 7 | No manifest | Configurable `warn|reject|off`; default `warn` |

## 1. Manifest location and format

**Recommendation**: sidecar `gym.json` at the skill root. SKILL.md is not extended.

Why not SKILL.md frontmatter:

- The spec fixes the top-level field set. New top-level keys risk rejection by strict validators.
- `metadata` is allowed but typed string-to-string. YAML decoding coerces scalars (`v: 1.0` and `ok: true` decode into `map[string]string` as `"1.0"`, `"true"`); a nested map errors. Lists (`allowed_tools`) would need string-packing.
- yaml.v3 `KnownFields(true)` rejects unknown keys and a sequence where a string is expected. Good for strictness, but `warmup.Metadata` is non-strict today; reusing it would mean two behaviors.

Why `gym.json` among the three names `ScanStaging` already probes (`gym.json`, `manifest.json`, `skill.json`): it is gym-owned, already read at install, already accepted by `gym guard test`. `manifest.json` and `skill.json` are generic names that other tooling may own; admission reads `gym.json` only, and `ScanStaging` keeps its three-name probe for legacy fields.

Alternatives:

| Option | Verdict |
|--------|---------|
| Frontmatter top-level keys | Rejected: breaks spec closedness |
| Frontmatter `metadata.gym.*` string keys | Rejected: no lists; stringly typed |
| Sidecar `gym.json` | Chosen |
| Sidecar YAML | Rejected: second parser story; JSON already used by the registry |
| Registry-only declaration | Rejected for tools/class (see below); kept for digest |

Schema (author-supplied file, closed object, `schema` required):

```json
{
  "schema": "gym.skill/v1",
  "allowed_tools": ["Read", "Bash(git:*)"],
  "data_class": "internal"
}
```

| Field | Type | Rules |
|-------|------|-------|
| `schema` | string | Required. Exactly `gym.skill/v1`. Unknown major rejected |
| `allowed_tools` | string[] | Required (may be empty). Tool tokens per Agent Skills syntax: `Name` or `Name(arg)`. No whitespace inside a token. No duplicates. Sorted bytewise on canonical output, any order on input |
| `data_class` | string | Required. One of the ladder in section 4 |
| existing `postinstall`, `shell_hook` | as today | Read by `ScanStaging` |
| legacy `allow_postinstall`, `large_binary` | bool | Accepted, ignored |

Fields an author MUST NOT set: `digest`, `approval_state`. Closed-object decoding turns `digest`/`approval_state` into `manifest-invalid` with an explicit message ("set by operator, not author"). `allow_postinstall` and `large_binary` are accepted in `gym.json` but ignored (see gap 1, closed); they never weaken a policy.

Schema validation approach: Go struct + `json.Decoder.DisallowUnknownFields` + explicit checks. No new dependency. A published JSON Schema document for authors is a follow-up, not a gate. Alternative (embedded JSON Schema + validator library): rejected for v1; adds a dependency for four fields.

Cross-file rule: SKILL.md `allowed-tools` is parsed as a space-separated string (spec). Every token must be covered by the manifest (section 3). Frontmatter keeps the standard field; the manifest is the declaration gym enforces.

## 2. Digest

**What is hashed**: the skill root (staging dir joined with `entry.Path`).

Algorithm, `gym.digest/v1`:

1. Walk the tree. Skip the directory `.git` only. Rationale: `ScanStaging` skips `node_modules`/`vendor`; admission MUST NOT, since skipped content is unhashed content.
2. Regular files only. Symlink: reject install (`digest-unsupported-entry`). Other irregular entries: reject. Empty directories ignored.
3. Per file: `sha256(raw bytes)`. No newline, charset, or Unicode normalization.
4. Listing: one line per file, `<x|-> <hex> <relpath>\n`. `x` if owner-exec bit set, else `-`. Other mode bits ignored (clones apply umask). `relpath` is slash-separated, relative to skill root, UTF-8 as stored, no normalization.
5. Lines sorted bytewise on `relpath`.
6. Digest = `sha256(listing)`. String form `sha256:<lowercase hex>`.

Encoding: `sha256:<hex>`, the OCI/Subresource-style `algo:hex` form. Hex is chosen over unpadded base64url for greppability and `sha256sum` cross-checks; the algorithm prefix leaves room for others.

Alternatives for the hashed bytes:

| Option | Verdict |
|--------|---------|
| tar/zip archive hash | Rejected: archive metadata (mtime, uid) makes it non-reproducible |
| git tree/commit SHA | Rejected: SHA-1 object format by default, only for git sources, not local dirs |
| Per-file manifest list published alongside | Rejected for v1; the listing is derivable. Expose with `gym digest --files <dir>` for diagnosis |
| Canonical-JSON hash of listing | Rejected: no need; line format is trivially canonical |

Known gap: git checkout filters (`autocrlf`, `eol` attributes) can change bytes between platforms. Documented limitation; no normalization.

**Circularity**. A digest stored in the content it covers cannot be checked. Therefore the manifest has no `digest` field (rejected on parse, section 1) and the expected digest lives outside the tree:

| Source | Trust | Where |
|--------|-------|-------|
| Lockfile pin | Operator/project decision | Lockfile `.gym/lock.json` (ADR-0003). Entry `{name, kind, source, requested, version, commit, path, registry, digest}` |
| Registry entry | Index publisher (https, curated flag) | New optional `SkillEntry.digest` (`json:"digest,omitempty"`) |
| Detached file next to source (`<skill>.sha256`) | Same as source | Rejected: same origin as content, adds no trust |
| None | Unpinned | Computed and recorded only |

Precedence: lockfile > registry > none. Both present and different: lockfile wins, finding `digest-pin-conflict` (warn). Conflict handling shipped with the lockfile (ADR-0003 section 2.4): a conflict needs lock and registry to describe the same version; an operator `--digest` outranks both.

Outcomes, reported as `digest_status`:

- `match`: allow.
- `mismatch`: hard fail in Go, reason `digest-mismatch`. Not relaxable by `--insecure` and not dependent on the bus (gap 4).
- `unpinned`: policy `digest-unpinned`, default warn. Computed digest is recorded in installed-skill state and published in events so the lockfile can adopt it later (trust on first use is an explicit operator step, not implicit).

Time-of-check fix (gap 3): hash the staged tree, copy, then re-hash the destination and compare before reporting success; on mismatch remove the destination and fail. Cheap, covers local-directory sources mutated mid-install.

A lockfile that does not exist yet is not a blocker: define `ExpectedDigest(entry) (digest, source string)` in `internal/install`, backed first by the registry field; the lockfile ADR adds a second backend.

`gym digest <dir>` (print digest) is recommended so registry maintainers and authors can produce the value. Small, no policy.

## 3. `allowed_tools`

**Definition of "undeclared tool"** (v1, enforceable): a tool token that the skill itself grants or references in a machine-readable place and that the manifest `allowed_tools` does not cover.

Sources checked:

1. SKILL.md frontmatter `allowed-tools` tokens (parsed by `internal/warmup/extract.go`). This is the real grant: agent runtimes honoring `allowed-tools` pre-approve exactly these.
2. Nothing else is enforced.

Coverage relation `declared D covers used U`:

- Same tool name, and D has no argument: covers any U with that name (`Bash` covers `Bash(git:status)`).
- D `Bash(git:*)` covers `Bash(git:` + anything. Wildcard only as a trailing `*` after `:`; no other globbing.
- Otherwise exact, case-sensitive.
- Manifest that declares bare `Bash` is legal but yields finding `tools-unrestricted` (warn): it declares nothing narrower than "any shell".

Result: `undeclared_tools = frontmatter tokens not covered`. Non-empty means deny by policy `tools-undeclared`.

**What is not detectable (be honest)**:

- Runtime tool use. gym is not in the agent's execution path and cannot observe calls.
- What the SKILL.md body instructs the model to do ("run curl ..."). Natural language.
- What scripts do after interpretation (`eval`, downloaded code, interpreter indirection).

Optional advisory heuristic, off by default, finding severity warn: scan `scripts/*` shebang and first-token commands in shell files; report commands not matched by any `Bash(cmd:*)` declaration. Expect a high false-negative and moderate false-positive rate. Never deny on it. Not part of the first implementation.

Operator side: an `admission.allowed_tools` allowlist (token patterns, same coverage relation) is deferred. The requirement asks only for undeclared tools.

Alternatives:

| Option | Verdict |
|--------|---------|
| Frontmatter-vs-manifest consistency only | Chosen v1 |
| Heuristic script scan as deny | Rejected: unsound both directions |
| Drop `allowed_tools` from the manifest, trust frontmatter | Rejected: nothing for the digest-pinned, operator-reviewed record to compare against; reviewers read one small file |
| Sandbox/trace at install | Out of scope |

## 4. `data_class`

**Ladder** (ordered, low to high):

`public` < `internal` < `confidential` < `personal` < `special_category`

Semantics: highest class of data the skill is designed to read, produce, or transmit. A skill classed `personal` may be installed only where the ceiling is `personal` or above.

**Ceiling**: no profile or config concept in gym defines one. Define `admission.max_data_class` in gym config (`internal/config`, same layered `config.yaml` files).

Resolution:

Layers, lowest to highest: defaults < system < user < project (clamped) < `-c <file>` < `-c key=value` < `--max-data-class <level>`.

1. Default when unset: `internal`.
2. System and user config `admission.max_data_class`.
3. Project config: may only lower relative to what defaults, system and user resolve to. Layers are loaded separately and the project value is clamped, since the kit loader overwrites scalars; otherwise a cloned repo's config raises the ceiling (same class as gap 8).
4. `-c <file>` and `-c key=value` (operator on the command line, trusted like the flag; may raise or lower), applied after the clamp.
5. `--max-data-class <level>` flag (may raise or lower).

Behavior matrix:

| Skill `data_class` | Ceiling | Result |
|--------------------|---------|--------|
| valid, rank <= ceiling | any | allow |
| valid, rank > ceiling | any | deny, reason `data-class-exceeds-ceiling` (policy; `--insecure` lowers to warn like any deny) |
| absent in manifest | any | `manifest-invalid` (field required) |
| unknown string | any | `manifest-invalid` |
| no manifest | any | section 7 |
| any | invalid config value | config error, exit before install |

Rationale for default `internal` rather than "no ceiling": silent absence of a ceiling would make the check vacuous for every operator who never configured it. Cost: `confidential` skills need one config line.

Optional rider (cheap, recommended): on upgrade, `data_class` rising above the installed version's class yields warn `data-class-escalated`, even if under ceiling. Requires the class in installed-skill state.

## 5. `approval_state`

**Values**: `unreviewed` | `approved` | `revoked`.

- `unreviewed`: no approval record for this name+digest. Default.
- `approved`: operator recorded approval for this exact digest.
- `revoked`: operator recorded revocation for this name (any digest) or this digest.

**Who sets it**: the operator only. Never the author, never the skill's own tree, never the project tree.

Trust model:

- Not in `gym.json`: a manifest containing `approval_state` is rejected (section 1).
- Registry-supplied approval is ignored in v1. A registry is a publisher, and approval decisions may not be self-attested. A later ADR may allow signed attestations from configured approvers.
- Stored in an operator-local file under the user config directory (`<config-dir>/gym/approvals.json`, resolved through `internal/xdg`). Not project-level: a project file travels with a clone and a hostile repo would ship its own approvals (same reasoning as gap 8). The local file is trusted as the operator's own and is not signed; tamper-resistance against a local attacker is out of scope.
- Record: `{skill, source_url, digest, state, by, at}`. `by` defaults to `$USER`. Binding to digest means any content change returns the skill to `unreviewed`.
- Set via `gym guard approve <skill|dir> [--digest d]` and `gym guard revoke`, under `guard` since it is admission state.

Policy:

- `revoked`: Go hard fail, reason `approval-revoked`. Not relaxable.
- `unreviewed`: policy `approval-required`. Default effect warn; `admission.require_approval: true` flips to deny. `--strict` turns the warn into deny via existing gate semantics.
- `approved`: allow.

Alternatives: author-set field with registry vouching (rejected: self-approval); signed approvals now (rejected: key management scope); states `pending|approved|rejected` with workflow (rejected: no workflow engine in gym; `unreviewed` is the absence of a record).

## 6. Enforcement point

**Options**:

| Option | Pros | Cons |
|--------|------|------|
| A. Extend validator step (`ValidateSkillPackage`) | Runs on every install and upgrade; Go-native hashing | Validator is an external command today; mixes operator-configurable validator with built-in admission; no policy override or warn tier |
| B. CEL guard policies only | Matches ADR-0001; overridable; `guard test` works | CEL cannot hash files; guard is fail-open on timeout and no bus; upgrade path carries no content facts (gaps 2, 4) |
| C. New admission stage + facts to policies | Hard failures where integrity matters; policy flexibility where judgment matters | One more stage to maintain |

**Recommendation: C.**

Pipeline order inside `InstallSkill` / `InstallSkillMulti`:

1. `stageSource`
2. `ValidateSkillPackage` (unchanged)
3. **Admission (new, `internal/install/admission.go`)**: parse `gym.json`, parse frontmatter, compute digest, resolve expected digest, compute tool coverage, class rank vs ceiling, approval lookup. Output `AdmissionResult`.
4. Hard failures return `*AdmissionError{Reason}`: `manifest-invalid`, `digest-mismatch`, `approval-revoked`, `digest-unsupported-entry`.
5. `Gate` (existing) receives `AdmissionResult` facts; CEL policies adjudicate the rest.
6. Copy; re-hash destination (section 2).

Because step 3 runs inside `InstallSkill` itself, `gym train` is covered even though it passes no `Gate` (gap 2). For CEL on upgrade, additionally pass the per-skill `Gate` in `train` (the batch plan gate stays). Double evaluation is idempotent by `request_id`.

**Payload fields** added to `SkillInstallRequestedPayload` and mirrored in the upgrade payload (all `omitempty`; guard's `payloadFromInstall`/`payloadFromUpgrade` must default every key so policies can skip `has()`):

| Field | Type | Meaning |
|-------|------|---------|
| `manifest_present` | bool | `gym.json` found and valid |
| `digest` | string | computed `sha256:<hex>` |
| `digest_status` | string | `match` / `mismatch` / `unpinned` |
| `digest_source` | string | `lockfile` / `registry` / `` (`operator` for a `gym guard test --expect-digest` simulation) |
| `declared_tools` | string[] | manifest `allowed_tools` |
| `undeclared_tools` | string[] | uncovered frontmatter tokens |
| `data_class` | string | manifest value or `` |
| `max_data_class` | string | resolved ceiling |
| `data_class_over` | bool | rank(class) > rank(ceiling) |
| `approval_state` | string | resolved state |
| `manifest_mode` | string | `off` / `warn` / `reject` |
| `data_class_escalated` | bool | upgrade raises the class above the installed version's |

Computed booleans keep CEL trivial. Ranks stay in Go (single ladder definition).

**Built-in policies** (YAML next to existing ones):

| Policy | When | Effect |
|--------|------|--------|
| `admission-tools-undeclared` | `size(payload.undeclared_tools) > 0` | deny |
| `admission-data-class` | `payload.data_class_over` | deny |
| `admission-digest-unpinned` | `payload.digest_status == 'unpinned'` | warn |
| `admission-approval-required` | `payload.approval_state == 'unreviewed'` | warn |
| `admission-approval-enforced` | `payload.approval_state == 'required-missing'` | deny |
| `admission-manifest-missing` | `!payload.manifest_present && payload.manifest_mode == 'warn'` | warn |
| `admission-manifest-required` | `!payload.manifest_present && payload.manifest_mode == 'reject'` | deny |
| `admission-data-class-escalated` | `payload.data_class_escalated` | warn |
| `admission-tools-unrestricted` | declared contains bare `Bash` | warn |

`require_approval` and `require_manifest` change effect by emitting different fact values (`approval_state` becomes `required-missing`), not by editing policies, so a project-level policy override cannot silently weaken an operator config.

**Rejection reasons** (stable strings, reuse `SkillInstallRejectedPayload.Reason`):

`manifest-invalid`, `manifest-missing`, `digest-mismatch`, `digest-unsupported-entry`, `tools-undeclared`, `data-class-exceeds-ceiling`, `approval-required`, `approval-revoked`, `validator-failed`; a non-admission policy deny carries the denying rule's name.

Policy denies reach the user as `ErrPolicyDeny` (typed `PolicyDenyError`, with findings) and rule names in the message; the reason string is the rule name minus the `admission-` prefix, except `admission-data-class` (`data-class-exceeds-ceiling`), `admission-approval-enforced` (`approval-required`), and `admission-manifest-required` (`manifest-missing`).

Gap 5 fix: `gym.skill.install.rejected` is published from the production install path (`learn`, `train`) for every rejected install or upgrade attempt, before returning the error: admission failures, validator failures (`validator-failed`), and non-admission policy denies (reason = denying rule name). `--insecure`-lowered denies and non-rejection errors (source fetch, I/O) publish nothing. The `request_id` matches the requested event of the same attempt; failures before the gate (hard admission failures, validator failure) have no matching requested event and carry the attempt's own id.

**Exit codes**: keep exit 1 for every rejection (current `App.Run` behavior). Distinguishing codes would be a cross-cutting change to `Run` and every command. The reason is carried in stderr and the rejected event.

**`gym guard test`**:

- Target is a directory (or `SKILL.md`): run the same admission function read-only (no copy, no install), build the same payload fields as install, evaluate policies, print verdict + findings + computed facts (`digest`, `undeclared_tools`, ...). Shares code with the install path so behavior cannot drift.
- Flags to simulate operator state: `--max-data-class`, `--approval-state`, `--expect-digest`.
- Target is a JSON file: unchanged raw-payload mode, which lets policy authors unit-test with hand-written facts.
- Exit nonzero on deny (existing behavior).
- Should also stop requiring a `gym.json`-style file when given a directory: absence is evaluated under section 7.

## 7. Skills with no manifest

Most Agent Skills carry only SKILL.md; the spec does not define a manifest.

Config `admission.require_manifest`: `off` | `warn` | `reject`.

| Mode | Behavior |
|------|----------|
| `off` | No finding |
| `warn` (default) | Finding `manifest-missing` (warn). Tools, data class checks skipped. `--strict` makes it deny via existing warn-to-deny flip |
| `reject` | Deny `manifest-missing` |

Checks that need no manifest still run: digest (computed; pinned comparison if a pin exists) and approval. `data_class` is "unclassified": with a ceiling configured, an unclassified skill is not silently treated as `public`; it surfaces only the manifest-missing finding, since the class is unknowable.

Alternatives: always reject (rejected: excludes almost all existing skills; adoption cliff); always warn (rejected: no way for a locked-down operator to require it).

## Consequences

### Positive

- One admission definition shared by install, upgrade, and dry-run.
- Integrity is not dependent on the bus, timeouts, or relaxable policies.
- Closed-object manifest and a single ladder definition keep the schema small and testable.
- Existing override, strict, and insecure semantics reused for judgment checks.
- Upgrade path stops bypassing content checks.

### Negative

- New files and state: `gym.json` schema, approvals file, registry `digest` field, installed-state digest field.
- Tool check is consistency-only; cannot detect actual misuse. Must be documented as such, or operators will over-trust it.
- Digest has no checkout-filter normalization; cross-platform mismatches are possible for text files.
- Default ceiling `internal` is a stricter default than any current behavior.
- `allow_postinstall` / `large_binary` in the author manifest are ignored; the self-approval channel (gap 1) is closed. Operators allow postinstall scripts or large binaries by relaxing or disabling the policy in their user policies dir.

### Neutral

- Lockfile is a future second backend for expected digests; interface is defined now.
- Signed approvals and registry attestations are deferred.

## Test strategy (outline)

| # | Case | Layer | Expect |
|---|------|-------|--------|
| 1 | Valid `gym.json`, matching frontmatter | unit | allow, no findings |
| 2 | Unknown top-level key in manifest | unit | `manifest-invalid` |
| 3 | `approval_state` / `digest` in manifest | unit | `manifest-invalid`, message names operator-only |
| 4 | Missing `data_class`; unknown class; wrong `schema` | unit | `manifest-invalid` each |
| 5 | Malformed JSON; duplicate tools; token with whitespace | unit | `manifest-invalid` |
| 6 | Digest stable under file creation order and mtime | unit | same digest |
| 7 | Digest changes on: content byte, rename, exec bit; unchanged on non-exec mode change | unit | as stated |
| 8 | `.git` ignored; `node_modules` included | unit | digest differs when node_modules changes |
| 9 | Symlink in tree | unit | `digest-unsupported-entry` |
| 10 | Pin matches / differs / absent | unit | match / `digest-mismatch` hard fail / unpinned warn |
| 11 | Lockfile pin vs registry digest conflict | unit | lockfile wins, conflict warn (closed by ADR-0003 test B6; B7: a newer registry version is no conflict) |
| 12 | Local-dir source mutated between hash and copy | integration | re-hash fails, dest removed |
| 13 | Frontmatter `Read Bash(git:*)`, manifest same | unit | allow |
| 14 | Frontmatter token absent from manifest | unit | `tools-undeclared` deny |
| 15 | `Bash(git:status)` vs declared `Bash(git:*)`; vs `Bash`; vs `Bash(npm:*)` | unit | covered / covered / uncovered |
| 16 | Declared bare `Bash` | unit | `tools-unrestricted` warn |
| 17 | `allowed-tools` as YAML list, not string | unit | frontmatter parse error surfaced as `manifest-invalid` |
| 18 | Class ladder boundaries: equal, one above, `special_category` vs each ceiling | unit | allow / deny |
| 19 | Ceiling unset | unit | default `internal` |
| 20 | Project config tries to raise user ceiling | unit | min-merge keeps user value |
| 21 | `--max-data-class` flag overrides config | integration | flag wins |
| 22 | Approval none / matching digest / other digest / revoked | unit | warn / allow / warn / hard fail |
| 23 | `require_approval: true`, unreviewed | integration | deny |
| 24 | Approval file in project tree | unit | ignored |
| 25 | No manifest under `off`, `warn`, `reject`; with `--strict` | integration | none / warn / deny / deny |
| 26 | Digest and approval still checked without manifest | integration | findings present |
| 27 | `--insecure` with data-class deny | integration | warn, install proceeds |
| 28 | `--insecure` with digest mismatch | integration | still fails |
| 29 | Bus absent / gate timeout | integration | hard failures still enforced; policy findings fail-open, deny under `--strict` |
| 30 | `gym train` upgrade to a version with undeclared tool | integration | rejected, nothing written |
| 31 | Rejected event published once per failure with reason and `request_id` | integration | matches contract |
| 32 | Payload round-trip with new fields; defaults for each key present in CEL activation | unit | topics round-trip test extended |
| 33 | `gym guard test <dir>` equals install verdict for the same tree | integration | same verdict and findings |
| 34 | `gym guard test <file.json>` raw mode | unit | unchanged |

Each enforcement test is mutation-checked: remove or invert the check, confirm the guarding test fails, restore.

## Decisions

1. Data-class ladder is `public < internal < confidential < personal < special_category`.
2. The default ceiling is `internal` when unset.
3. Digest strings are `sha256:<lowercase hex>`.
4. Exit code stays 1 for every rejection.
5. The operator tool allowlist (`admission.allowed_tools`) is deferred.
6. Approval commands live under guard: `gym guard approve`, `gym guard revoke`.
7. Author-set `allow_postinstall` / `large_binary` are no longer read by `internal/install/scan.go`; closed.
8. `gym train` passes a per-skill gate.
9. The validator default is out of scope here; it was corrected separately to `skills-ref validate`.

## Implementation Notes

- New: `internal/install/admission.go` (+ digest, tools, class helpers), `internal/guard/policies/admission-*.yaml`, approvals store, config keys under `admission`.
- Touch: `internal/events/skill.go` (payload), `internal/guard/guard.go` (activation defaults for install and upgrade), `internal/cli/guard.go` (gate payload, `guard test`), `internal/cli/app.go` (`train` gate), `internal/registry/registry.go` (`digest`), installed-state record (digest, data_class), `internal/config/config.go` (min-merge for ceiling).
- Docs: `docs/dev.md` holds the payload field table and rejection reasons; `docs/user.md` the operator behaviour.

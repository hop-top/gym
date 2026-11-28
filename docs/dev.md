# gym: Dev Guide

This is the maintainer/contributor view of `gym`.

## Build and Test

```bash
go build -o gym ./cmd/gym
go test ./...
go vet ./...
gofmt -w .
```

Or:

```bash
make build
make test
make vet
make fmt
```

## Architecture (packages)

- `cmd/gym`: main entrypoint
- `internal/cli`: Cobra command tree + Viper CLI config/env + TUI/REPL
- `internal/config`: user/project config merge; mutation helpers
- `internal/project`: project root detection (`DetectRoot`, `IsRoot`); `.gym/` dir constants
- `internal/scope`: `Chain(dir)`, the folder scope roots that apply to a directory, with the ownership and `$HOME` rules
- `internal/registry`: `skills.json` fetch/merge; curated labeling; index store
- `internal/install`: stage source (git clone / local dir); validate (`skills-ref`); copy into install path; scan for policies
- `internal/state`: installed skill store (global + project)
- `internal/upgrade`: upgrade planner (scope + mirror policy)
- `internal/serve`: serve `skills.json` + debug `_index.json`
- `internal/ide`: detect IDE/TDE standard paths (global + project)
- `internal/xdg`: XDG directory resolution + mkdirs (thin wrapper over kit/core/xdg)
- `internal/events/`: event payload structs + topic constants (coach, skill, warmup, toolspec, external subscribers)
- `internal/eventbus/`: kit/runtime/bus wrapper; adapter selection + request/response helpers
- `internal/judge/`: rsx-backed coach judging subscriber; cache; subprocess driver
- `internal/guard/`: CEL policy gate subscriber; loader for built-in policies + user overrides + add-only project policies
- `internal/guard/policies/`: baked-in CEL policies (default YAML files, including `admission-*`)
- `internal/cheat/`: toolspec registry builder; gym self-spec; skill specs from manifests; tip adapter writer; optional tip bridge subscriber
- `e2e/`: integration tests with xrr cassettes (build-tag gated, `go test -tags cassettes ./e2e/...`)

## Config + Precedence

- Loader: `internal/config.LoadMergedConfig` delegates to `hop.top/kit/go/core/config` `Load`; typed defaults via `Options.Defaults`; no gym-side merge.
- Config sources (YAML; paths from kit's `OptionsForToolWithMarkersFrom`):
  - system: `/etc/gym/config.yaml`
  - user: `config.yaml` in `$XDG_CONFIG_HOME/gym` (else OS default: Linux `~/.config/gym`, macOS `~/Library/Application Support/gym`)
  - folder scopes: the `.gym/config.yaml` of every root on the chain (`scope.Chain`), folded outermost to innermost
  - legacy: `config.json` is accepted as a fallback for user and project (`config.yaml` wins); writes always target `config.yaml`, seeded from `config.json` when only that exists
  - `internal/xdg` treats the install as XDG-style when any of `~/.config|.local/share|.local/state|.cache/gym` exists and then uses the previous location for every kind whose `XDG_*` variable is unset (decided once for all kinds: macOS native config/data/state coincide)
  - `-c <file>`: kit's `-c/--config` flag; `PersistentPreRunE` passes `Root.ConfigArgs()` files and overrides into the loader
- Precedence: defaults < system < user < folder chain (outermost to innermost) < `-c <file>` < `-c key=value`.
- Merge strategy (kit): scalars replace, maps merge, lists replace (no append across layers).
- The folder layers are repository content and are not handed to kit with the others. `loadChained` (`internal/config/chain.go`; `LoadScoped` for a given scope root, `loadConfigWarn` for a cwd) loads defaults+system+user through kit, reads each folder file alone (`readProjectLayer`, `internal/config/project_layer.go`), outermost first, copies only the allowlisted keys onto the result (an inner folder's key replaces an outer one's) (`projectKeys`: `install_paths`, `repositories`, `curation`, `upgrade`, `lock`; `lock.frozen` folds with or, so a folder can turn it on and never off; `admission` goes through `loadAdmission`, which folds each folder with `foldAdmission` and the `Clamp*` functions: an inner folder can only tighten, `require_pin` included via `ClampRequirePin`), then applies `-c` files and overrides unrestricted. Any other key warns once (`config: ignoring project key "<key>" (user/system/-c only)`), so a key added to `Config` is ignored from a project until listed. `LoadMergedConfigWarn` returns the warnings; the CLI prints them.
- Folder `install_paths` (`sanitizeProjectPaths`): relative, no `..` escape, no `~`, inside the folder that wrote them after `ResolveExisting` (symlinks on the existing prefix); kept entries are made absolute against the scope root being operated on (and must stay inside it), so an inherited relative path lands in each folder's own tree. `normalizePaths` anchors operator-layer relative project paths at the project root too, never the working directory.
- Mutations (`AddInstallPath`, `AddRepository`, `RemoveRepository`) write one key via kit `SetValue`, preserving the rest of the file.
- Struct fields carry `yaml` tags; kit matches keys (files and `-c key=value`) by them.
- `-c/--config` is hidden from default `--help` by kit; `execCobra` calls `Root.ApplyGroupVisibility` so `--help-all` reveals it.

### Folder scopes

- `internal/scope.Chain(dir)`: roots from `dir` up, innermost first; stops at `$HOME` (inclusive) or the filesystem root. An ancestor root counts only if its `.gym` is owned by the current user and not group/world-writable (`ownedByMe`, a variable so tests can inject a foreign owner; `owner_other.go` is a permissive stub for platforms without POSIX owner and mode). `dir` itself is never tested.
- The CLI resolves a scope in `internal/cli/scope_flags.go`: `applyDirFlag` (PersistentPreRunE) validates `--dir`, `loadScopeAt(start, exact)` runs `Chain`, loads the chain config and fills `runtime.ProjectRoot`/`Chain`. `exact` (learn, `--dir`) makes `start` the scope root even before it holds a `.gym`; otherwise the root is the nearest chain root.
- Per-scope reads go through `skillLayers` (`list.go`): records are checked with `trustedRecordsAt` against their own scope root; `resolveEffective` marks shadowed names. `warmup` keeps the winners only.
- `train` (`train.go`): `trainScope` plans and runs one scope; `--recursive` finds scopes with `findScopeRoots` and runs `trainScope` per root, collecting failures. `upgrade.PlanItem.Root` is the scope root of a project item.
- Guard policies: `LoadOptions.ProjectDirs` takes each chain root's `.gym/policies`, outermost first; the command's own engine is built from the start folder. Gate requests carry `policy_root`, the scope being operated on (`train` sets it per scope, so `train -r` gates each nested scope on its own chain); the guard resolves it through `WithEngineResolver` to `App.scopeEngine`, which loads the union of the start chain and the scope's chain (never smaller than the start chain) once per process. A scope whose engine cannot be built is gated by the start-chain engine (`scopeEngine` falls back; `scopeGate` warns, or denies under `--strict`).
- `network.mode` and `network.allow_hosts` fold in `loadChained` through `foldNetworkLayer`/`ClampNetwork` (`internal/config/network.go`): the mode only rises, a layer's list only intersects (`netpolicy.Intersect`); an open policy with no hosts listed takes the layer's list as is. `network` is read by the fold, not copied by `applyProjectLayer`, so it neither warns nor leaks past the clamp.

## Network policy and egress

One decision point: `netpolicy.Check(rawURL)` (`internal/netpolicy`). The current policy is process-wide (`netpolicy.Set` returns the restore function; tests that set it restore it), installed where `--offline` merges into config (`App.applyNetwork`, from `loadScopeAt`) and once in `PersistentPreRunE` (`initNetworkPolicy`) before the bus, judge and notifier start. `--offline` raises the mode to offline, never lowers it. `execCobra` restores the previous policy when a run ends. A config that cannot be read denies the network for the run.

| Egress | Hook |
|--------|------|
| registry fetch (`update`, `serve` refresh) | `registry.Get`: check before the request and in `CheckRedirect`; auth header only for the exact repository host/port over https or loopback http, dropped on any redirect leaving it |
| coach manifest fetch | `registry.Get` (no credential) |
| archive URL | `install.download` through `registry.Get`; the listing repository's credential via `archiveAuthFor` (`OriginURL` equal to a configured repository URL), same host only |
| git clone/fetch | `install.checkGitSource`: under a non-open policy the effective URL from `git ls-remote --get-url` (insteadOf applied) is checked before any network git command; every call goes through `gitCmd`: `-c protocol.allow=user -c protocol.https.allow=always -c protocol.ssh.allow=always -c protocol.file.allow=user -c protocol.ext.allow=never`, `GIT_TERMINAL_PROMPT=0` when stdin is not a terminal; no `--recurse-submodules` |
| event bus network adapter (`--bus network`, `GYM_BUS_URL`) | `eventbus.New` checks the hub URL; a denial stops startup |
| upgrade notifier (`kit/core/upgrade`) | skipped unless `netpolicy.Open()` |
| `judge` (rsx subprocess) | `judge.WithNetworkGate`: skipped with a warning and a `judge-skipped` finding; a cached judgement is still served |
| `git credential fill` | local helper, prompts disabled (`GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=`); not an egress by itself |

Kit installs its own offline guard under `http.DefaultTransport` (`netpolicy.Install`, called by `cli.New`) that acts on a context marked offline; kit stamps the marker in a `PersistentPreRunE` hook that gym's own `PersistentPreRunE` replaces, so `initNetworkPolicy` stamps it with `kitcli.WithOffline`. Kit's guard exempts loopback; gym's check does not.

Auth lives in `internal/registry/auth_fetch.go` (`AuthError`, `Get`, `bearerToken`, `readTokenFile`, `credentialHeader`); the `token_file` mode and owner check is `tokenFileTrusted` (`auth_owner_unix.go`). Redaction helpers are `internal/redact` (`install.RedactURL` delegates). The cached registry index is the publisher's own document and keeps `source_url` verbatim.

Archives: `internal/install/archive.go`. Limits `MaxArchiveEntries`/`MaxArchiveBytes`; a declared size over the budget is refused from the header (nothing written), the real byte count is enforced as well; `archiveRoot` takes the sole top-level directory only when it is the only entry.

## Registry Refresh

`internal/registry.Refresh`:
- fetch each configured coach URL
- parse `skills.json` into a `skillsDoc`
- compute curated label:
  - config `curated: true` is downgraded if `repository.stars < min_stars`
  - `repository.curated` or `repository.curated_early` forces curated
- flatten skill entries into a merged index:
  - one entry per `(name, version)` (a leading `v` is ignored); the best of equal pairs wins (curated, then repository name)
  - entries sorted by name, best first; `Index.Find(name)` returns the best (curated, then newer version), `Index.FindVersion(name, version)` the exact version, `Index.Versions(name)` the list
- report diffs vs previous index (new skills, upgrade candidates), best entry to best entry per name

Index storage:
- `$XDG_DATA_HOME/gym/registry-index.json`

## Install

`internal/install.InstallSkill`:
- stage source (`internal/install/source.go`, `StageSource`):
  - local directory: use as-is
  - URL: `git clone --depth 1` to temp dir
  - URL, file URL or directory with a `#ref` (`SplitRef`, `ValidRef`): branch or tag by `git clone --depth 1 --branch <ref>`; a 40/64-hex commit by `git init`, `remote add`, `fetch --depth 1 origin <sha>`, `checkout --detach FETCH_HEAD`. Refs starting with `-` or holding odd characters are refused, and `--` precedes every URL. A remote without the ref is `ref-not-found: <ref> at <url>` (a command error, nothing published). `git rev-parse HEAD` of the checkout is reported through `Request.Outcome.Commit` (the lock's `commit`)
- resolve skill root:
  - if registry entry has `path`, use `<repo>/<path>`
  - else use repo root
- validate using configured `validator.command` (default `skills-ref validate {path}`)
- admission (`internal/install/admission.go`, `Admit`): digest, pin, `gym.json`, tool coverage, ceiling, approval. `Request` carries the pin inputs: `Lock` (the lock entry being honoured), `ExpectDigest` (operator), `Kind` (`registry|git|dir`, as the caller resolved it), `Requested`, `Ref`
- gate (policies) with the admission facts
- copy validated directory into install path:
  - destination is `<install-path>/<skill-name>`; the name must be a single path component
  - `.git` directories are skipped (`fsutil.CopyDir` skip predicate), as in the digest
  - destination is re-hashed after the copy; a mismatch removes it and fails
  - the default project path `<root>/.gym/skills` must not resolve outside the project root
- source URLs: `install.CheckSourceURL` rejects a userinfo password before any network use (`source-credentials-in-url`); `install.RedactURL` strips the password and credential-looking query values from `source_url` in state, events, progress items and error text

State trust: a state record's `install_path` is used for a write or delete only after `install.CheckRecordPath` (clean absolute path, base name = skill name, parent resolved inside the project root or equal to a trusted parent from `install.TrustedParents`, directory or absent, never a symlink). Failure is `install-path-untrusted: <path>`; `gym train` skips that record and still processes the others. Any future uninstall must call the same function.

`gym train` passes a per-skill gate (request-scoped) in addition to the plan gate.

### Lock and pins

Design: [ADR-0003](adr/0003-folder-scopes-lockfile-uninstall-sources.md) section 2. `internal/lock` is the file (`gym.lock/v1`: `Parse` is strict, `Marshal` deterministic, `Save` atomic through `fsutil.WriteFileAtomic`; `SourceForLock` and `ResolveSource` store and resolve local sources, refusing absolute or escaping ones in a project lock with `lock-source-escapes`). Path: `<root>/.gym/lock.json`, global `<ConfigHome>/lock.json` (`internal/xdg`). Commands: `internal/cli/learn.go` (token flow, restore, frozen), `learn_lock.go` (lock location, writers, train pins, unlearn hook), `lock_check.go` (restore, verify, `list --check`), `learn_resolve.go` (`name@version`, `url#ref`). `internal/install/drift.go` (`LockDrift`) compares a lock with trusted state records: `missing`, `digest-drift`, `modified`, `untracked` (the first three fail).

Flow of `learn <name>` with a lock entry: the entry becomes the install target (`lockedTarget`: source resolved inside the scope root, `Ref` = locked commit); `Request.Lock` makes the locked digest the pin; the registry index only contributes its digest for the same version, so a disagreement is reported (`digest_pin_conflict`) and the lock still wins. `learn` with no arguments never calls `registry.LoadIndex`. The lock is written after the install directories and the state; unlearn's `OnSkillGone` hook removes an entry last.

`ExpectedDigest(entry, lock) (digest, source, conflict)`: lock entry, then registry digest; `Admit` applies the operator's `ExpectDigest` first and then reports no conflict. `conflict` only when both pin different digests for the same `version`. `digest_source` is `operator`, `lockfile` or `registry`.

| Reason | Where | Published as `install.rejected` |
|--------|-------|----------------------------------|
| `pin-unavailable`, `ref-not-found` | `learn name@version`, `url#ref` | no |
| `lock-missing`, `lock-invalid`, `lock-frozen` | lock load, `--frozen`, train | no |
| `lock-drift` | `learn --frozen` (one event per drifted skill) | yes |
| `lock-source-escapes` | restore or `learn <name>` of a locked local source | yes |
| `digest-required` | `admission.require_pin` (policy `admission-digest-required`) | yes |
| `digest-pin-conflict` | warn finding `admission-digest-pin-conflict` | no (not a rejection) |
| `network-denied` | `netpolicy.Check` at any egress (`*netpolicy.Error`); hard, not relaxed by `--insecure` | yes, when it happens during an install; `gym update` and bus startup just fail |
| `archive-invalid` | `install.StageArchive` / `ExtractArchive` (`*install.ArchiveError`): `archive-invalid: <reason>` | no (command error) |
| `source-credentials-in-url` | `CheckSourceURL` | yes |

`admission.require_pin` (`none|remote|all`, default `none`) is clamped like `require_manifest`: `ClampRequirePin`, and a project or folder layer can only raise it. `remote` covers registry, git and archive sources; the fact is `digest_status = unpinned-required`.

## Admission

Design: [ADR-0002](adr/0002-skill-manifest-admission.md). `install.Admit` is read-only and shared by install, upgrade, and `gym guard test <dir>`.

Config: `admission.*` is part of `config.Config` (yaml tags, typed defaults). `loadAdmission` asks the kit loader for layers separately (defaults+system+user, then the project layer alone), tightens with `Clamp*`, then re-applies `-c <file>` and `-c key=value` on top of the clamped value; `--max-data-class` is applied last by the CLI. Layer path selection is shared with the main loader, so legacy `config.json` and previous-dir discovery apply. Precedence, lowest to highest: defaults < system < user < project (clamped) < `-c <file>` < `-c key=value` < `--max-data-class`.

```yaml
admission:
  max_data_class: internal
  require_manifest: warn
  require_approval: false
  require_pin: none
lock:
  frozen: false
```

Files: `internal/install/{admission,digest,manifest,tools}.go`, `internal/dataclass` (ladder), `internal/approvals` (operator store), `internal/config/admission.go`, `internal/guard/policies/admission-*.yaml`, `internal/cli/{admission,guard_admission,approvals,digest}.go`.

Digest `gym.digest/v1`: regular files only under the skill root, `.git` directories skipped, symlinks rejected (`digest-unsupported-entry`); one line `<x|-> <hex> <relpath>` per file (`x` = owner-exec bit), sorted bytewise on path; digest is `sha256:` + sha256 of the listing. The expected pin comes from `install.ExpectedDigest`: the operator's `--digest`, then the lock entry, then the registry `digest` field.

### Hard failures and rejection reasons

Hard failures are `*install.AdmissionError` (not relaxable by `--insecure`, independent of the bus):

| Reason | Cause |
|--------|-------|
| `manifest-invalid` | `gym.json` unparsable, unknown field, operator-only field, bad schema/class/tools, or SKILL.md `allowed-tools` not a string |
| `digest-mismatch` | pin differs from computed digest, or installed copy differs from admitted tree |
| `digest-unsupported-entry` | symlink or irregular file in the tree |
| `approval-revoked` | operator revoked the skill name or digest |

Policy denies surface as `guard.PolicyDenyError` (`errors.Is(err, guard.ErrPolicyDeny)`). `gym.skill.install.rejected` is published once per rejected install or upgrade attempt from `learn` and `train` with `{skill, scope, reason, request_id}`. `request_id` is the id the attempt used for its `gym.skill.install.requested` event; failures before the gate (hard admission failures, validator failure) have no requested event and carry the attempt's own id. `--insecure` lowering a deny to a warn is not a rejection: nothing is published. Errors that are not rejections (source fetch, I/O) publish nothing; no bus wired publishes nothing.

| Rejection | Reason |
|-----------|--------|
| hard admission failure | the hard-failure reason (table above) |
| `admission-*` policy deny | rule name minus `admission-`, except `admission-data-class` (`data-class-exceeds-ceiling`), `admission-approval-enforced` (`approval-required`), `admission-manifest-required` (`manifest-missing`) |
| validator command failed, timed out, or could not run | `validator-failed` |
| other policy deny (e.g. `no-curl-bash`, `no-postinstall-script`, `path-allowlist`) | the denying rule name |

Deny-severity findings pick the reason (warn findings only under `--strict`, when no deny finding exists); among them an `admission-*` rule wins, else the first finding. Gate timeout under `--strict` denies without findings and publishes nothing.

### Uninstall (`gym unlearn`)

Implementation: `internal/cli/unlearn.go` (command, matching, confirmation, events), `internal/install/remove.go` (`Remover`), `internal/fsutil/atomic.go` (state writes). Every directory is deleted by `Remover.RemoveSkill` only after `install.CheckRecordPath` accepts the record; there is no other deletion path. Order per record: validate, modified check (re-hash vs `state.digest`), `RemoveAll` of the validated path (already absent counts as removed), then the record leaves `installed.json` by atomic write. The modified check is skipped for a tree with only directories at any depth (no files, symlinks or irregular entries; symlinks are not followed), so a retry after an interrupted `RemoveAll` needs no `--force`. `Remover.OnSkillGone(name)` fires once when no record of the skill remains in the scope; `unlearn` wires it to remove the scope's lock entry (`--keep-lock` leaves it; a corrupt lock is reported as `remove-failed` and left untouched).

| Topic | Payload |
|-------|---------|
| `gym.skill.uninstall.requested` | `{skill, scope, folder, paths, request_id}` |
| `gym.skill.uninstall.completed` | `{skill, scope, folder, paths, request_id}` |
| `gym.skill.uninstall.failed` | `{skill, scope, reason, request_id}` |

One `request_id` per skill attempt. Informational: the guard does not gate uninstall, `path-allowlist` is not consulted, and `--dry-run` publishes nothing. `folder` is the scope root (empty for global). `failed.reason` is the first failure of the skill.

| Reason | Cause |
|--------|-------|
| `skill-not-installed` | no record matches (exit 1; `--ignore-missing` exit 0) |
| `install-path-untrusted` | record `install_path` fails `CheckRecordPath`; record and directory untouched |
| `skill-modified` | directory differs from recorded digest, or no digest recorded, or cannot be hashed; `--force` |
| `confirmation-required` | glob matched more than one skill, no TTY, no `--yes` |
| `remove-failed` | I/O error removing the directory or writing state; record kept |

### Install-requested admission fields

Added to `SkillInstallRequestedPayload` and `SkillUpgradePlannedPayload` (all `omitempty`; guard defaults every key in the CEL activation):

| Field | Type | Meaning |
|-------|------|---------|
| `folder` | string | scope root of a project scope install or plan; `` for global (also on `SkillInstallCompletedPayload`) |
| `manifest_present` | bool | `gym.json` found and valid |
| `manifest_mode` | string | `admission.require_manifest`: `off`/`warn`/`reject` |
| `digest` | string | computed `sha256:<hex>` |
| `digest_status` | string | `match` / `unpinned` / `unpinned-required` (`admission.require_pin` demands a pin; `mismatch` is a hard failure) |
| `digest_source` | string | `operator` / `lockfile` / `registry` / `` |
| `digest_pin_conflict` | bool | lock and registry pin different digests for the same version; the lock won |
| `source_kind` | string | `registry` / `git` / `dir` / `archive` |
| `source_host` | string | network host the source is fetched from (plan: the one host all items share); `` for a local source |
| `requested` | string | what the operator pinned: the version after `@` or the ref after `#` |
| `resolved` | string | git commit staged |
| `lock_present` | bool | a lock entry was honoured as the pin |
| `declared_tools` | string[] | manifest `allowed_tools` (sorted) |
| `undeclared_tools` | string[] | SKILL.md `allowed-tools` tokens no declared token covers |
| `data_class` | string | manifest value or `` |
| `max_data_class` | string | resolved ceiling |
| `data_class_over` | bool | class ranks above the ceiling |
| `data_class_escalated` | bool | upgrade raises the class above the installed version's |
| `approval_state` | string | `unreviewed` / `approved` / `required-missing` (`require_approval` on and unreviewed) |

### Admission policies

| Policy | When | Effect |
|--------|------|--------|
| `admission-tools-undeclared` | `undeclared_tools` non-empty | deny |
| `admission-data-class` | `data_class_over` | deny |
| `admission-digest-unpinned` | `digest_status == 'unpinned'` | warn |
| `admission-digest-required` | `digest_status == 'unpinned-required'` | deny (reason `digest-required`) |
| `admission-digest-pin-conflict` | `digest_pin_conflict` | warn (reason `digest-pin-conflict`) |
| `admission-approval-required` | `approval_state == 'unreviewed'` | warn |
| `admission-approval-enforced` | `approval_state == 'required-missing'` | deny |
| `admission-manifest-missing` | no manifest, mode `warn` | warn |
| `admission-manifest-required` | no manifest, mode `reject` | deny |
| `admission-tools-unrestricted` | declared tools contain bare `Bash` | warn |
| `admission-data-class-escalated` | `data_class_escalated` | warn |

Config-driven effects (`require_approval`, `require_manifest`, `require_pin`) change fact values, not policies, so a project policy override cannot weaken an operator setting.

## Upgrade Policy

Upgrade selection is planned in `internal/upgrade.Plan`:
- outside a project (or no project skills): upgrade global only
- inside project with project-installed skills:
  - upgrade project skills
  - by default also upgrade matching global installs
  - `--no-global` disables mirroring
- the project scope is the folder scope at `--dir`, else the nearest root; `Input.ProjectRoot` becomes `PlanItem.Root`
- `--recursive` plans each nested scope separately (see Folder scopes)
- lock (see Lock and pins): after the plan is trusted, items whose lock entry has `requested` are dropped with a `skip <name>: pinned to <requested> ...` line; with `--frozen` or `lock.frozen` any item whose registry entry is newer fails `lock-frozen` before the gate; each upgraded item's lock entry (in the item's own scope: the folder's lock, or the global lock for a mirrored item) is replaced after the state is saved; a skill with no lock entry stays unlisted

## Kit primitive adoption

gym imports kit packages for consistency with hop.top ecosystem. Key mappings:

| Subsystem | Kit primitive | Notes |
|-----------|---------------|-------|
| XDG paths | `kit/core/xdg` | Replaced `internal/xdg` |
| CLI scaffolding | `kit/console/cli` | Persistent flags: `--format`, `--quiet`, `--no-color`, `--profile`, `-V`, `--progress-format` |
| Logging | `kit/console/log` | Lazy-binds to viper; `-V/-VV` contract included |
| Output formatting | `kit/console/output` | (Not yet swapped in install; existing code uses fmt.Fprintf; golden tests needed) |
| TUI/REPL | `kit/console/tui` + `bubbletea/v2` | Matches tlc/aps look-and-feel |
| Interactive prompts | `kit/console/wizard` | Used by `gym coach hire` only (no other command has a wizard) |
| Progress reporting | `kit/console/progress` | Slow paths (learn/train/coach hire) emit phase updates |
| Upgrade checking | `kit/core/upgrade` | Banner runs only when `network.mode` is `open` (`PersistentPreRunE` in `cobra.go`) |
| Event bus | `kit/runtime/bus` | Memory/SQLite/Network adapters; request/response with timeout |
| Policy gates | `kit/runtime/policy/cel` | CEL evaluator for install/upgrade guards |
| Tool specifications | `kit/ai/toolspec` | Self-spec at boot; skill specs merged at runtime |
| HTTP helpers | `kit/core/util` | Consistent timeouts, redaction (manifest fetch, coach hire) |
| Identity | `kit/core/identity` | Stable origin ID for bus events  |

**Go version**: pinned to 1.26.1+ (kit requirement).

`hop.top/kit` is pinned to a published tag in `go.mod`; no `replace` directives. Do not `go mod vendor`.

## Adding a new policy guard

1. Drop a YAML file under `internal/guard/policies/` (one policy per file):

```yaml
name: my-policy-name
when: payload.field == "bad_value"
effect: deny           # or warn
otherwise: allow       # optional; default
severity: deny         # optional; display severity
message: "why this matched"
```

2. Add a targeted unit test in `internal/guard/guard_test.go` verifying the CEL expression fires correctly on a test payload.

3. Document the policy in `docs/agent.md` under the "Policy guards" section.

Never derive a policy allowance from the package under evaluation: `ScanStaging` reads only descriptive manifest fields (`postinstall`, `shell_hook`) and file sizes. The `allow_postinstall` / `large_binary` payload keys stay present and always false so existing expressions keep evaluating; built-in policies do not reference them.

User overrides in `$XDG_CONFIG_HOME/gym/policies/` can disable via `policies.yaml` (`disable: ["my-policy-name"]`), relax from deny to warn (`relax: ["my-policy-name"]`), or redefine by name. `<root>/.gym/policies/` is untrusted repository content: it can only add policies (and disable/relax its own); entries touching built-in or user policies are ignored with a logged warning (`guard.LoadOptions.Logger`). A project/folder file that fails to read, parse, validate or compile (CEL) is skipped with one warning naming it (`loadDir` with a skip callback, `compileCheck`); a broken user-dir or built-in file is a hard error.

## Adding a new toolspec source

1. Implement `kit/ai/toolspec.Source` interface (Resolve method).
2. Register in `internal/cheat/registry.go` constructor.
3. Call `BuildSelfSpec(cmd.Root(), version)` to walk the cobra tree and extract the ToolSpec.

Refer to kit's `toolspec` package docs for the full API. The registry merges gym's self-spec with one lightweight spec per installed skill (extracted from SKILL.md frontmatter).

## Extending the event bus

1. Define payload struct + topic constant in `internal/events/<family>.go`:

```go
const TopicMyNewEvent bus.Topic = "gym.my.new.event"

type MyEventPayload struct {
  Field1 string `json:"field1"`
  RequestID string `json:"request_id,omitempty"`
}
```

2. Add round-trip test in `internal/events/topics_roundtrip_test.go` to verify JSON marshaling.

3. Publish via `App.Bus().Publish(ctx, events.TopicMyNewEvent, payload)` or `App.Bus().RequestResponse(ctx, reqTopic, respPattern, payload, timeout)` for sync gates.

**Rules**: all topics are 4-segment (e.g. `gym.coach.repository.hired`); past-tense or present-participle action verbs; request/response pairs carry `request_id` (uuid v7); subscribers tolerate duplicates (idempotent on `request_id`).

## Versioning + Releases

- `gym version` reads embedded values set by ldflags (and falls back to Go build info).
- Releases are built via GoReleaser:
  - tag `vX.Y.Z` triggers `.github/workflows/release.yml`
  - `.goreleaser.yaml` injects version/commit/date into `internal/cli`

## Vanity Go Module (Cloudflare Pages)

If you want a vanity module path like `hop.top/gym`, you must serve `go-import` metadata at that URL.

This repo includes a workflow that publishes a minimal Cloudflare Pages site to a detached website branch:
- workflow: `.github/workflows/publish.yml`
- branch: `gh-pages`

Cloudflare Pages config:
- production branch: `gh-pages`
- output directory: `.`
- attach custom domain(s) as needed

The published site is static:
- `_redirects` rewrites all paths to `index.html` (so `.../cmd/gym?go-get=1` works)
- `index.html` includes `go-import` metadata and JS-redirects humans to GitHub

## CI

`.github/workflows/ci.yml` enforces:
- `go mod tidy` produces no diff
- `gofmt` clean
- `go test ./...`
- `go vet ./...`

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
- The folder layers are repository content and are not handed to kit with the others. `loadChained` (`internal/config/chain.go`; `LoadScoped` for a given scope root, `loadConfigWarn` for a cwd) loads defaults+system+user through kit, reads each folder file alone (`readProjectLayer`, `internal/config/project_layer.go`), outermost first, copies only the allowlisted keys onto the result (an inner folder's key replaces an outer one's) (`projectKeys`: `install_paths`, `repositories`, `curation`, `upgrade`; `admission` goes through `loadAdmission`, which folds each folder with `foldAdmission` and the `Clamp*` functions: an inner folder can only tighten), then applies `-c` files and overrides unrestricted. Any other key warns once (`config: ignoring project key "<key>" (user/system/-c only)`), so a key added to `Config` is ignored from a project until listed. `LoadMergedConfigWarn` returns the warnings; the CLI prints them.
- Folder `install_paths` (`sanitizeProjectPaths`): relative, no `..` escape, no `~`, inside the folder that wrote them after `ResolveExisting` (symlinks on the existing prefix); kept entries are made absolute against the scope root being operated on (and must stay inside it), so an inherited relative path lands in each folder's own tree. `normalizePaths` anchors operator-layer relative project paths at the project root too, never the working directory.
- Mutations (`AddInstallPath`, `AddRepository`, `RemoveRepository`) write one key via kit `SetValue`, preserving the rest of the file.
- Struct fields carry `yaml` tags; kit matches keys (files and `-c key=value`) by them.
- `-c/--config` is hidden from default `--help` by kit; `execCobra` calls `Root.ApplyGroupVisibility` so `--help-all` reveals it.

### Folder scopes

- `internal/scope.Chain(dir)`: roots from `dir` up, innermost first; stops at `$HOME` (inclusive) or the filesystem root. An ancestor root counts only if its `.gym` is owned by the current user and not group/world-writable (`ownedByMe`, a variable so tests can inject a foreign owner; `owner_other.go` is a permissive stub for platforms without POSIX owner and mode). `dir` itself is never tested.
- The CLI resolves a scope in `internal/cli/scope_flags.go`: `applyDirFlag` (PersistentPreRunE) validates `--dir`, `loadScopeAt(start, exact)` runs `Chain`, loads the chain config and fills `runtime.ProjectRoot`/`Chain`. `exact` (learn, `--dir`) makes `start` the scope root even before it holds a `.gym`; otherwise the root is the nearest chain root.
- Per-scope reads go through `skillLayers` (`list.go`): records are checked with `trustedRecordsAt` against their own scope root; `resolveEffective` marks shadowed names. `warmup` keeps the winners only.
- `train` (`train.go`): `trainScope` plans and runs one scope; `--recursive` finds scopes with `findScopeRoots` and runs `trainScope` per root, collecting failures. `upgrade.PlanItem.Root` is the scope root of a project item.
- Guard policies: `LoadOptions.ProjectDirs` takes each chain root's `.gym/policies`, outermost first; the engine is built once per process from the command's start folder, so `train -r` evaluates the policies of that folder's chain.
- Config keys that arrive later (network policy, frozen lock, pin requirement) join `foldAdmission` in `internal/config/chain.go`, each with its own clamp.

## Registry Refresh

`internal/registry.Refresh`:
- fetch each configured coach URL
- parse `skills.json` into a `skillsDoc`
- compute curated label:
  - config `curated: true` is downgraded if `repository.stars < min_stars`
  - `repository.curated` or `repository.curated_early` forces curated
- flatten skill entries into a merged index:
  - one best entry per skill name
  - prefer curated; otherwise prefer newer version
- report diffs vs previous index (new skills, upgrade candidates)

Index storage:
- `$XDG_DATA_HOME/gym/registry-index.json`

## Install

`internal/install.InstallSkill`:
- stage source:
  - local directory: use as-is
  - URL: `git clone --depth 1` to temp dir
- resolve skill root:
  - if registry entry has `path`, use `<repo>/<path>`
  - else use repo root
- validate using configured `validator.command` (default `skills-ref validate {path}`)
- admission (`internal/install/admission.go`, `Admit`): digest, pin, `gym.json`, tool coverage, ceiling, approval
- gate (policies) with the admission facts
- copy validated directory into install path:
  - destination is `<install-path>/<skill-name>`; the name must be a single path component
  - `.git` directories are skipped (`fsutil.CopyDir` skip predicate), as in the digest
  - destination is re-hashed after the copy; a mismatch removes it and fails
  - the default project path `<root>/.gym/skills` must not resolve outside the project root
- source URLs: `install.CheckSourceURL` rejects a userinfo password before any network use (`source-credentials-in-url`); `install.RedactURL` strips the password and credential-looking query values from `source_url` in state, events, progress items and error text

State trust: a state record's `install_path` is used for a write or delete only after `install.CheckRecordPath` (clean absolute path, base name = skill name, parent resolved inside the project root or equal to a trusted parent from `install.TrustedParents`, directory or absent, never a symlink). Failure is `install-path-untrusted: <path>`; `gym train` skips that record and still processes the others. Any future uninstall must call the same function.

`gym train` passes a per-skill gate (request-scoped) in addition to the plan gate.

## Admission

Design: [ADR-0002](adr/0002-skill-manifest-admission.md). `install.Admit` is read-only and shared by install, upgrade, and `gym guard test <dir>`.

Config: `admission.*` is part of `config.Config` (yaml tags, typed defaults). `loadAdmission` asks the kit loader for layers separately (defaults+system+user, then the project layer alone), tightens with `Clamp*`, then re-applies `-c <file>` and `-c key=value` on top of the clamped value; `--max-data-class` is applied last by the CLI. Layer path selection is shared with the main loader, so legacy `config.json` and previous-dir discovery apply. Precedence, lowest to highest: defaults < system < user < project (clamped) < `-c <file>` < `-c key=value` < `--max-data-class`.

```yaml
admission:
  max_data_class: internal
  require_manifest: warn
  require_approval: false
```

Files: `internal/install/{admission,digest,manifest,tools}.go`, `internal/dataclass` (ladder), `internal/approvals` (operator store), `internal/config/admission.go`, `internal/guard/policies/admission-*.yaml`, `internal/cli/{admission,guard_admission,approvals,digest}.go`.

Digest `gym.digest/v1`: regular files only under the skill root, `.git` directories skipped, symlinks rejected (`digest-unsupported-entry`); one line `<x|-> <hex> <relpath>` per file (`x` = owner-exec bit), sorted bytewise on path; digest is `sha256:` + sha256 of the listing. The expected pin comes from `install.ExpectedDigest` (registry `digest` field today; a lockfile backend would slot in ahead of it).

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

### Install-requested admission fields

Added to `SkillInstallRequestedPayload` and `SkillUpgradePlannedPayload` (all `omitempty`; guard defaults every key in the CEL activation):

| Field | Type | Meaning |
|-------|------|---------|
| `folder` | string | scope root of a project scope install or plan; `` for global (also on `SkillInstallCompletedPayload`) |
| `manifest_present` | bool | `gym.json` found and valid |
| `manifest_mode` | string | `admission.require_manifest`: `off`/`warn`/`reject` |
| `digest` | string | computed `sha256:<hex>` |
| `digest_status` | string | `match` / `unpinned` (`mismatch` is a hard failure) |
| `digest_source` | string | `registry` / `` |
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
| `admission-approval-required` | `approval_state == 'unreviewed'` | warn |
| `admission-approval-enforced` | `approval_state == 'required-missing'` | deny |
| `admission-manifest-missing` | no manifest, mode `warn` | warn |
| `admission-manifest-required` | no manifest, mode `reject` | deny |
| `admission-tools-unrestricted` | declared tools contain bare `Bash` | warn |
| `admission-data-class-escalated` | `data_class_escalated` | warn |

Config-driven effects (`require_approval`, `require_manifest`) change fact values, not policies, so a project policy override cannot weaken an operator setting.

## Upgrade Policy

Upgrade selection is planned in `internal/upgrade.Plan`:
- outside a project (or no project skills): upgrade global only
- inside project with project-installed skills:
  - upgrade project skills
  - by default also upgrade matching global installs
  - `--no-global` disables mirroring
- the project scope is the folder scope at `--dir`, else the nearest root; `Input.ProjectRoot` becomes `PlanItem.Root`
- `--recursive` plans each nested scope separately (see Folder scopes)

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
| Upgrade checking | `kit/core/upgrade` | Background banner respects `--offline` |
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

User overrides in `$XDG_CONFIG_HOME/gym/policies/` can disable via `policies.yaml` (`disable: ["my-policy-name"]`), relax from deny to warn (`relax: ["my-policy-name"]`), or redefine by name. `<root>/.gym/policies/` is untrusted repository content: it can only add policies (and disable/relax its own); entries touching built-in or user policies are ignored with a logged warning (`guard.LoadOptions.Logger`).

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

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
- `internal/project`: project root detection; `.gym/` dir constants
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
- `internal/guard/`: CEL policy gate subscriber; loader for built-in + user/project policy overrides
- `internal/guard/policies/`: baked-in CEL policies (10 default YAML files)
- `internal/cheat/`: toolspec registry builder; gym self-spec; skill specs from manifests; tip adapter writer; optional tip bridge subscriber
- `e2e/`: integration tests with xrr cassettes (build-tag gated, `go test -tags cassettes ./e2e/...`)

## Config + Precedence

- Loader: `internal/config.LoadMergedConfig` delegates to `hop.top/kit/go/core/config` `Load`; typed defaults via `Options.Defaults`; no gym-side merge.
- Config sources (YAML; paths from kit's `OptionsForToolWithMarkersFrom`):
  - system: `/etc/gym/config.yaml`
  - user: `config.yaml` in `$XDG_CONFIG_HOME/gym` (else OS default: Linux `~/.config/gym`, macOS `~/Library/Application Support/gym`)
  - project: nearest `.gym/config.yaml` at or above cwd
  - `-c <file>`: kit's `-c/--config` flag; `PersistentPreRunE` passes `Root.ConfigArgs()` files and overrides into the loader
- Precedence: defaults < system < user < project < `-c <file>` < `-c key=value`.
- Merge strategy (kit): scalars replace, maps merge, lists replace (no append across layers).
- Mutations (`AddInstallPath`, `AddRepository`, `RemoveRepository`) write one key via kit `SetValue`, preserving the rest of the file.
- Struct fields carry `yaml` tags; kit matches keys (files and `-c key=value`) by them.
- `-c/--config` is hidden from default `--help` by kit; `execCobra` calls `Root.ApplyGroupVisibility` so `--help-all` reveals it.

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
- validate using configured `validator.command` (default `skills-ref test {path}`)
- copy validated directory into install path:
  - destination is `<install-path>/<skill-name>`

## Upgrade Policy

Upgrade selection is planned in `internal/upgrade.Plan`:
- outside a project (or no project skills): upgrade global only
- inside project with project-installed skills:
  - upgrade project skills
  - by default also upgrade matching global installs
  - `--no-global` disables mirroring

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

User/project overrides in `$XDG_CONFIG_HOME/gym/policies/` or `<root>/.gym/policies/` can disable via `policies.yaml` (`disable: ["my-policy-name"]`) or relax from deny to warn (`relax: ["my-policy-name"]`).

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

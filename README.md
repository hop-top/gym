# gym

> [!WARNING]
> **🚧 Do Not Use — History Will Be Rewritten 🚧**
>
> This repo is undergoing major restructuring as we selectively
> open-source internal tools built at
> [Idea Crafters LLC](https://ideacrafters.com). Git history **will be
> force-pushed and rewritten** multiple times. Do not fork, clone, or
> depend on this repo in any capacity until we tag a stable release.

Universal package manager for [agentskills.io](https://agentskills.io) skills.

- Module/binary: `hop.top/gym` / `gym`
- Validation gate: `skills-ref validate`
- Supports curated registries and direct URL installs
- XDG-first paths with user + project hierarchical config
- Scope-aware upgrades with project/global mirror policy
- Interactive management via Charmbracelet `tui` and `repl`

## Install / Build

```bash
go install hop.top/gym/cmd/gym@latest
```

Build from source:

```bash
go build -o gym ./cmd/gym
```

## Commands

```bash
gym version
gym update
gym learn [--global|-g] [--path NAME_OR_PATH] [--agent PATTERN ...] <skill-or-url> [...]
gym train [--no-global] [pattern ...]
gym install ...  # alias of learn
gym upgrade ...  # alias of train
gym warmup [--format xml|json|agents-md] [--scope all|global|project] [-o FILE]
gym coach hire [--global|-g] [--name NAME] [--curated] [--no-update] <manifest-url|owner/repo>
gym coach list
gym coach dismiss [--global|-g] [--no-update] <name|url>
gym paths
gym paths add [--scope global|project] [--target user|project] [--name NAME] --path PATH
gym serve [--host 127.0.0.1] [--port 4873] [--daemon] [--pidfile PATH] [--logfile PATH]
gym tui
gym repl
```

## Docs

- User guide: [`docs/user.md`](docs/user.md)
- Dev guide: [`docs/dev.md`](docs/dev.md)
- Agent notes: [`docs/agent.md`](docs/agent.md)

## Behavior Highlights

### Event-driven interop

gym publishes and subscribes on `kit/runtime/bus` for decoupled coordination with rsx, tip, and policy validators. All cross-tool events (coach hiring, skill installs, policy verdicts, hints) flow through the bus. Enables multi-process deployment, replay, and audit trails without tight coupling.

### Registry update

`gym update` refreshes local registry references and reports:
- new skills since last update (with origin)
- installed-skill upgrade candidates by version

### Coach judging via rsx

`gym coach hire` now scores coaches using rsx's repository analysis:
- Runs `rsx analyze` + `rsx policy test` (cached for 24h).
- `--strict` fails-closed on rsx timeout; default warns and proceeds.
- `--refresh-judgement` bypasses cache and re-runs rsx.
- `--insecure` downgrades DENY to WARN (allow risky coaches).
- `gym coach list` shows `score=N/100 (verdict)` per coach.

### Policy guards on installs/upgrades

`gym learn` and `gym train` gate on CEL policies that catch risky patterns:
- Baked-in policies: sudo, curl|bash, secrets, egress in hooks, large binaries, suspicious chmod, /etc/hosts edits, postinstall scripts, package installer shells.
- `--strict` fails-closed; `--insecure` downgrades DENY to WARN.
- `gym guard list` shows effective policies; `gym guard test <skill>` dry-runs.
- User/project policy overrides in `$XDG_CONFIG_HOME/gym/policies/` and `<root>/.gym/policies/`.

### Skill admission

Every install and upgrade runs an admission stage after the validator: a SHA-256 content digest (optionally pinned by the registry entry's `digest`), the optional `gym.json` manifest (`schema`, `allowed_tools`, `data_class`), declared vs `allowed-tools` in SKILL.md, a data-class ceiling (default `internal`), and operator approval. Skills without `gym.json` still install by default, with a warning.

- `gym digest <dir>` prints the digest; `--files` prints the listing.
- `gym guard approve <skill|dir>` / `gym guard revoke <skill|dir>` record operator approval, bound to the exact digest.
- `gym guard test <dir>` dry-runs admission and policies; `--max-data-class`, `--approval-state`, `--expect-digest` simulate operator state.
- Config `admission.max_data_class`, `admission.require_approval`, `admission.require_manifest` (`off|warn|reject`) in `config.yaml`; a project config can only tighten them. Precedence, lowest to highest: defaults < system < user < project (clamped) < `-c <file>` < `-c key=value` < `--max-data-class`.

  ```yaml
  admission:
    max_data_class: confidential   # default: internal
    require_manifest: warn         # default: warn
    require_approval: false        # default: false
  ```

Details: [docs/user.md](docs/user.md), [ADR-0002](docs/adr/0002-skill-manifest-admission.md).

### Warmup (Progressive Disclosure)

`gym warmup` generates lightweight skill metadata for context-efficient agent startup:
- `--format xml` → `<available_skills>` XML for Claude Code
- `--format json` → JSON array of skill metadata
- `--format agents-md` → AGENTS.md format for multi-agent compatibility
- `--scope all|global|project` → filter by install scope
- `-o FILE` → write to file (default: stdout)

Implements the agentskills.io progressive disclosure pattern: agents load only name+description at startup (~100 tokens), then fetch full skill content on-demand.

### Cheat sheets (toolspec + tip)

`gym cheat <topic>` renders inline hints from gym's commands and installed skills:
- `gym cheat warmup` → hints on warmup usage
- `gym cheat learn` → hints on learn usage
- `gym cheat --tip-export` → writes tip adapter file for opt-in bridge integration (GYM_TIP_BUS_BRIDGE=1).

### Learn sources

`gym learn` supports:
- skill names from local merged registry index
- direct URLs (`https://`, `ssh://`, `git@`, `file://`) and local directories
- default target is project-local install in `CWD/.gym/skills`
- use `--global` (or `-g`) for global install scope
- if multiple install paths match the chosen scope, `gym learn` installs into all of them
- use `--agent` patterns (or `--path`) to filter install targets

Install gates on validator pass (`skills-ref validate {path}` by default), admission checks, and policy verdicts.

### Train policy

- `gym train` with no args upgrades all matching scope.
- `gym train name1 name2` upgrades named skills.
- Patterns are accepted (`gym train "auth-*"`).
- If run inside a project with installed project skills:
  - project skills are upgraded
  - matching global installs are also upgraded by default
  - use `gym train --no-global` to disable global mirror
- If run outside a project with skills:
  - global skills only

### Global flags (kit/console/cli)

New persistent flags added by the event-driven interop track:
- `--bus memory|sqlite|network` — event bus adapter (default: env-driven, else memory).
- `--strict` — fail-closed on policy/judgement timeouts (fail-open by default).
- `--insecure` — downgrade DENY verdicts to WARN (allow risky coaches/skills).
- `--gate-timeout=DURATION` — max wait for sync gates (default 30s).
- `--refresh-judgement` — bypass coach judgement cache.

Inherited from kit/console/cli: `--format`, `--cols`, `--quiet`, `--no-color`, `--no-hints`, `-V`, `-C`, `--profile`, `--progress-format`, `--offline`.

### CLI parsing/config

- Command tree and aliases are implemented with Cobra.
- Runtime CLI overrides are loaded with Viper from:
  - `.gym/cli.yaml` in current working directory
  - `$XDG_CONFIG_HOME/gym/cli.yaml`
- Env overrides are supported via `GYM_*` keys (for example `GYM_LEARN_GLOBAL=true`).
- `-c/--config` (from kit/console/cli, listed under `--help-all`) layers extra config; see Configuration.

### IDE/TDE path awareness

Known global standards are detected and surfaced in `gym paths` and `gym paths add` suggestions:
- `~/.claude/skills`
- `~/.cursor/skills`
- `~/.gemini/skills`
- `~/.codex/skills`
- `~/.windsurf/skills`

### Serving a registry

`gym serve` runs a local HTTP server exposing:
- `GET /skills.json` (spec-shaped `skills.json` manifest)
- `GET /_index.json` (full merged index, for debugging)
- `GET /health`

Example:

```bash
gym serve --host 127.0.0.1 --port 4873
```

Daemon mode (with SQLite bus):

```bash
gym serve --daemon --host 127.0.0.1 --port 4873
gym serve --status --host 127.0.0.1 --port 4873
gym serve --stop --host 127.0.0.1 --port 4873
```

## Configuration

Config is YAML, layered lowest to highest:
1. built-in defaults
2. system: `/etc/gym/config.yaml`
3. user: `config.yaml` in `$XDG_CONFIG_HOME/gym` (else the OS default: `~/.config/gym` on Linux, `~/Library/Application Support/gym` on macOS)
4. project: nearest `.gym/config.yaml` at or above the working directory

Canonical file name is `config.yaml`. An existing `config.json` (user or project) is still read when no `config.yaml` sits beside it, and the previous directories (`~/.config/gym`, `~/.local/share/gym`, `~/.local/state/gym`, `~/.cache/gym`) are still used, all four together, when any of them exists and the matching `XDG_*` variable is unset (a set variable always wins). Changes made by gym write `config.yaml`, seeded from the old `config.json`, which is left untouched.
5. extra files: `-c <file>` (repeatable, in order; a missing file is an error)
6. overrides: `-c key=value` (repeatable; dotted keys nest; value parses as YAML)

Lists (`repositories`, `install_paths`) are replaced by the highest layer that sets them, not appended.

```bash
gym -c curation.min_stars=50 paths
gym -c /path/to/extra.yaml paths
gym -c 'install_paths=[{name: x, path: /opt/skills, scope: global}]' paths
```

Project state:
- `<project-root>/.gym/installed.json`

Global state:
- `$XDG_STATE_HOME/gym/installed-global.json`

Local registry index cache:
- `$XDG_DATA_HOME/gym/registry-index.json`

### Example `config.yaml`

```yaml
repositories:
  - name: official-curated
    url: https://registry.example.com/curated/skills.json
    curated: true
  - name: community-pack
    url: https://example.com/community/skills.json
    curated: false
install_paths:
  - name: claude-global
    path: ~/.claude/skills
    scope: global
  - name: claude-project
    path: .claude/skills
    scope: project
curation:
  min_stars: 100
validator:
  command: [skills-ref, validate, "{path}"]
  timeout_seconds: 20
upgrade:
  mirror_global_from_project: true
```

## Repository metadata (`skills.json`)

Each skill repository can expose `skills.json` for indexing/tracking.

See `examples/skills.json`.

Curation labeling rules:
- Curated source + stars >= threshold => curated
- Below threshold => non-curated label
- `curated_early: true` (or `curated: true`) marks curated despite stars

## Testing

```bash
go test ./...
```

## Contributing

See `CONTRIBUTING.md`.

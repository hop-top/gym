# gym

Universal package manager for [agentskills.io](https://agentskills.io) skills.

> **Status:** Active development. Usable today, with some rough edges as features evolve.

- Module/binary: `hop.top/gym` / `gym`
- Validation gate: `skills-ref test`
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

### Registry update

`gym update` refreshes local registry references and reports:
- new skills since last update (with origin)
- installed-skill upgrade candidates by version

### Warmup (Progressive Disclosure)

`gym warmup` generates lightweight skill metadata for context-efficient agent startup:
- `--format xml` → `<available_skills>` XML for Claude Code
- `--format json` → JSON array of skill metadata
- `--format agents-md` → AGENTS.md format for multi-agent compatibility
- `--scope all|global|project` → filter by install scope
- `-o FILE` → write to file (default: stdout)

Implements the agentskills.io progressive disclosure pattern: agents load only name+description at startup (~100 tokens), then fetch full skill content on-demand.

### Learn sources

`gym learn` supports:
- skill names from local merged registry index
- direct URLs (`https://`, `ssh://`, `git@`, `file://`) and local directories
- default target is project-local install in `CWD/.gym/skills`
- use `--global` (or `-g`) for global install scope
- if multiple install paths match the chosen scope, `gym learn` installs into all of them
- use `--agent` patterns (or `--path`) to filter install targets

Install only succeeds if validator passes (`skills-ref test {path}` by default).

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

### CLI parsing/config

- Command tree and aliases are implemented with Cobra.
- Runtime CLI overrides are loaded with Viper from:
  - `--config <file>` (if provided)
  - `.gym/cli.yaml` in current working directory
  - `$XDG_CONFIG_HOME/gym/cli.yaml`
- Env overrides are supported via `GYM_*` keys (for example `GYM_LEARN_GLOBAL=true`).

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

Daemon mode:

```bash
gym serve --daemon --host 127.0.0.1 --port 4873
gym serve --status --host 127.0.0.1 --port 4873
gym serve --stop --host 127.0.0.1 --port 4873
```

## Configuration

User config (XDG):
- `$XDG_CONFIG_HOME/gym/config.json`
- fallback: `~/.config/gym/config.json`

Project config:
- `<project-root>/.gym/config.json`

Project state:
- `<project-root>/.gym/installed.json`

Global state:
- `$XDG_STATE_HOME/gym/installed-global.json`

Local registry index cache:
- `$XDG_DATA_HOME/gym/registry-index.json`

### Example `config.json`

```json
{
  "repositories": [
    {
      "name": "official-curated",
      "url": "https://registry.example.com/curated/skills.json",
      "curated": true
    },
    {
      "name": "community-pack",
      "url": "https://example.com/community/skills.json",
      "curated": false
    }
  ],
  "install_paths": [
    {
      "name": "claude-global",
      "path": "~/.claude/skills",
      "scope": "global"
    },
    {
      "name": "claude-project",
      "path": ".claude/skills",
      "scope": "project"
    }
  ],
  "curation": {
    "min_stars": 100
  },
  "validator": {
    "command": ["skills-ref", "test", "{path}"],
    "timeout_seconds": 20
  },
  "upgrade": {
    "mirror_global_from_project": true
  }
}
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

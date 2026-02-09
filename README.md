# skills

Universal package manager for [agentskills.io](https://agentskills.io) skills.

- Module/binary: `hop/skills` / `gym`
- Validation gate: `skills-ref test`
- Supports curated registries and direct URL installs
- XDG-first paths with user + project hierarchical config
- Scope-aware upgrades with project/global mirror policy
- Interactive management via Charmbracelet `tui` and `repl`

## Install / Build

```bash
go build -o gym ./cmd/gym
```

## Commands

```bash
gym update
gym install [--global|--project] [--path NAME_OR_PATH] <skill-or-url> [...]
gym upgrade [--no-global] [pattern ...]
gym coach hire [--global|-g] [--name NAME] [--curated] [--no-update] <manifest-url|owner/repo>
gym coach list
gym coach dismiss [--global|-g] [--no-update] <name|url>
gym paths
gym paths add [--scope global|project] [--target user|project] [--name NAME] --path PATH
gym serve [--host 127.0.0.1] [--port 4873] [--daemon] [--pidfile PATH] [--logfile PATH]
gym tui
gym repl
```

## Behavior Highlights

### Registry update

`gym update` refreshes local registry references and reports:
- new skills since last update (with origin)
- installed-skill upgrade candidates by version

### Install sources

`gym install` supports:
- skill names from local merged registry index
- direct URLs (`https://`, `ssh://`, `git@`, `file://`) and local directories

Install only succeeds if validator passes (`skills-ref test {path}` by default).

### Upgrade policy

- `gym upgrade` with no args upgrades all matching scope.
- `gym upgrade name1 name2` upgrades named skills.
- Patterns are accepted (`gym upgrade "auth-*"`).
- If run inside a project with installed project skills:
  - project skills are upgraded
  - matching global installs are also upgraded by default
  - use `--no-global` to disable global mirror
- If run outside a project with skills:
  - global skills only

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
- `$XDG_CONFIG_HOME/skills/config.json`
- fallback: `~/.config/skills/config.json`

Project config:
- `<project-root>/.gym/config.json`

Project state:
- `<project-root>/.gym/installed.json`

Global state:
- `$XDG_STATE_HOME/skills/installed-global.json`

Local registry index cache:
- `$XDG_DATA_HOME/skills/registry-index.json`

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

# gym: User Guide

`gym` learns and trains skills that follow the [agentskills.io](https://agentskills.io) spec.

## Install

```bash
go install hop.top/gym/cmd/gym@latest
```

Build from source:

```bash
go build -o gym ./cmd/gym
```

## Prereq: `skills-ref`

Installs are gated by validation (default: `skills-ref test {path}`).

If you use a different validator, set it in user/project config:

```json
{
  "validator": {
    "command": ["skills-ref", "test", "{path}"],
    "timeout_seconds": 20
  }
}
```

## Concepts

### Coaches (registry sources)

A coach is a URL to a `skills.json` manifest. Coaches are the equivalent of “taps” in Homebrew.

- `gym coach hire …` adds a coach to config.
- `gym coach dismiss …` removes a coach from config.
- `gym update` fetches all configured coaches and merges them into a local index.

`gym coach hire` supports:
- `owner/repo` shorthand for GitHub (`main`/`master` raw `skills.json` candidates)
- a direct `https://…/skills.json` URL
- a URL ending in `/` (it will append `skills.json`)

`--name` sets the local label for this coach entry in your config. If omitted, `gym` tries to read `repository.name` from the manifest; if missing, it derives a name from the URL/repo.

### Scopes

Two scopes exist for *installed skills*:
- `global`: installs into your global IDE/TDE path (or whatever you configured)
- `project`: installs into the current project’s configured path(s)

Two scopes exist for *config*:
- user (XDG): `~/.config/gym/config.json` (or `$XDG_CONFIG_HOME/gym/config.json`)
- project: `<project-root>/.gym/config.json`

Project roots are detected by presence of `.gym/config.json` or `.gym/installed.json`.

## Quickstart

1. Hire a coach:

```bash
gym coach hire owner/repo
```

To hire globally (user config):

```bash
gym coach hire --global owner/repo
```

2. Update local index:

```bash
gym update
```

3. Learn a skill by name:

```bash
gym learn acme-auth
```

Or learn directly from a URL / local dir:

```bash
gym learn https://github.com/acme/acme-auth-skill.git
gym learn /path/to/local/skill
```

## Common Commands

### Update registry index

```bash
gym update
```

### Learn

```bash
gym learn <skill-name>
gym learn <git-url|file-url|dir>
```

Scope selection:
- default: installs to project-local `CWD/.gym/skills`
- override: `gym learn --global …` (or `gym learn -g …`)
- compatibility aliases: `gym install ...`

Target selection:
- if multiple install paths match the chosen scope, `gym learn` installs into all of them
- filter with `--agent` patterns (prefix with `!` to exclude)
- override a single path with `--path NAME_OR_PATH`

### Train

```bash
gym train              # all
gym train auth-*       # patterns ok
gym train skill1 skill2
```

Train mirror policy:
- if run inside a project with project-installed skills: upgrade project skills
- by default, also upgrades matching globally-installed skills
- disable global mirror: `gym train --no-global`
- compatibility aliases: `gym upgrade ...`

### CLI config (Viper)

You can provide CLI defaults via Viper:

- `--config <file>`
- `.gym/cli.yaml` in current working directory
- `$XDG_CONFIG_HOME/gym/cli.yaml`

Example:

```yaml
learn:
  global: false
  path: ""
  agents: []
train:
  no_global: false
```

Environment overrides:
- `GYM_LEARN_GLOBAL=true`
- `GYM_TRAIN_NO_GLOBAL=true`

### Install paths

List configured + detected IDE/TDE paths:

```bash
gym paths
```

Add a global install path:

```bash
gym paths add --scope global --path ~/.claude/skills
```

Add a project install path (writes to project config by default):

```bash
gym paths add --scope project --path .claude/skills
```

### Warmup (Progressive Disclosure)

Generate lightweight skill metadata for context-efficient agent startup:

```bash
gym warmup                          # XML to stdout (default)
gym warmup --format json            # JSON array of metadata
gym warmup --format agents-md       # AGENTS.md format
gym warmup -o skills.xml            # Write to file
gym warmup --scope global           # Only global skills
gym warmup --scope project          # Only project skills
```

**What is progressive disclosure?**

Following the [agentskills.io specification](https://agentskills.io/specification#progressive-disclosure), agents should:
1. Load only skill **metadata** at startup (~100 tokens per 50 skills)
2. Fetch **full content** on-demand when needed (~5000 tokens per skill)

This keeps agent context clean and reduces startup latency.

**Example workflow:**
```bash
# Generate XML manifest for Claude Code
gym warmup --format xml -o ~/.claude/skills-manifest.xml

# Generate AGENTS.md for multi-agent compatibility
gym warmup --format agents-md -o .agents/AGENTS.md

# Generate JSON for custom tooling
gym warmup --format json > skills.json
```

### Serve a registry

Serve your local merged index as a `skills.json` endpoint:

```bash
gym serve --host 127.0.0.1 --port 4873
```

Endpoints:
- `GET /skills.json`
- `GET /_index.json` (debug)
- `GET /health`

## Files and Locations

- User config: `$XDG_CONFIG_HOME/gym/config.json` (fallback `~/.config/gym/config.json`)
- Project config: `<project-root>/.gym/config.json`
- Global state: `$XDG_STATE_HOME/gym/installed-global.json`
- Project state: `<project-root>/.gym/installed.json`
- Local registry cache: `$XDG_DATA_HOME/gym/registry-index.json`

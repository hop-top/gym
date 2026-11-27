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

Installs are gated by validation (default: `skills-ref validate {path}`).

If you use a different validator, set it in user/project config:

```json
{
  "validator": {
    "command": ["skills-ref", "validate", "{path}"],
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
- user: `config.yaml` in `$XDG_CONFIG_HOME/gym` (else `~/.config/gym` on Linux, `~/Library/Application Support/gym` on macOS)
- project: `<project-root>/.gym/config.yaml`

A system-wide `/etc/gym/config.yaml` is read first. Precedence, lowest to highest: defaults, system, user, project, `-c <file>`, `-c key=value`. Lists (`repositories`, `install_paths`) are replaced by the highest layer that sets them, not appended.

Project roots are detected by presence of `.gym/config.yaml`, `.gym/config.json` or `.gym/installed.json`.

The canonical file name is `config.yaml`. An existing `config.json` (user or project) is still read when no `config.yaml` is beside it, and `config.yaml` wins when both exist. Commands that change config write `config.yaml`, first copying the old `config.json` content into it; the old file is never modified. Likewise the previous directories (`~/.config/gym`, `~/.local/share/gym`, `~/.local/state/gym`, `~/.cache/gym`) keep being used, all four together, when any of them exists and the matching `XDG_*` variable is unset (a set variable always wins).

Layer extra config for one run with `-c` (repeatable; listed in `gym --help-all`):

```bash
gym -c curation.min_stars=50 paths
gym -c ./team.yaml paths
gym -c 'install_paths=[{name: x, path: /opt/skills, scope: global}]' paths
```

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

## Trust and safety

### Coach scoring (rsx)

`gym coach hire` now scores coaches using rsx's repository analysis and policy evaluation:

- **Default behavior**: scores are cached for 24 hours. If rsx is unavailable, the hire proceeds with a warning.
- `--refresh-judgement`: bypass the cache and re-run rsx.
- `--strict`: fail if rsx is unreachable or times out (fail-closed safety).
- `--insecure`: lower DENY verdicts to WARN, allowing risky coaches to proceed (use with caution).
- `--gate-timeout=30s`: override the 30-second wait for rsx to finish (configurable per environment).

`gym coach list` shows the cached score: `score=N/100 (verdict)` per coach.

### Policy guards (CEL)

`gym learn` and `gym train` now gate installations on policy rules designed to catch risky patterns:

- **Default behavior**: warn on violations but allow install. Policy guards check for: `sudo`/`doas`/`pkexec`, `curl|bash` pipes, secret leakage (AWS keys, GitHub tokens), egress in shell hooks, large binaries, suspicious chmod, `/etc/hosts` edits, postinstall scripts, package installer shells.
- `--strict`: fail (deny) on any violation; allow only on explicit ALLOW verdicts.
- `--insecure`: downgrade DENY verdicts to WARN, allowing risky skills to install.
- `--gate-timeout=30s`: override the default 30-second wait for guard evaluation.

List installed policies: `gym guard list`.

Test a skill against all policies (dry-run): `gym guard test <skill-directory>` or `gym guard test manifest.json` (exit 0 = all allow, exit 1 = deny/warn).

### Skill admission

After the validator passes, `gym learn` and `gym train` admit each skill:

- **Digest**: sha256 over the skill's sorted file listing (`gym digest <dir>`; `--files` shows the listing). A registry entry may pin it with `"digest": "sha256:<hex>"`; a differing digest rejects the install. No pin: warning, digest recorded in installed state.
- **Manifest** (optional `gym.json` at the skill root):

  ```json
  {"schema": "gym.skill/v1", "allowed_tools": ["Read", "Bash(git:*)"], "data_class": "internal"}
  ```

  `data_class` is one of `public < internal < confidential < personal < special_category`. `allowed_tools` must cover every token in SKILL.md `allowed-tools`. Unknown fields, and the operator-only fields `digest` and `approval_state`, are rejected. This checks consistency; it cannot observe what a skill does at runtime.
- **Data class ceiling**: `admission.max_data_class` (default `internal`) or `--max-data-class`. A skill above it is denied.
- **Approval**: `gym guard approve <skill|dir>` records approval for the exact digest; any content change returns the skill to unreviewed. `gym guard revoke <skill|dir>` blocks a skill (all digests) or, with `--digest`, one digest. Records live in `<config-dir>/gym/approvals.json`.
- **No manifest**: `admission.require_manifest` = `warn` (default), `off`, or `reject`.

Config (`config.yaml`; a project config can only lower the ceiling, turn approval on, or raise the manifest requirement, relative to what defaults, system and user resolve to):

```yaml
admission:
  max_data_class: confidential
  require_approval: false
  require_manifest: warn
```

Precedence, lowest to highest: defaults < system < user < project (clamped) < `-c <file>` < `-c key=value` < `--max-data-class`. The command-line layers are operator-trusted, so `-c admission.max_data_class=confidential` may raise the ceiling over a project value. A legacy `config.json` is read for these keys like any other. An invalid value is a config error before any install.

Hard failures (`manifest-invalid`, `digest-mismatch`, `digest-unsupported-entry`, `approval-revoked`) are not relaxed by `--insecure` and do not depend on the event bus. The rest are policies (`gym guard list`): `--insecure` lowers denies to warnings, `--strict` turns warnings into denies. Every rejection exits 1.

Dry-run: `gym guard test <skill-dir>` prints verdict, findings, and the computed facts; `--max-data-class`, `--approval-state unreviewed|approved|revoked`, and `--expect-digest sha256:<hex>` simulate operator state. `gym guard test payload.json` still evaluates a raw payload.

### Opting out of gates

Coaches and skills pass through gates by default. To run outside the safety checks:

- `gym coach hire --insecure …` — proceed even on DENY verdicts.
- `gym learn --insecure …` — proceed even on DENY verdicts.
- `gym train --insecure …` — proceed even on DENY verdicts.

For project-level policy relaxation, create `.gym/policies/policies.yaml`:

```yaml
relax: ["no-large-binary"]  # allow large binaries without re-opt-in
disable: ["no-postinstall-script"]  # completely disable this rule
```

## Cheat sheets and inline hints

### gym cheat

View hints for gym's commands and installed skills:

```bash
gym cheat warmup              # hints on `gym warmup` usage
gym cheat learn               # hints on `gym learn` usage
gym cheat                     # list all available topics
```

Cheat sheets are sourced from gym's command structure and the installed skills' metadata (from SKILL.md when available).

### Integrating with tip (optional)

If you use tip (hop.top/tip), export gym's cheat-sheet adapter:

```bash
gym cheat --tip-export
# Writes ~/.config/tip/adapters/gym.json
```

Then enable the bridge to receive inline hints when you run gym commands:

```bash
export GYM_TIP_BUS_BRIDGE=1
gym learn …
# tip can now suggest gym-related hints based on observed commands
```

To disable, unset the env var or don't export the adapter.

## Event bus and daemon mode

`gym serve --daemon` persists events to a local SQLite bus at `$XDG_STATE_HOME/gym/bus.sqlite`:

- `--bus memory` — events live in RAM (default for CLI; lost on exit).
- `--bus sqlite` — events persist to disk (useful for daemons and debugging).
- `--bus network` — connect to a remote bus via `GYM_BUS_URL` env (multi-host deployments).

Most users stay on the default memory bus. Daemons and multi-host setups use SQLite or network.

## Files and Locations

- System config: `/etc/gym/config.yaml`
- User config: `config.yaml` in `$XDG_CONFIG_HOME/gym` (else `~/.config/gym` on Linux, `~/Library/Application Support/gym` on macOS)
- Project config: `<project-root>/.gym/config.yaml`
- Global state: `$XDG_STATE_HOME/gym/installed-global.json`
- Project state: `<project-root>/.gym/installed.json`
- Local registry cache: `$XDG_DATA_HOME/gym/registry-index.json`
- Coach judgement cache: `$XDG_CACHE_HOME/gym/judgements/<sha256>.json` (24-hour TTL; bypass with `--refresh-judgement`)
- Policy overrides (user): `$XDG_CONFIG_HOME/gym/policies/` (YAML files)
- Policy overrides (project): `<project-root>/.gym/policies/` (YAML files)
- Daemon bus (SQLite): `$XDG_STATE_HOME/gym/bus.sqlite` (used when `--bus sqlite` or daemon mode)

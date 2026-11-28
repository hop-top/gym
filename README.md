# gym

> [!WARNING]
> **🚧 Do Not Use — History Will Be Rewritten 🚧**
>
> This repo is undergoing major restructuring as we selectively
> open-source internal tools built at
> [Idea Crafters LLC](https://ideacrafters.com). Git history **will be
> force-pushed and rewritten** multiple times. Do not fork, clone, or
> depend on this repo in any capacity until we tag a stable release.

Package manager for [agentskills.io](https://agentskills.io) skills: install, pin, vet,
upgrade and remove skills for every agent host on a machine.

[![CI](https://github.com/hop-top/gym/actions/workflows/ci.yml/badge.svg)](https://github.com/hop-top/gym/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Why

A skill is a folder. Each agent host reads its own (`~/.claude/skills`, `~/.cursor/skills`,
…), so skills get copied by hand: no version, no record of origin, nothing checking the
scripts they ship.

gym gives skills what packages already have:

- **One install, every host**: `gym learn` writes to each install path of the scope;
  `--agent` filters them
- **Any source**: registry name, git URL, archive, local directory; pin with `name@version`,
  `url#ref` or `--digest`
- **Reproducible**: a lock per scope records source, commit and content digest; `gym learn`
  with no arguments restores it
- **Gated**: validator, content digest, optional manifest, CEL policies and operator
  approval run before a skill is installed
- **Scoped**: global, project and folder scopes; inheritance resolved at read time, never
  by copying
- **Cheap at agent startup**: `gym warmup` emits name + description only

Limits: checks are static, gym cannot observe what a skill does at runtime. The network
policy matches host names; a firewall remains the guarantee.

## Install

Requirements:

- Go 1.26.1+ and `git`
- a skill validator on `PATH`. Default: `skills-ref validate`
  ([agentskills/skills-ref](https://github.com/agentskills/agentskills/tree/main/skills-ref)).
  Without one, installs fail with `validation failed`. Another validator: set
  `validator.command` in user config (see [Configuration](#configuration))

```bash
go install hop.top/gym/cmd/gym@latest
```

From source:

```bash
make build        # go build -o gym ./cmd/gym
```

## Quickstart

```bash
gym coach hire owner/repo       # add a registry source (a skills.json); refreshes the index
gym learn acme-auth             # install into ./.gym/skills, record in ./.gym/lock.json
gym list                        # skills installed in this scope
gym warmup --format agents-md   # name + description of each skill, for agent startup
gym train                       # upgrade
gym unlearn acme-auth           # remove
```

No registry needed:

```bash
gym learn https://github.com/acme/acme-auth-skill.git#v1.2.0   # branch, tag or commit
gym learn ./skills/acme-auth                                   # directory
gym learn ./acme-auth.tar.gz                                   # archive (.tar.gz, .tgz, .zip)
gym learn -g acme-auth                                         # global scope
```

Commit `.gym/lock.json`. On a fresh clone, `gym learn` with no arguments installs the same
bytes.

## Use cases

Worked examples with real output: [docs/examples.md](docs/examples.md).

| Goal | Example |
|------|---------|
| Same skills on every clone and in CI | [Share a project's skills through the repository](docs/examples.md#1-share-a-projects-skills-through-the-repository) |
| Claude Code, Cursor and others from one install | [One skill, several agent hosts](docs/examples.md#2-one-skill-several-agent-hosts) |
| Hold a reviewed version, let the rest follow | [Pin some skills, track others](docs/examples.md#3-pin-some-skills-track-others) |
| Check a skill from outside before it lands | [Vet a third-party skill before installing](docs/examples.md#4-vet-a-third-party-skill-before-installing) |
| Company-wide, unit and project skills | [Skills per folder](docs/examples.md#5-company-unit-project-skills-per-folder) |
| Private catalogue, machines limited to your hosts | [Run a registry for a team](docs/examples.md#6-run-a-registry-for-a-team) |
| Small context at agent startup | [Give an agent its skill list](docs/examples.md#7-give-an-agent-its-skill-list-at-startup) |

## Agent skill

gym ships with a skill that teaches an agent to drive it:
[`skills/gym-cli/SKILL.md`](skills/gym-cli/SKILL.md). One short file: when to reach for
which command, and what `--help` does not say.

Install it globally, from a checkout:

```bash
gym learn -g ./skills/gym-cli     # every detected host directory
```

Or copy it, as with any skill:

```bash
mkdir -p ~/.claude/skills
cp -r skills/gym-cli ~/.claude/skills/
```

## Commands

| Command | Aliases | Purpose |
|---------|---------|---------|
| `learn [source ...]` | `install` | Install by name, URL, archive or directory; no arguments: restore from the lock |
| `train [pattern ...]` | `upgrade` | Upgrade installed skills |
| `unlearn <pattern ...>` | `uninstall`, `remove`, `rm` | Remove installed skills |
| `list` | `ls` | List one scope; `--effective` the whole chain; `--check` drift against the lock |
| `update` | | Refresh the local registry index |
| `coach hire`, `coach list`, `coach dismiss` | | Manage registry sources |
| `paths`, `paths add` | | List or add install paths |
| `warmup` | | Skill metadata as `xml`, `json` or `agents-md` |
| `guard list`, `guard test`, `guard approve`, `guard revoke` | | Policies, dry-run, operator approvals |
| `digest <dir or archive>` | | Print the content digest; `--files` adds the listing |
| `serve` | | Serve the merged index over HTTP |
| `cheat [topic]` | | Hints for gym commands and installed skills |
| `tui`, `repl` | | Interactive management |
| `version`, `completion` | | Version; shell completion script |

Flags: `gym <command> --help`. Every global flag: `gym --help-all`.

### Global flags

| Flag | Effect |
|------|--------|
| `--strict` | Fail closed: warnings and gate timeouts deny |
| `--insecure` | Lower DENY verdicts to WARN (hard failures stay) |
| `--gate-timeout DURATION` | Max wait for a gate (coach judgement, policy guard); default `30s` |
| `--refresh-judgement` | Bypass the coach judgement cache |
| `--max-data-class CLASS` | Data class ceiling for admission; overrides config |
| `--offline` | No network access (`network.mode: offline`) |
| `--bus memory`, `sqlite` or `network` | Event bus adapter; default env-driven, else `memory` |
| `-c, --config FILE` or `KEY=VALUE` | Layer extra config for one run (repeatable) |

Also: `--format`, `--quiet`, `--no-color`, `--no-hints`, `-V`. Under `--help-all`: `--cols`,
`-C`, `--progress-format` and more.

## How it works

Full reference: [docs/user.md](docs/user.md).

### Sources and pins

- registry name from the merged local index; `name@version` pins an exact version
- git URL (`https://`, `ssh://`, `git@`, `file://`); `url#ref` pins a branch, tag or commit
- archive (`.tar.gz`, `.tgz`, `.zip`) as a path, `file://` URL or https URL
- local directory
- `--digest sha256:HEX` pins any source to its content digest

`gym update` refreshes the index from every coach and reports new skills since the last
update (with origin) and upgrade candidates for installed skills. Details:
[Learn](docs/user.md#learn).

### Scopes and install paths

- **project** (default): `CWD/.gym/skills`
- **global** (`-g`): global install paths
- **folder** (`--dir FOLDER`): a project scope rooted at a folder you choose, for work
  organised as a tree (company, unit, project). Ancestors are inherited when gym reads
  (`warmup`, `list --effective`), nearest wins

With several install paths in a scope, `gym learn` installs into all of them; `--agent`
patterns or `--path` narrow the set. `gym paths` lists configured paths and detects these
global ones:

- `~/.claude/skills`
- `~/.cursor/skills`
- `~/.gemini/skills`
- `~/.codex/skills`
- `~/.windsurf/skills`

Details: [Folder scopes](docs/user.md#folder-scopes).

### Lock

`gym learn` records each install in `<root>/.gym/lock.json` (global:
`<config-dir>/gym/lock.json`): source, requested pin, version, commit, content digest.
Sorted, no timestamps, meant to be committed.

- `gym learn` (no arguments): restore from the lock without reading the registry index
- `gym learn --frozen`, `gym list --check`: report `missing`, `digest-drift`, `modified`
  (exit 1) and `untracked` (warning)
- `lock.frozen: true`: refuse any change to the lock

Details: [Lock and pins](docs/user.md#lock-and-pins).

### Train

- no arguments: every skill of the scope; names and patterns (`gym train "auth-*"`) narrow
- inside a project: project skills, plus matching global installs (`--no-global` disables)
- outside a project: global skills only
- `--dir FOLDER -r`: that folder scope and every scope below it, each on its own
- skills pinned explicitly (`name@version`, `url#ref`) are skipped; `--frozen` changes
  nothing and fails if an upgrade is available

### Trust gates

Every install and upgrade passes, in order:

1. **Validator**: `skills-ref validate {path}` by default
2. **Admission**: content digest against its pin, optional `gym.json` manifest
   (`allowed_tools`, `data_class`), data class ceiling (default `internal`), operator
   approval bound to the exact digest
3. **Policies** (CEL):
   - declared `postinstall` / `shell_hook`: sudo, `curl | bash`, `/etc/hosts` edits,
     suspicious chmod, package installer shells, egress in hooks
   - script files (`#!`, `.sh`, `.py`, `.js`, …): same command patterns, warn only
   - text files: secrets (deny); large binaries (warn)
   - not scanned: SKILL.md prose for commands

Only the operator relaxes a gate: user policies in `$XDG_CONFIG_HOME/gym/policies/` may
redefine, disable or relax; project policies may only add. A skill cannot approve itself.

`gym coach hire` also scores the coach repository with `rsx` (cached 24h; shown by
`gym coach list`). Without `rsx` the hire proceeds with a warning; `--strict` fails
instead.

```bash
gym guard list                 # effective policies
gym guard test ./my-skill      # dry-run admission and policies
gym guard approve ./my-skill   # approve this exact digest
```

Details: [Trust and safety](docs/user.md#trust-and-safety).

### Network policy

`network.mode: open|allowlist|offline` with `network.allow_hosts` limits the hosts gym
contacts: registries, archives, git, the event bus. Project and folder configs can only
tighten. For a machine that must reach only your own hosts, combine `allowlist` with
`admission.require_pin: remote`: [Isolated deployment](docs/user.md#isolated-deployment).

### Warmup

`gym warmup` implements agentskills.io progressive disclosure: agents load name +
description at startup, full skill content on demand.

```bash
gym warmup                                  # <available_skills> XML
gym warmup --format agents-md -o AGENTS.md
gym warmup --format json --scope project
```

### Serving a registry

```bash
gym serve --host 127.0.0.1 --port 4873
```

- `GET /skills.json`: every version of each skill, with `digest` when the index has one
- `GET /_index.json`: full merged index, for debugging
- `GET /health`

`--update` refreshes the index on start, `--refresh 10m` periodically; `--name` sets
`repository.name`. Daemon mode (SQLite bus):

```bash
gym serve --daemon --host 127.0.0.1 --port 4873
gym serve --status --host 127.0.0.1 --port 4873
gym serve --stop --host 127.0.0.1 --port 4873
```

### Events

gym publishes installs, coach hires, policy verdicts and hints on an event bus (`--bus`),
so other tools (`rsx`, `tip`, policy validators) coordinate without tight coupling.
`gym cheat --tip-export` writes the `tip` adapter; `GYM_TIP_BUS_BRIDGE=1` enables the
bridge. Details: [Event bus and daemon mode](docs/user.md#event-bus-and-daemon-mode).

## Configuration

YAML, layered lowest to highest:

1. built-in defaults
2. system: `/etc/gym/config.yaml`
3. user: `config.yaml` in `$XDG_CONFIG_HOME/gym` (else `~/.config/gym` on Linux,
   `~/Library/Application Support/gym` on macOS)
4. project: every `.gym/config.yaml` at or above the working directory (up to `$HOME`),
   outermost first
5. extra files: `-c <file>` (repeatable, in order; a missing file is an error)
6. overrides: `-c key=value` (repeatable; dotted keys nest; value parses as YAML)

Lists (`repositories`, `install_paths`) are replaced by the highest layer that sets them,
not appended.

```yaml
repositories:
  - name: official-curated
    url: https://registry.example.com/curated/skills.json
    curated: true
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
admission:
  max_data_class: internal   # ceiling
  require_manifest: warn     # off | warn | reject
  require_approval: false
  require_pin: none          # none | remote | all
network:
  mode: open                 # open | allowlist | offline
lock:
  frozen: false
```

```bash
gym -c curation.min_stars=50 paths
gym -c /path/to/extra.yaml paths
gym -c 'install_paths=[{name: x, path: /opt/skills, scope: global}]' paths
```

### Project files are untrusted

A project file arrives with a clone; whoever cloned it did not write it. So a project or
folder config:

- may set only `install_paths`, `repositories` (without `auth`), `curation`, `upgrade`
- may only tighten `admission` and `network`, and only turn `lock.frozen` on
- has every other key, `validator` included, ignored with a warning
- must keep `install_paths` relative and inside the project root

User and system files, `-c <file>` and `-c key=value` are the operator's and unrestricted.
Other repository content gets the same distrust: `.gym/cli.yaml` and `.gym/policies/` are
restricted, `.gym/installed.json` and `.gym/lock.json` are checked before use. Details:
[Scopes](docs/user.md#scopes).

### CLI defaults

`cli.yaml` sets flag defaults: `$XDG_CONFIG_HOME/gym/cli.yaml` (unrestricted) and
`.gym/cli.yaml` (project: only `learn.agents`, `train.no_global: true`, `strict: true`).
Flags win over both. Environment: `GYM_*` keys, for example `GYM_LEARN_GLOBAL=true`.

### Files

| What | Where |
|------|-------|
| User config, policies | `$XDG_CONFIG_HOME/gym/config.yaml`, `$XDG_CONFIG_HOME/gym/policies/` |
| Project config, policies | `<root>/.gym/config.yaml`, `<root>/.gym/policies/` |
| Lock | `<root>/.gym/lock.json`; global: `<config-dir>/gym/lock.json` |
| Installed state | `<root>/.gym/installed.json`; global: `$XDG_STATE_HOME/gym/installed-global.json` |
| Registry index cache | `$XDG_DATA_HOME/gym/registry-index.json` |
| Approvals | `<config-dir>/gym/approvals.json` |

Full list: [Files and Locations](docs/user.md#files-and-locations).

## Publishing skills (`skills.json`)

A coach is a URL to a `skills.json` manifest listing skills, versions and sources. Example:
[`examples/skills.json`](examples/skills.json).

Curation labels:

- curated source and stars >= `curation.min_stars`: curated
- below the threshold: non-curated
- `curated_early: true` (or `curated: true`): curated despite stars

## Docs

- User guide: [docs/user.md](docs/user.md)
- Use cases: [docs/examples.md](docs/examples.md)
- Dev guide: [docs/dev.md](docs/dev.md)
- Agent notes: [docs/agent.md](docs/agent.md)

## Development

```bash
make build   # go build -o gym ./cmd/gym
make test    # go test ./...
make fmt vet
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).

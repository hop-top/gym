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

If you use a different validator, set it in user or system config (or with `-c`). A project config cannot set it: `validator` there is ignored with a warning, because it names a command to run.

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

Project roots are detected by presence of `.gym/config.yaml`, `.gym/config.json` or `.gym/installed.json`. A root's `.gym/config.yaml` applies to that folder and, merged as described under Folder scopes, to every folder below it.

A project file arrives with a clone, so it is not trusted like your own config. A project config can set only `install_paths`, `repositories` (without `auth`: a project's `auth` is ignored with `config: ignoring auth of repository "<name>" in project layer <path> (user/system/-c only)`), `curation`, `upgrade`, `lock.frozen` (on only), `admission` and `network` (both can only tighten). Every other key, including `validator`, is ignored with `config: ignoring project key "<key>" (user/system/-c only)`. Project `install_paths` must be relative and stay inside the project root: an absolute path, a `..` that leaves the tree, a `~` path, or a symlink out of the tree is ignored with `config: install path <p> escapes project <root>`. Relative project paths resolve against the scope root (the folder gym installs into, see Folder scopes), not the directory you run gym from. `.gym/skills` must not be a symlink out of the tree either. Absolute and `~` paths stay fine in user and system config and with `-c`.

`.gym/installed.json` is trusted the same way: `gym train` writes or replaces a recorded skill only when its `install_path` is `<install path>/<skill name>`, a real directory (or absent, never a symlink), under the project root for project skills, or under a configured or standard global install path for global skills. Any other record fails with `install-path-untrusted: <path>`, its directory is not touched, and the remaining records still upgrade. `--insecure` does not relax this. `gym warmup` and `gym cheat` apply the same check and skip an untrusted record with a warning, never reading from its directory.

The canonical file name is `config.yaml`. An existing `config.json` (user or project) is still read when no `config.yaml` is beside it, and `config.yaml` wins when both exist. Commands that change config write `config.yaml`, first copying the old `config.json` content into it; the old file is never modified. Likewise the previous directories (`~/.config/gym`, `~/.local/share/gym`, `~/.local/state/gym`, `~/.cache/gym`) keep being used, all four together, when any of them exists and the matching `XDG_*` variable is unset (a set variable always wins).

Layer extra config for one run with `-c` (repeatable; listed in `gym --help-all`):

```bash
gym -c curation.min_stars=50 paths
gym -c ./team.yaml paths
gym -c 'install_paths=[{name: x, path: /opt/skills, scope: global}]' paths
```

### Folder scopes

A folder scope is a `project` scope whose root is a folder you choose: the scope value stays `project`. Use it when work is a tree of folders (company, unit, project) and each level carries its own skills. A folder holds its scope in `<folder>/.gym/` (`installed.json`, `skills/`, optional `config.yaml`, `policies/`).

Choose the folder with `--dir <path>` on `learn`, `train`, `list` and `warmup`. It must be an existing directory and cannot be combined with `-g`/`--global`:

| Command | Default folder (no `--dir`) |
|---------|-----------------------------|
| `learn` | the current directory |
| `train`, `list`, `warmup` | nearest scope root at or above the current directory |

`gym learn` run in a folder with no scope of its own, below a folder that has one, creates a new scope there and says so on stderr: `note: installing into new folder scope <cwd>; nearest existing scope is <root> (--dir <root> to use it)`.

**Inheritance.** gym resolves it when it reads, never by copying skills. The chain of a folder is its own scope, then each ancestor's, innermost first, ending at `$HOME` (included; folders above it are never read) or at the filesystem root for a folder outside `$HOME`; the global scope follows. An ancestor's `.gym` is read only when it belongs to you and is not group- or world-writable; otherwise gym skips it with `scope: ignoring <dir>/.gym (not owned by you or writable by others)`. The folder you point at is not tested. Agent hosts load whatever skill directories they are configured to load; to give a host the effective set, write it out: `gym warmup --dir <folder> --format agents-md -o <folder>/AGENTS.md`.

**Config across the chain.** Every `.gym/config.yaml` on the chain is read, outermost to innermost, on top of user and system config:

| Key | Folded as |
|-----|-----------|
| `admission.max_data_class` | lowest ceiling wins |
| `admission.require_manifest` | strictest wins |
| `admission.require_approval` | on if any folder sets it |
| `admission.require_pin` | strictest wins |
| `network.mode` | strictest wins (`open < allowlist < offline`) |
| `network.allow_hosts` | intersection |
| `install_paths` (scope `project`) | innermost folder that sets it replaces the list |
| `repositories`, `curation`, `upgrade` | innermost wins |
| anything else | ignored |

So an inner folder can tighten what an outer one set, never loosen it. A folder's `install_paths` must be relative and stay inside the folder that wrote it, else `config: install path <p> escapes project <root>` and the entry is ignored. A relative path resolves against the scope root being operated on: an org-level `.claude/skills` installs into `<scope>/.claude/skills` for each folder scope. `-c` files and `-c key=value` still apply last and are not restricted.

`<folder>/.gym/policies` add policies across the chain. A policy name defined by several folders keeps the outermost definition; an inner one is ignored with a warning, as is a policy that would redefine a built-in or user policy.

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

A URL must not carry a password (`https://user:token@host/...` fails with `source-credentials-in-url: use a git credential helper or ssh key`); `git@host:org/repo` and `ssh://git@host/...` are fine. Recorded and printed source URLs have credential-looking query parameters (`token`, `key`, `secret`, `password`, `auth`, ...) replaced by `REDACTED`. The installed copy never contains `.git`: installed bytes are exactly the bytes the content digest covers.

## Common Commands

### Update registry index

```bash
gym update
```

### Learn

```bash
gym learn <skill-name>
gym learn <skill-name>@<version>      # that exact registry version
gym learn <git-url|file-url|dir>
gym learn <git-url>#<branch|tag|commit>
gym learn <archive.tar.gz|.tgz|.zip>  # a path, file:// URL or https URL
gym learn                             # restore every skill the lock lists
```

A pin is part of the argument: `@` for registry names, `#` for git sources. `lint@1.2.0` installs exactly that version from the registry index, or fails with `pin-unavailable: lint@1.2.0 not in index (available: 1.3.0)`. `url#v1.2.0` clones that branch or tag; a full 40- or 64-hex commit is fetched directly. An unknown ref fails with `ref-not-found: <ref> at <url>`. Ranges (`^1.2`) do not exist. Directory and archive sources have no version: pin them by digest (below).

Archives (`*.tar.gz`, `*.tgz`, `*.zip`, as a path, a `file://` URL or an https URL) are extracted into a temporary directory with the standard library and then go through the normal pipeline. The skill root is the archive root, or its single top-level directory when that is the only entry (a root-level dotfile beside the directory means the archive root is the skill root). The digest is over the extracted tree, so `gym digest <archive>` equals `gym digest <dir>` of the same files. An archive is refused as `archive-invalid: <reason>` for an absolute name, `..`, a symlink, hardlink or device entry, a duplicate path (compared case-insensitively), more than 10 000 entries, or more than 256 MiB expanded; nothing is installed. An archive takes no `#ref`. An archive made with macOS `tar` carries `._*` AppleDouble entries beside the files: they count as content (and as extra root entries), so build it with `COPYFILE_DISABLE=1 tar czf ...`.

With a lock entry for the name, `gym learn lint` installs what the lock says (`using locked 1.2.0 (gym train to upgrade)`); see [Lock and pins](#lock-and-pins). `gym learn --digest sha256:<hex> <skill|url|dir>` pins one install to a digest without a lock. `--no-lock` installs without reading or writing the lock.

Scope selection:
- default: installs to project-local `CWD/.gym/skills`
- a folder scope elsewhere: `gym learn --dir <folder> …` installs into `<folder>/.gym/skills` and records state in `<folder>/.gym/installed.json`
- override: `gym learn --global …` (or `gym learn -g …`); `--dir` and `-g` exclude each other
- compatibility aliases: `gym install ...`

Target selection:
- if multiple install paths match the chosen scope, `gym learn` installs into all of them
- filter with `--agent` patterns (prefix with `!` to exclude)
- override a single path with `--path NAME_OR_PATH`

### Unlearn

```bash
gym unlearn lint
gym unlearn 'auth-*' --yes
gym unlearn lint -g --dry-run
gym unlearn lint --dir ../other-project
```

- aliases: `gym uninstall`, `gym remove`, `gym rm`
- needs at least one name or pattern (`filepath.Match`, as `train`); removing everything takes an explicit `'*'`
- scope and targets as `learn`: project by default (`CWD/.gym`), `--global`/`-g`, or `--dir FOLDER` (not combinable with `-g`); the scope is exactly that folder, never an ancestor: a skill installed only at a parent is `skill-not-installed: ... (installed in: <parent>, global)`; `--path NAME_OR_PATH`, `--agent` patterns (prefix `!` excludes) pick the install paths to remove from
- `-n/--dry-run` prints `would remove <skill> <dir>` lines and changes nothing
- an explicit name never prompts; a pattern matching more than one skill asks `remove N skills from <root>? [y/N]` on a terminal, and without one fails with `confirmation-required: pattern matched N skills; pass --yes`. `-y/--yes` skips the prompt
- the lock entry goes last: once no record of the skill remains in the scope, its entry is removed from that scope's lock (`<root>/.gym/lock.json`, or the global lock). `--keep-lock` leaves it, so the next `gym learn` restores the skill. A skill that is refused or fails to remove keeps its entry; with `--dry-run` nothing changes
- per install directory, in order: the record's `install_path` must pass the state trust check (`install-path-untrusted: <path>`, the directory is never touched); the directory is re-hashed against the digest recorded at install, and a difference fails with `skill-modified: <dir> differs from recorded <digest>; --force to remove` (a record with no digest cannot be verified and needs `--force` too; a tree holding only directories, such as one left by an interrupted removal, has nothing to lose and skips the check); the directory is removed; then its record is dropped from `installed.json`, which stays even with an empty list
- `--force` removes despite local modification; it never relaxes the trust check
- a skill that is not installed fails with `skill-not-installed: <name> in <root>`, plus `(installed in: global)` when the other scope has it; `--ignore-missing` makes that exit 0
- a failure does not stop the other records or skills; failed records stay in `installed.json`, each failure is printed, exit is 1. A rerun is safe
- kept: approvals, config, the registry index

### Train

```bash
gym train              # all
gym train auth-*       # patterns ok
gym train skill1 skill2
gym train --dir org/unit      # that folder scope
gym train --dir org -r        # that folder scope and every scope below it
gym train --dir org -r --depth 3
```

`gym train` leaves a skill alone when its lock entry was requested explicitly (`learn lint@1.2.0`, `learn url#v1`): `skip lint: pinned to 1.2.0 (gym learn lint@<new> to change)`. Other skills upgrade and their lock entry is rewritten (a skill the lock does not list stays unlisted). Directory and git installs are not in the registry and are skipped as before. `--frozen`, or `lock.frozen: true`, makes `train` change nothing: it exits 0 only if no upgrade is available, else `lock-frozen: train would modify <lock>`.

`--recursive` (`-r`) walks below the scope root for folders holding `.gym/installed.json` (it skips `.git`, `node_modules` and `.gym`, and does not follow symlinks; `--depth` bounds the levels, default 8). Each scope is planned and upgraded on its own, in path order. A scope that fails is reported with its path and the others still run; the exit code is 1 if any failed. Each scope's upgrades are gated by that scope's own policy chain (the folders' `.gym/policies` from the outermost scope down), as when `gym train` runs inside it; a scope whose engine cannot be built is gated by the policies of the folder you started in (warned, or denied under `--strict`).

Train mirror policy:
- if run inside a project with project-installed skills: upgrade project skills
- by default, also upgrades matching globally-installed skills
- disable global mirror: `gym train --no-global`
- compatibility aliases: `gym upgrade ...`

### Lock and pins

Every scope has a lock that records where each installed skill came from, so a clone can get the same bytes:

| Scope | File |
|-------|------|
| project, folder | `<root>/.gym/lock.json` (commit it) |
| global | `<config-dir>/gym/lock.json` |

`gym learn` writes the entry after the skill is installed and the state saved; `gym train` rewrites upgraded entries; `gym unlearn` removes them. The lock is JSON, schema `gym.lock/v1`, with the skills sorted by name, no timestamps, so a diff shows only real changes. The first write of a project lock also writes `.gym/.gitignore` (`installed.json`, `skills/`) if there is none: the lock is committed, the installed tree and state are not. Per skill: `name`, `kind` (`registry`, `git` or `dir`), `source` (credential-free; a local directory inside the scope root is stored relative to it), `requested` (what you typed after `@` or `#`; absent means the entry tracks), `version`, `commit`, `path`, `registry`, `digest`. A directory outside the scope root still installs, but is not written to a project lock (`lock-source-not-portable: <path> is outside <root>; not written to the lock`): the skill stays untracked, `list --check` reports it as `untracked`, and an existing entry of that name is removed. The global lock keeps absolute directories.

```bash
gym learn                 # install every locked skill that is missing, drifted or modified
gym learn --frozen        # verify only: fail with lock-drift, change nothing
gym list --check          # the same report as a table (or --format json)
```

Scope of `list --check` without `--dir` or `-g`: nearest folder scope at or above the current
directory; none there, nearest folder at or above it holding `.gym/lock.json` (a fresh clone:
`installed.json` and `skills/` gitignored). Same walk as the scope chain: stops at `$HOME`, skips
an ancestor `.gym` not owned by you or writable by others. Neither found: `no folder scope at or
above <dir>; use --dir or -g`.

`gym learn` with no arguments needs a lock (`lock-missing: <path> not found`). It installs from the lock's own source at the locked commit, expects the locked digest, and does not read the registry index, so it works offline from a mirror. One failing entry does not stop the others; the exit is 1 at the end. The lock is not rewritten.

Drift, as `list --check` and `learn --frozen` report it:

| Status | Meaning | Fails |
|--------|---------|-------|
| `missing` | in the lock, not installed | yes |
| `digest-drift` | the digest recorded at install differs from the lock's | yes |
| `modified` | installed files no longer hash to the recorded digest | yes |
| `untracked` | installed, not in the lock | no (warning) |

Exit 1 with `lock-drift: N of M locked skills differ from <lock>` on any failing status. `--frozen`, and config `lock.frozen: true`, never write the lock: `learn <name>` of a locked name is allowed, anything else fails `lock-frozen: learn <arg> would modify <lock>`, and `--no-lock` and `--digest` are refused. A project or folder config may turn `lock.frozen` on, never off.

A lock in a cloned repository is repository content. A local source that is absolute, or that leaves the scope root (`..`, a symlink out), is refused before anything is read: `lock-source-escapes: <path>`. A corrupt lock is `lock-invalid: <path>: <why>` and stops `learn` and `train` before they change anything. The digest in the lock is checked like any pin: a source whose content changed is `digest-mismatch`, nothing is written.

When a registry pins a different digest than the lock for the same version, the lock wins and the guard warns (`admission-digest-pin-conflict`); a registry that has only moved on to a newer version is not a conflict.

### CLI config (Viper)

You can provide CLI defaults via Viper:

- `.gym/cli.yaml` in current working directory
- `$XDG_CONFIG_HOME/gym/cli.yaml`

The project file arrives with a clone, so it can only narrow a run: it may set `learn.agents`, `train.no_global: true` and `strict: true`. Every other key (`learn.global`, `learn.path`, `insecure`, `gate-timeout`, `bus`, `max-data-class`, `refresh-judgement`, anything else) is ignored with `cli: ignoring project key "<key>" (user config or flags only)`. To pick an in-tree install path from a project, use `install_paths` in `.gym/config.yaml`. The user file is unrestricted and wins over the project file; command-line flags win over both.
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

### List

```bash
gym list                      # nearest folder scope
gym list --dir org/unit       # that folder scope
gym list -g                   # global scope
gym list --effective          # what an agent in this folder sees
gym list --check              # drift against the lock (exit 1 on missing, digest-drift, modified)
gym list --format json
```

`gym list` shows one scope: the folder scope at `--dir`, else the nearest one at or above the current directory (`-g` for global). With `--effective` it shows the whole chain, the folder's own scope, its ancestors and the global scope; where several scopes hold the same name the nearest wins and the others are listed with `SHADOWED BY` (`shadowed_by` in JSON) naming the winner's folder or `global`. `--check` compares the scope (`--dir`, `-g`, or the nearest folder) with its lock instead; see [Lock and pins](#lock-and-pins). Its JSON form is `{"scope", "lock", "ok", "findings": [{"name", "status", "detail"}]}`; `--check` and `--effective` exclude each other. Columns: name, version, scope, folder, install path. JSON rows add `depth` (0 is the innermost folder; the global scope follows the last folder) and the source URL.

On a fresh clone (lock committed, nothing installed) `gym list` notes `no folder scope`, while
`gym list --check` finds the folder holding the lock and reports each locked skill `missing`.

### Warmup (Progressive Disclosure)

Generate lightweight skill metadata for context-efficient agent startup:

```bash
gym warmup                          # XML to stdout (default)
gym warmup --format json            # JSON array of metadata
gym warmup --format agents-md       # AGENTS.md format
gym warmup -o skills.xml            # Write to file
gym warmup --scope global           # Only global skills
gym warmup --scope project          # Only project skills
gym warmup --dir org/unit           # As seen from that folder
gym warmup --no-inherit             # Nearest folder scope only
```

`gym warmup` is effective by default: the global scope plus every folder scope on the chain, the nearest holder of a name winning. `--no-inherit` keeps only the nearest folder scope. JSON rows carry `folder` (scope root of a project skill) and `depth` (0 is the innermost folder; the global scope follows the last folder).

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

`/skills.json` lists every version of every skill the index holds, each with its `digest` when the index has one, so a client can pin an older version with `name@version`. A client may also read the same file from disk: a repository `url` can be a `file://` URL or a plain path to an index file, and an entry's `source_url` can be an archive (`.tar.gz`, `.tgz`, `.zip`) on your own host.

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

`gym learn` and `gym train` gate installations on policy rules designed to catch risky patterns:

- **Declared commands** (`postinstall`, `shell_hook` in `gym.json`): `sudo`/`doas`/`pkexec`,
  `curl|bash` pipes, `/etc/hosts` edits, suspicious chmod, any postinstall script (deny);
  package installer shells, egress in shell hooks (warn).
- **Script files** in the skill tree (`#!` first line, or `.sh .bash .zsh .fish .ksh .ps1 .py
  .rb .js .ts .pl`; `node_modules/` and `vendor/` included): same command patterns, as
  `*-in-scripts` policies (`no-curl-bash-in-scripts`, `no-sudo-in-scripts`,
  `no-hosts-edit-in-scripts`, `no-suspicious-chmod-in-scripts`,
  `no-package-installer-in-scripts`). Warn only: a script finding never denies on its own;
  `--strict` turns it into a deny.
- **Text files** (SKILL.md included): secrets (AWS keys, GitHub tokens, Slack tokens, JWTs),
  deny (`no-secret-leak`).
- **Sizes**: binaries over 25 MiB (warn).
- Not scanned: SKILL.md prose for command patterns, binaries, symlink targets, `.git/`, what
  a skill does at runtime. Reads capped at 256 KiB per file and 4 MiB per scan (text,
  scripts); content past a cap is not checked.
- `gym guard test <dir>` and the install gate scan the same tree the same way.
- `--strict`: fail (deny) on any violation; allow only on explicit ALLOW verdicts.
- `--insecure`: downgrade DENY verdicts to WARN, allowing risky skills to install.
- `--gate-timeout=30s`: override the default 30-second wait for guard evaluation.

List installed policies: `gym guard list`.

Test a skill against all policies (dry-run): `gym guard test <skill-directory>` or `gym guard test manifest.json` (exit 0 = all allow, exit 1 = deny/warn).

### Network and credentials

`network` decides which hosts gym may contact. One check, by host name, runs before every request, on every redirect, and before git runs:

```yaml
network:
  mode: open          # open (default) | allowlist | offline
  allow_hosts: []     # "host", "host:port", "*.suffix"
```

- `open`: no restriction. `allowlist`: only the listed hosts. `offline`: no host at all; directory sources, archives on disk, the cached index and lock-driven `learn` from local mirrors keep working, and `gym update` fails by design.
- `--offline` sets `mode: offline` for the run. There is no `--allow-host` flag: use `-c 'network.allow_hosts=[host]'`.
- Matching is case-insensitive and by name as written (no IDNA: list the punycode form of an international name). `host` matches that host on any port, `host:port` only that port (https 443, http 80, ssh and scp-style 22 by default), `*.suffix` any host below the suffix but not the suffix itself. Loopback is not implicitly allowed: list `127.0.0.1:4873`.
- A project or folder `config.yaml` can only tighten: the mode may rise (`open < allowlist < offline`) and `allow_hosts` can only intersect with the list above it. User, system and `-c` layers set it freely.
- Covered: registry index fetches (and each redirect), coach manifest fetches, archive URLs, git sources (the host of the effective URL, after your `url.<base>.insteadOf` rewrites, so a mirror counts, not the URL you typed), and the event bus network adapter (`GYM_BUS_URL`). Not run unless the mode is `open`: the update notice and the `rsx` coach judgement (a warning says so).
- A denial is `network-denied: host "github.com" not in network.allow_hosts (mode allowlist)` or `network-denied: offline mode forbids network access to "github.com"`. It is not relaxed by `--insecure`, and during an install it is published as `install.rejected` (reason `network-denied`). `gym update` goes on with the other repositories, prints one `repository "<name>": network-denied: ...` line per denied one, and exits 1 without replacing the cached index.
- The policy is by host name, not by network path or resolved address: a firewall remains the guarantee.

Git runs with `protocol.allow=user`, https and ssh always, `file` for sources you typed (a local repository or `file://` URL, with or without `#ref`; nested and submodule fetches cannot use it), `ext` never, and with prompts off when stdin is not a terminal (a missing credential fails instead of hanging). gym never asks git for submodules.

Registry credentials, in a user, system or `-c` config (a project or folder `auth` is ignored with a warning, otherwise a cloned repository could point a token at its own host):

```yaml
repositories:
  - name: corp
    url: https://registry.corp.example/skills.json
    auth: {type: bearer, token_env: GYM_CORP_TOKEN}    # or token_file: ~/.config/gym/corp.token
  - name: corp2
    url: https://registry2.corp.example/skills.json
    auth: {type: git-credential}                       # `git credential fill`, sent as Basic
```

The token goes only to the exact host (and port) of the repository URL, over https (http only to a loopback host), is dropped on a redirect to another host, port or http, is read on every request and never written to disk. `token_file` must be mode 0600 and owned by you, else `auth: token file <path> is group/world accessible`. An archive URL that a repository lists reuses that repository's credential, for that repository's host only. A git source uses git's own credentials. Tokens, `Authorization` and `Cookie` headers, URL passwords and query tokens are never printed, stored in state or the lock, or published; a URL with a password is refused (`source-credentials-in-url`).

### Isolated deployment

For a machine that must reach only your own hosts, in the user or system config:

```yaml
admission:
  require_pin: remote            # registry, git and archive sources need a digest pin
network:
  mode: allowlist
  allow_hosts: [registry.corp.example, files.corp.example]
```

Publish a registry whose entries carry `digest`, pin the rest with `gym learn --digest` or commit the lock, and use `gym learn` (no arguments) to restore from it. The registry index itself can come from disk: a repository `url` may be a `file://` URL or a plain path.

### Skill admission

After the validator passes, `gym learn` and `gym train` admit each skill:

- **Digest**: sha256 over the skill's sorted file listing (`gym digest <dir>`; `--files` shows the listing). The pin comes, in order, from `--digest` (operator), the lock entry, then the registry entry's `"digest": "sha256:<hex>"`; a differing digest rejects the install (`digest-mismatch`). No pin: warning, digest recorded in installed state. With the first install unpinned, `gym learn` once and commit the lock, or use `learn --digest`.
- **Manifest** (optional `gym.json` at the skill root):

  ```json
  {"schema": "gym.skill/v1", "allowed_tools": ["Read", "Bash(git:*)"], "data_class": "internal"}
  ```

  `data_class` is one of `public < internal < confidential < personal < special_category`. `allowed_tools` must cover every token in SKILL.md `allowed-tools`. Unknown fields, and the operator-only fields `digest` and `approval_state`, are rejected. This checks consistency; it cannot observe what a skill does at runtime.
- **Data class ceiling**: `admission.max_data_class` (default `internal`) or `--max-data-class`. A skill above it is denied.
- **Approval**: `gym guard approve <skill|dir>` records approval for the exact digest; any content change returns the skill to unreviewed. `gym guard revoke <skill|dir>` blocks a skill (all digests) or, with `--digest`, one digest. Records live in `<config-dir>/gym/approvals.json`.
- **No manifest**: `admission.require_manifest` = `warn` (default), `off`, or `reject`.
- **Pin required**: `admission.require_pin` = `none` (default), `remote`, or `all`. With `remote`, registry, git and archive sources need a pin from the lock, the registry or `--digest`; directory sources are exempt. `all` also covers directories. An unpinned source is then denied (`admission-digest-required`, reason `digest-required`). A project or folder config can only raise it. Recommended for an isolated deployment: `remote` with `network.mode: allowlist` (see [Isolated deployment](#isolated-deployment)).

Config (`config.yaml`; a project config can only lower the ceiling, turn approval on, raise the manifest or pin requirement, or turn `lock.frozen` on, relative to what defaults, system and user resolve to):

```yaml
admission:
  max_data_class: confidential
  require_approval: false
  require_manifest: warn
  require_pin: none
lock:
  frozen: false
```

Precedence, lowest to highest: defaults < system < user < project (clamped) < `-c <file>` < `-c key=value` < `--max-data-class`. The command-line layers are operator-trusted, so `-c admission.max_data_class=confidential` may raise the ceiling over a project value. A legacy `config.json` is read for these keys like any other. An invalid value is a config error before any install.

Hard failures (`manifest-invalid`, `digest-mismatch`, `digest-unsupported-entry`, `approval-revoked`) are not relaxed by `--insecure` and do not depend on the event bus. The rest are policies (`gym guard list`): `--insecure` lowers denies to warnings, `--strict` turns warnings into denies. Every rejection exits 1.

Dry-run: `gym guard test <skill-dir>` prints verdict, findings, and the computed facts; `--max-data-class`, `--approval-state unreviewed|approved|revoked`, and `--expect-digest sha256:<hex>` simulate operator state. `gym guard test payload.json` still evaluates a raw payload.

### Opting out of gates

Coaches and skills pass through gates by default. To run outside the safety checks:

- `gym coach hire --insecure …` — proceed even on DENY verdicts.
- `gym learn --insecure …` — proceed even on DENY verdicts.
- `gym train --insecure …` — proceed even on DENY verdicts.

To relax or disable a rule for yourself, create `$XDG_CONFIG_HOME/gym/policies/policies.yaml`:

```yaml
relax: ["no-large-binary"]  # deny -> warn
disable: ["no-postinstall-script"]  # completely disable this rule
```

A user-dir `.yaml` file whose policy name matches an existing one replaces it.

Project policies (`<project-root>/.gym/policies/`) are repository content and are not trusted to weaken checks. They may add new policies. Disabling or relaxing a policy that is not project-defined, and a project file redefining a built-in or user policy, are ignored with one warning per entry; the built-in/user definition stays in force. A project may disable or relax policies it defines itself. A project or folder policy file that cannot be read, parsed, validated or compiled is skipped with one warning naming the file; every other policy still loads and gates. A broken file in your own user policies directory stays a hard error (the guard does not start: denied under `--strict`).

Allowances come only from you. A skill manifest cannot approve its own postinstall script or large binary: `allow_postinstall` and `large_binary` fields in a manifest are ignored. To permit postinstall scripts or large binaries, relax or disable `no-postinstall-script` / `no-large-binary` as above (user policies dir: `$XDG_CONFIG_HOME/gym/policies/`).

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
- Policy additions (project): `<project-root>/.gym/policies/` (YAML files; add-only)
- Daemon bus (SQLite): `$XDG_STATE_HOME/gym/bus.sqlite` (used when `--bus sqlite` or daemon mode)

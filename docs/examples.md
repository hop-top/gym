# gym: Use Cases

Worked examples. Commands are exact: copy them as written, change only names and paths.
Output is trimmed; `…` marks a cut. Guard warnings (`unreviewed`, `unpinned`,
`no manifest`) are left out unless the example is about them.

Needs a validator on `PATH`: see [Install](../README.md#install).

1. [Share a project's skills through the
   repository](#1-share-a-projects-skills-through-the-repository)
2. [One skill, several agent hosts](#2-one-skill-several-agent-hosts)
3. [Pin some skills, track others](#3-pin-some-skills-track-others)
4. [Vet a third-party skill before installing](#4-vet-a-third-party-skill-before-installing)
5. [Company, unit, project: skills per folder](#5-company-unit-project-skills-per-folder)
6. [Run a registry for a team](#6-run-a-registry-for-a-team)
7. [Give an agent its skill list at startup](#7-give-an-agent-its-skill-list-at-startup)

## 1. Share a project's skills through the repository

Every clone and every CI run gets the same bytes.

```bash
cd ~/work/app
gym learn ./skills-src/lint       # skill source kept in the repository
git add .gym/lock.json .gym/.gitignore
```

`.gym/lock.json`:

```json
{
  "schema": "gym.lock/v1",
  "digest_scheme": "gym.digest/v1",
  "scope": "project",
  "skills": [
    {
      "name": "lint",
      "kind": "dir",
      "source": "skills-src/lint",
      "version": "0.0.0",
      "digest": "sha256:2df0dabcc75915b88cb17bfbefb09939c893418f8fdfce9a8358d68b33a7a6aa"
    }
  ]
}
```

`.gym/.gitignore` keeps `installed.json` and `skills/` out of the commit.

On a fresh clone:

```console
$ gym learn
installed lint 0.0.0 -> ~/work/app/.gym/skills/lint
$ gym list --check
lock ok: 1 locked skills match ~/work/app/.gym/lock.json
```

CI gate. Changes nothing, exit 1 on any difference:

```console
$ gym learn --frozen
Error: lock-drift: 1 of 1 locked skills differ from ~/work/app/.gym/lock.json
```

A locally edited skill shows up the same way:

```console
$ gym list --check
STATUS    NAME  DETAIL
modified  lint  ~/work/app/.gym/skills/lint hashes to sha256:45ae…, recorded sha256:2df0…
Error: lock-drift: 1 of 1 locked skills differ from ~/work/app/.gym/lock.json
```

Notes:

- a source outside the project root is not written to the lock
  (`lock-source-not-portable`): keep directory sources in the tree, or use remote ones

## 2. One skill, several agent hosts

A project used from Claude Code and Cursor. `.gym/config.yaml`:

```yaml
install_paths:
  - {name: claude-project, path: .claude/skills, scope: project}
  - {name: cursor-project, path: .cursor/skills, scope: project}
```

```console
$ gym learn ./skills-src/review
installed review 0.0.0 -> ~/work/app/.claude/skills/review
installed review 0.0.0 -> ~/work/app/.cursor/skills/review
$ gym learn --agent 'claude*' ./skills-src/docs
installed docs 0.0.0 -> ~/work/app/.claude/skills/docs
```

For every project on the machine, use the global scope. Without configured global paths
gym installs into each host directory it detects:

```console
$ gym learn -g fmt
installed fmt 0.2.0 -> ~/.claude/skills/fmt
installed fmt 0.2.0 -> ~/.cursor/skills/fmt
```

`gym paths` shows configured and detected paths; `gym paths add --scope global --path
~/.claude/skills` fixes the set.

## 3. Pin some skills, track others

`lint` must stay on a reviewed version; `fmt` may follow the registry.

```console
$ gym learn lint@1.0.0
installed lint 1.0.0 -> ~/work/svc/.gym/skills/lint
$ gym learn fmt
installed fmt 0.1.0 -> ~/work/svc/.gym/skills/fmt
```

The lock records the difference: `lint` carries `"requested": "1.0.0"`, `fmt` does not.
Later, the registry publishes `fmt 0.2.0` and `lint 1.1.0`:

```console
$ gym update
updated registry: 4 skills (0 new, 2 upgrades)
  upgrade available: fmt 0.1.0 -> 0.2.0
  upgrade available: lint 1.0.0 -> 1.1.0
$ gym train
skip lint: pinned to 1.0.0 (gym learn lint@<new> to change)
upgraded fmt (project): 0.1.0 -> 0.2.0
```

Move the pin on purpose:

```console
$ gym learn lint@1.1.0
installed lint 1.1.0 -> ~/work/svc/.gym/skills/lint
$ gym learn lint@9.9.9
Error: pin-unavailable: lint@9.9.9 not in index (available: 1.1.0, 1.0.0)
```

CI check that nothing is waiting:

```console
$ gym train --frozen
Error: lock-frozen: train would modify ~/work/svc/.gym/lock.json (fmt 0.1.0 -> 0.2.0)
```

Git sources pin the same way: `gym learn https://github.com/acme/lint-skill.git#v1.0.0`.

## 4. Vet a third-party skill before installing

A skill directory received from outside. Dry-run first; nothing is installed:

```console
$ gym guard test third-party/deploy
verdict: warn
  admission-approval-required [warn] skill is unreviewed (no operator approval for this digest); …
  admission-digest-unpinned [warn] no digest pin for this skill; computed digest is recorded but …
  admission-manifest-missing [warn] skill has no gym.json manifest; tool and data class checks …
digest: sha256:0737227056c5e15fb5563afe7bcf43e1eae94001b596a19c6d71513c083eb09b (unpinned)
manifest: false
data_class: - (ceiling internal)
approval_state: unreviewed
```

List exactly what the digest covers, then read those files:

```console
$ gym digest --files third-party/deploy
- ec85da0c… SKILL.md
- aeba8d64… install.sh
sha256:0737227056c5e15fb5563afe7bcf43e1eae94001b596a19c6d71513c083eb09b
```

Once reviewed, approve that exact digest and install pinned to it:

```console
$ gym guard approve third-party/deploy
approved deploy sha256:0737…
$ gym learn --digest sha256:0737227056c5e15fb5563afe7bcf43e1eae94001b596a19c6d71513c083eb09b \
    third-party/deploy
installed deploy 0.0.0 -> ~/work/app/.gym/skills/deploy
```

Any later change to the files is refused, and returns the skill to unreviewed:

```console
$ gym learn --digest sha256:0737… third-party/deploy
Error: admission: digest-mismatch: operator pin sha256:0737…, computed sha256:d87e…
```

To make review mandatory instead of advisory, run with `--strict` (warnings deny) or set
`admission.require_approval: true` in user config:

```console
$ gym --strict learn third-party/other
Error: guard: policy deny: admission-approval-required: skill is unreviewed …
```

The checks are static: gym does not observe what a skill does at runtime.

## 5. Company, unit, project: skills per folder

Work organised as a tree. Each level adds skills; lower levels inherit.

```bash
cd ~/work/acme
gym learn --dir . ./skills-src/security                   # whole company
gym learn --dir payments ./payments/skills-src/pci        # one unit
gym learn --dir payments/checkout ./payments/checkout/skills-src/checkout-flows
```

From the project folder, one scope or the whole chain:

```console
$ cd payments/checkout && gym list
NAME            VERSION  SCOPE    FOLDER                         PATH
checkout-flows  0.0.0    project  ~/work/acme/payments/checkout  …/.gym/skills/checkout-flows
$ gym list --effective
NAME            VERSION  SCOPE    FOLDER                         PATH
checkout-flows  0.0.0    project  ~/work/acme/payments/checkout  …/.gym/skills/checkout-flows
pci             0.0.0    project  ~/work/acme/payments           …/.gym/skills/pci
security        0.0.0    project  ~/work/acme                    …/.gym/skills/security
```

Nothing is copied downwards: inheritance is resolved when gym reads. Agent hosts load
only the directories they are configured for, so write the effective set out for them:

```console
$ gym warmup --dir ~/work/acme/payments --format agents-md -o ~/work/acme/payments/AGENTS.md
wrote agents-md to ~/work/acme/payments/AGENTS.md
```

Upgrade the whole tree in one run; each scope is planned on its own:

```console
$ gym train --dir ~/work/acme -r
…
scope ~/work/acme/payments
…
scope ~/work/acme/payments/checkout
…
```

A folder's `.gym/config.yaml` can tighten what an outer folder set (admission, network),
never loosen it. Details: [Folder scopes](user.md#folder-scopes).

## 6. Run a registry for a team

A registry is one `skills.json` plus sources it points at (git repositories or archives).

```json
{
  "repository": {"name": "acme-skills", "curated": true},
  "skills": [
    {"name": "lint", "version": "1.0.0", "description": "Lint the repo.",
     "source_url": "https://files.corp.example/skills/lint-1.0.0.tar.gz"},
    {"name": "lint", "version": "1.1.0", "description": "Lint the repo.",
     "source_url": "https://files.corp.example/skills/lint-1.1.0.tar.gz"},
    {"name": "fmt", "version": "0.2.0", "description": "Format code.",
     "source_url": "https://files.corp.example/skills/fmt-0.2.0.tar.gz",
     "digest": "sha256:916e18c0d44ede0ce81c18879c4d46f831c80874a6a9d6253d7e1bc3a5e74d83"}
  ]
}
```

Build archives with `COPYFILE_DISABLE=1 tar czf lint-1.0.0.tar.gz lint` (macOS `tar`
otherwise adds `._*` entries that change the digest). `gym digest lint-1.0.0.tar.gz`
prints the value for `digest`.

Each teammate hires it once:

```console
$ gym coach hire --global https://files.corp.example/skills/skills.json
hired coach "acme-skills" -> https://files.corp.example/skills/skills.json
updated registry: 3 skills (2 new, 0 upgrades)
  new: fmt 0.2.0 (acme-skills from https://files.corp.example/skills/skills.json)
  new: lint 1.1.0 (acme-skills from https://files.corp.example/skills/skills.json)
$ gym learn lint
```

An index file on disk can be listed in user config instead:

```yaml
repositories:
  - name: acme
    url: /srv/skills/skills.json
    curated: true
```

Re-publish the merged index of one machine to others:

```console
$ gym serve --daemon --port 4873
started daemon pid 80856
$ curl -s http://127.0.0.1:4873/health
ok
$ gym serve --stop --port 4873
stopped (pid 80856)
```

Lock a machine to your own hosts, and require a digest pin for anything remote:

```yaml
admission:
  require_pin: remote
network:
  mode: allowlist
  allow_hosts: [files.corp.example]
```

```console
$ gym learn fmt@0.2.0          # registry entry carries a digest
installed fmt 0.2.0 -> ~/work/svc/.gym/skills/fmt
$ gym learn lint@1.1.0         # no digest in the registry, none in the lock
Error: guard: policy deny: … admission-digest-required: admission.require_pin demands a digest …
$ gym learn https://github.com/acme/lint-skill.git
Error: network-denied: host "github.com" not in network.allow_hosts (mode allowlist)
```

Details: [Isolated deployment](user.md#isolated-deployment),
[Network and credentials](user.md#network-and-credentials).

## 7. Give an agent its skill list at startup

Agents load name + description only, then open a skill when they need it.

```console
$ gym warmup
<available_skills>
  <skill>
    <name>hello</name>
    <description>Say hello. Use when greeting.</description>
    <location>project</location>
  </skill>
</available_skills>
$ gym warmup --format agents-md
# Available Skills

Skills available for use by agents. Use `/skill <name>` to load a skill on-demand.

## Project Skills

- **security**: Company security rules.
- **pci**: PCI rules for payments.
- **checkout-flows**: Checkout flows.
```

Regenerate after every `learn`, `train` or `unlearn`:

```bash
gym warmup --format agents-md -o AGENTS.md
gym warmup --format json -o skills.json     # for custom tooling
gym warmup --scope project --no-inherit     # nearest scope only
```

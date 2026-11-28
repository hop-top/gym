---
name: gym-cli
description: Install, pin, vet, upgrade and remove agentskills.io skills with the gym CLI. Use when adding a skill to a project or agent host, restoring skills from a lock, checking a skill before installing it, or emitting the skill list an agent loads at startup.
---

# gym

gym is a package manager for agent skills. `gym --help-all` lists every command and flag;
this file holds what help does not say.

## Pick the scope first

- Default is the project scope. `learn` uses the current directory and creates a scope
  there (a note on stderr says so); `train`, `list`, `warmup` use the nearest scope at or
  above it.
- `-g` is the global scope: every detected host directory (`~/.claude/skills`,
  `~/.cursor/skills`, …) or the configured global paths. `--dir FOLDER` picks another
  folder. The two exclude each other.
- With `install_paths` configured for the scope, `learn` writes to every one of them, not
  to `.gym/skills`. Narrow with `--agent 'claude*'` (`!` prefix excludes) or `--path NAME`.
- Unsure where an install lands: `gym paths`.

## Install

Sources: `name`, `name@version`, `git-url#ref`, archive (`.tar.gz`, `.tgz`, `.zip`),
directory. No arguments: restore what the lock lists.

- `WARN guard: …` lines are verdicts that passed. `Error: guard: policy deny` means nothing
  was installed (exit 1).
- `validation failed: exec: "skills-ref" …`: the validator is missing, the skill is not at
  fault. Report it; the operator installs `skills-ref` or sets `validator.command` in user
  config.
- `--insecure`, `gym guard approve` and policy changes are the operator's decisions. Never
  add them to get past a deny.
- `network-denied: …` is policy, not an outage. Do not retry.

## Lock

- Commit `.gym/lock.json`. `.gym/.gitignore` keeps `installed.json` and `skills/` out.
- `gym learn --frozen` and `gym list --check` verify and change nothing (`lock-drift`,
  exit 1).
- A pinned entry (`name@version`, `url#ref`) is skipped by `train`:
  `skip lint: pinned to 1.0.0`. Move the pin with `gym learn lint@<new>`.
- A source outside the scope root (a directory, a local archive) is not written to a
  project lock (`lock-source-not-portable`). The skill is untracked, and a `name@version`
  pin taken from such a source is not remembered: `train` upgrades it.

## Before installing something unknown

```bash
gym guard test <dir>       # verdict, findings, computed facts; installs nothing
gym digest <dir|archive>   # the content digest a pin refers to
gym learn --digest sha256:<hex> <source>   # refuse any other bytes (digest-mismatch)
```

Checks are static. They do not observe what a skill does at runtime.

## Upgrade

- `gym update` first: `train` reads the local index. `update` prints
  `upgrade available: fmt 0.1.0 -> 0.2.0`.
- `gym train` takes names and patterns. `--frozen` changes nothing and exits 1 if an
  upgrade exists. `--dir FOLDER -r` walks every scope below a folder.
- Directory and git installs are not in the registry:
  `skip <name>: not found in current registry index`.

## Tell an agent what is installed

`gym warmup` prints name + description of each skill (`xml` by default, `--format json`,
`--format agents-md`; `-o FILE` writes it). It covers the whole chain, global scope
included; `--no-inherit` keeps the nearest scope only. For rows with paths and versions:
`gym list --effective --format json`.

## Gotchas

- `coach hire` scores the source with `rsx`. Without it: `rsx-unavailable` warning, hire
  proceeds.
- `unlearn` with a pattern matching several skills needs `--yes` off a terminal
  (`confirmation-required`).
- A project `.gym/config.yaml` cannot set `validator`, nor loosen `admission` or `network`:
  such keys are ignored with a warning. Operator settings go in user config or `-c`.

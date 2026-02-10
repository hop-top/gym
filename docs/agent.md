# gym: Agent Notes

Goal: manage agentskills.io skills via CLI.

Preflight:
- `gym version`
- `gym update` (refresh index)

Coaches (registry sources):
- add (project default): `gym coach hire <manifest-url|owner/repo>`
- add (global): `gym coach hire --global <manifest-url|owner/repo>`
- list: `gym coach list`
- rm (project default): `gym coach dismiss <name|url>`
- rm (global): `gym coach dismiss --global <name|url>`
- `--name` = local label for this coach entry; else use `repository.name`; else derive from URL

Learn:
- by name: `gym learn <skill>`
- by URL/dir: `gym learn <git-url|file-url|dir>`
- default scope: project install at `CWD/.gym/skills`
- force global: `gym learn --global …` or `gym learn -g …`
- if multiple install paths match scope, installs into all of them
- filter targets: `gym learn --agent claude --agent "!codex" …`
- override single path: `gym learn --path <name-or-path> …`
- compatibility alias: `gym install ...`
- gate: validator runs (default `skills-ref test {path}`); fail => no install

Train:
- all: `gym train`
- select: `gym train a b`
- patterns: `gym train "auth-*"`
- in project with project skills: upgrades project; mirrors global by default; disable: `gym train --no-global`
- compatibility alias: `gym upgrade ...`

Paths:
- list: `gym paths`
- add global: `gym paths add --scope global --path ~/.claude/skills`
- add project: `gym paths add --scope project --path .claude/skills`

Warmup (Progressive Disclosure):
- metadata: `gym warmup --format xml|json|agents-md`
- scoped: `gym warmup --scope global|project|all`
- to file: `gym warmup -o FILE`
- implements agentskills.io progressive disclosure: load metadata (~100 tokens) at startup, fetch full content on-demand

Serve:
- `gym serve --host 127.0.0.1 --port 4873`
- endpoints: `/skills.json` `/health` `/_index.json`

Files:
- user cfg: `$XDG_CONFIG_HOME/gym/config.json` (fallback `~/.config/gym/config.json`)
- project cfg: `<root>/.gym/config.json`
- global state: `$XDG_STATE_HOME/gym/installed-global.json`
- project state: `<root>/.gym/installed.json`
- index cache: `$XDG_DATA_HOME/gym/registry-index.json`

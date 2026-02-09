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

Install:
- by name: `gym install <skill>`
- by URL/dir: `gym install <git-url|file-url|dir>`
- force scope: `gym install --project …` or `gym install --global …`
- gate: validator runs (default `skills-ref test {path}`); fail => no install

Upgrade:
- all: `gym upgrade`
- select: `gym upgrade a b`
- patterns: `gym upgrade "auth-*"`
- in project with project skills: upgrades project; mirrors global by default; disable: `gym upgrade --no-global`

Paths:
- list: `gym paths`
- add global: `gym paths add --scope global --path ~/.claude/skills`
- add project: `gym paths add --scope project --path .claude/skills`

Serve:
- `gym serve --host 127.0.0.1 --port 4873`
- endpoints: `/skills.json` `/health` `/_index.json`

Files:
- user cfg: `$XDG_CONFIG_HOME/gym/config.json` (fallback `~/.config/gym/config.json`)
- project cfg: `<root>/.gym/config.json`
- global state: `$XDG_STATE_HOME/gym/installed-global.json`
- project state: `<root>/.gym/installed.json`
- index cache: `$XDG_DATA_HOME/gym/registry-index.json`


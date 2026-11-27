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
- gate: validator runs (default `skills-ref validate {path}`); fail => no install

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

Coach judging (rsx-backed):
- `gym coach hire` blocks on rsx analysis + policy test; default timeout 30s.
- `gym coach hire --strict` fails-closed on timeout (else warns and proceeds).
- `gym coach hire --refresh-judgement` bypasses cache; re-runs rsx.
- `gym coach list` shows `score=N/100 (verdict)` per coach (cached).
- `--insecure` lowers DENY verdicts to WARN; allows risky coaches to proceed.
- `--gate-timeout=DURATION` overrides 30s default for gates.

Policy guards (CEL):
- `gym learn` / `gym train` gates on installed skills matching policy rules (sudo, curl|bash, secrets, egress, large binaries, postinstall scripts, etc.).
- Fails DENY unless `--insecure` set (deny→warn) or `--strict` (warn→deny + timeout fail-closed).
- `gym guard list` shows effective policies (baked-in + user overrides + project additions).
- `gym guard test <skill-or-manifest>` dry-runs policies; exit 0=allow, 1=deny/warn.

Cheat sheets (toolspec + tip integration):
- `gym cheat <topic>` renders markdown hints from gym's command tree + installed skills.
- `gym cheat --tip-export [--path PATH]` writes adapter file for tip integration (opt-in via GYM_TIP_BUS_BRIDGE=1).
- `gym cheat --reindex` rebuilds registry from installed skills.

Event bus (kit/runtime/bus):
- `--bus memory|sqlite|network` selects adapter (default: env-driven, else memory).
- memory: local CLI (default); sqlite: daemon with SQLite bus at `$XDG_STATE_HOME/gym/bus.sqlite`; network: multi-host via GYM_BUS_URL env.
- All cross-tool events (rsx judgement, policy guard, tip hints, warmup metadata) flow through the bus.

Serve:
- `gym serve --host 127.0.0.1 --port 4873`
- endpoints: `/skills.json` `/health` `/_index.json`

Files:
- system cfg: `/etc/gym/config.yaml`
- user cfg: `config.yaml` in `$XDG_CONFIG_HOME/gym` (else OS default: Linux `~/.config/gym`, macOS `~/Library/Application Support/gym`)
- project cfg: nearest `.gym/config.yaml` at or above cwd
- layering: defaults < system < user < project < `-c <file>` < `-c key=value`; lists replaced, not appended
- `-c key=value` / `-c <file>` repeatable; shown in `gym --help-all`
- global state: `$XDG_STATE_HOME/gym/installed-global.json`
- project state: `<root>/.gym/installed.json`
- index cache: `$XDG_DATA_HOME/gym/registry-index.json`
- judgement cache: `$XDG_CACHE_HOME/gym/judgements/<sha256>.json`
- bus state (sqlite): `$XDG_STATE_HOME/gym/bus.sqlite`

Event topics (gym.* outbound):
- `gym.coach.repository.hire_requested` — coach hire initiated (sync gate, awaits judgement).
- `gym.coach.repository.hired` — coach added to config.
- `gym.coach.repository.dismissed` — coach removed from config.
- `gym.coach.judgement.completed` — rsx verdict (score, verdict, findings).
- `gym.skill.install.requested` — install initiated (sync gate, awaits guard verdict).
- `gym.skill.install.completed` — skill installed successfully.
- `gym.skill.install.rejected` — install rejected by admission (hard check or admission-* policy deny); carries `reason` and the `request_id` of the attempt.
- `gym.skill.upgrade.planned` — upgrade plan available for veto.
- `gym.skill.upgrade.applied` — skill upgraded successfully.
- `gym.skill.upgrade.failed` — upgrade errored.
- `gym.warmup.metadata.emitted` — skill metadata snapshot; tip can index it.
- `gym.toolspec.cli.published` — gym's CLI as a ToolSpec (self-spec for cheat sheets).
- `gym.cheat.hint.emitted` — hint from tip-bridge subscriber (optional, GYM_TIP_BUS_BRIDGE=1).

Event topics (inbound):
- `gym.coach.judgement.completed` (from internal/judge, correlated by request_id).
- `policy.guard.install.evaluated` (from internal/guard, correlated by request_id).
- `tip.shell.command.observed` (optional, when GYM_TIP_BUS_BRIDGE=1).
- `toolspec.cli.resolved` (remote spec sources).

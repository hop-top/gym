# ADR-0003: Folder Scopes, Lockfile and Pins, Uninstall, Private Sources

**Status**: Accepted  
**Date**: 2026-11-27  
**Deciders**: Idea Crafters team  

## Context

Goal: gym installs the skills agents load at each level of an organisation whose work is a tree of folders (company, unit, project). An agent working in a folder loads the skills installed for that folder and for its ancestors. gym stays standalone: nothing below requires another tool.

Four gaps: (A) no scope bound to one folder; (B) version is a string, default `0.0.0`, no lockfile; (C) no uninstall; (D) installs are a shallow `git clone` or a local directory, nothing for a deployment that cannot reach public hosts.

### What exists today

1. A folder scope already exists by accident: `gym learn` (project scope) sets `projectRoot = CWD` (`internal/cli/app.go`, `runLearnCtx`), installs into `<cwd>/.gym/skills`, writes `<cwd>/.gym/installed.json`. That file makes `<cwd>` a root (`project.DetectRoot`: `.gym/config.yaml|config.json|installed.json`). Learning in `org/unit/proj/sub` and in `org/unit/proj` gives two independent scopes.
2. What is missing: no explicit targeting (cd is the only way); no inheritance: `train` and `warmup` use `DetectRoot` (nearest root only), so `warmup` in `sub` omits `proj`'s skills (shadowing); no `gym list` command exists (`coach list`, `guard list`, `paths` only); `train` upgrades one scope; `learn` ignores `DetectRoot` while `train`/`warmup` use it.
3. Config is nearest-file-only: kit loads one project file. An org-level `.gym/config.yaml` (`admission.max_data_class: public`) is silently dropped as soon as a deeper folder has its own `config.yaml`. Relative project `install_paths` resolve against CWD, not the config file's folder (`normalizePaths`).
4. Default project install path is `.gym/skills`, which no agent host loads. `ide.standardProjectPaths` lists `.claude/skills`, `.cursor/skills`, ... only as `paths add` candidates. Whether a host loads ancestor `.claude/skills` is host behaviour and is not relied on.
5. Registry: `Refresh` keeps one entry per name (`bestByName`), so older versions are unreachable. `gym serve` drops `digest` from `/skills.json` (`internal/serve/serve.go` `skill` struct has no field; `/_index.json` keeps it).
6. `--offline` only skips the upgrade-notifier (`cobra.go`); `gym update` and `git clone` still hit the network (`update` fetches with `--offline`). `file://` registry index: `unsupported protocol scheme "file"`. Unauthenticated `http.Client` only; no headers.
7. Egress inventory: `git clone` (`install.go`), registry `HTTPFetcher`, coach manifest fetch (`cli/coach.go`), upgrade notifier (kit, GitHub), `judge` shell-out to `rsx`, validator command.
8. Install copies `.git` into the skills dir for dir and clone sources; digest skips `.git`, so unhashed bytes land in the agent-readable tree. Clone leaves the typed URL in `.git/config`; state records `source_url` raw.
9. Local sources: bare dir and `file://` work (no copy, hashed in place). `git clone --depth 1 <sha>` cannot fetch a commit; `git init` + `fetch --depth 1 origin <sha>` can (local protocol). `git ls-remote --get-url` applies `url.<base>.insteadOf` rewrites.

### Hardening first

Project files (`.gym/config.yaml`, `.gym/installed.json`) are repository content. They were hardened before this work (section 0), because every feature below widens what they can reach.

ADR-0002 constraints kept: expected digests come from lockfile pin first, then registry (`ExpectedDigest`); digest `sha256:<hex>`, `gym.digest/v1`; lockfile vs registry disagreement: lockfile wins, warn `digest-pin-conflict`; test case 11 closes here; exit 1 for every rejection; project config/policies only tighten; admission stage hard-fails in Go.

## Decision

| # | Topic | Decision |
|---|-------|----------|
| 0 | Hardening (prerequisite) | Project-layer key allowlist (no `validator`); state `install_path` containment; install copy skips `.git`; URL redaction |
| A1 | Scope model | Scope values stay `global` and `project`. A folder scope is a `project` scope with an explicit root (`--dir`). Default root stays CWD, with a note when a nearer ancestor root exists |
| A2 | Inheritance | Resolved by gym at read time (`list --effective`, `warmup`), never by copying. Nearest wins on name clash. Hosts load their own dirs |
| A3 | Config layering | Folder config chain-merged outermost to innermost; security keys fold with the existing tighten-only rules |
| A4 | State, train, list | Per-folder `installed.json`; `train --dir`, `--recursive`; new `gym list` |
| B1 | Version and pin syntax | Registry `name@version`; git `url#ref`; dir/archive pinned by digest only |
| B2 | Lockfile | `<root>/.gym/lock.json` (project), `<ConfigHome>/lock.json` (global); JSON, schema `gym.lock/v1`, sorted, no timestamps; project lock meant to be committed |
| B3 | Behaviour | `learn` (no args) installs from lock; `--frozen`; `train` skips pinned; `list --check` reports drift |
| C | Uninstall | `gym unlearn` (aliases `uninstall`, `remove`, `rm`); dir, then state, then lock; idempotent; containment-checked |
| D1 | Source kinds | registry, git, dir (mirror), archive; digest required by config `admission.require_pin` |
| D2 | Network policy | `network.mode: open|allowlist|offline`, `allow_hosts`; `--offline` becomes mode offline; tighten-only |
| D3 | Credentials | Bearer token by env var or 0600 file, or `git credential`; user/system layers only; never persisted or logged |

## 0. Prerequisite hardening

Ships first, separate PR (`fix`), because every feature below widens what repository content can reach.

1. **Project-layer key allowlist.** Keys honoured from a project/folder `config.yaml`: `install_paths` (contained, see A3), `repositories`, `curation`, `admission` (tighten-only), `network` (tighten-only), `lock.frozen` (on only), `upgrade`. Everything else (`validator`) is ignored with warning `config: ignoring project key "validator" (user/system/-c only)`. Why not a deny-list: new keys default safe. Implementation: load the project layer alone (as `loadAdmission` already does) and copy allowed keys.
2. **State containment.** A record's `install_path` is trusted only if all hold: clean absolute path; `Base == name`; parent is a resolved install path of that scope (project/folder: inside the scope root after `EvalSymlinks`; global: any configured/standard global path, the state file is operator-owned); `Lstat` is a directory, not a symlink. Else error `install-path-untrusted: <path>`; applies to `train` (`PathHint` from state) and `unlearn`. Not relaxable.
3. **No `.git` in the installed copy.** `fsutil.CopyDir` takes a skip predicate; install skips `.git` so installed bytes equal hashed bytes.
4. **Redaction.** `install.RedactURL` strips userinfo password and query tokens; applied to `source_url` in state, lock, events, errors, and `git` stderr echoed by `stageSource`. URLs carrying a password are rejected: `source-credentials-in-url: use a git credential helper or ssh key`. Username-only (`git@host`, `ssh://git@host`) is allowed.

## 1. Folder scope (A)

### 1.1 Model and addressing

A scope is `(kind, root)`: `global` has no root; `project` has a root folder holding `.gym/`. "Folder scope" is the doc term for a `project` scope whose root is chosen explicitly or sits below another root. No new scope value: records, config `install_paths[].scope`, events, `--scope all|global|project` on `warmup`, and CEL (`payload.scope`) keep their enum. Alternative (third value `folder`): rejected, churn in every enum for no semantic gain; "project" was always "a folder with `.gym`".

| Flag | Commands | Meaning |
|------|----------|---------|
| `--dir <path>` | `learn`, `unlearn`, `train`, `list`, `warmup`, `lock`-aware paths | Scope root. Must exist and be a directory. `.gym/` created by `learn` |
| `-g/--global` | `learn`, `unlearn`, `list` | Global scope (existing on `learn`) |
| none | all | Default root: `learn`/`unlearn`: CWD (unchanged). `train`/`list`/`warmup`: nearest existing root at or above CWD (`DetectRoot`), as today |

`learn` default stays CWD so a subfolder learn keeps creating a folder scope. Footgun fix: when CWD has no root but an ancestor root exists, `learn` prints to stderr `note: installing into new folder scope <cwd>; nearest existing scope is <root> (--dir <root> to use it)`. Alternative (default to nearest root): rejected, silently changes where `learn` writes.

`--dir` plus `-g` is an error: `--dir and --global are mutually exclusive`.

### 1.2 Inheritance and who resolves it

`scope.Chain(dir)` (new `internal/scope`): roots from `dir` up, innermost first, then global. Stops at `$HOME` (inclusive) or the filesystem root. An ancestor root is read only if its `.gym` directory is owned by the current user and not group/world-writable (git `safe.directory` analogue); else skipped with warning `scope: ignoring <dir>/.gym (not owned by you or writable by others)`. The starting folder (`--dir`, or the current directory) is the operator's own choice and is not tested. On platforms without POSIX owner and mode bits the test passes every folder.

Resolution points:

| Where | Behaviour |
|-------|-----------|
| `gym list [--dir d] [--effective]` | Without `--effective`: this scope only. With: whole chain, nearest wins; shadowed entries marked `shadowed_by` (the winner's root, or `global`). Output is a table, or JSON with `--format json` |
| `gym warmup` | Default effective (global + chain), nearest wins on a name clash. `--no-inherit` restores nearest-only. JSON adds `folder` (root path, project skills) and `depth` (0 = innermost folder; the global scope follows the last folder). Scope string stays `project`/`global` |
| `gym learn`, `unlearn`, `train` | One scope at a time, never inherited |
| Agent host | Loads whatever its own skill dirs give. gym does not copy ancestor skills into descendants |

Why read-time, not materialised: copying ancestor skills into every descendant duplicates trees, makes digests and lock entries ambiguous, and breaks `train`. Hosts that do not search ancestors get the effective set via `gym warmup --dir <folder> --format agents-md -o <folder>/AGENTS.md`. Materialising (`gym learn --propagate`, symlink farms) is a non-goal (open question 2).

Host mapping: unchanged. A folder installs into its project-scope `install_paths` (default `.gym/skills`; add `claude-project` = `.claude/skills` etc. via `gym paths add --scope project`). `--agent` and `--path` behave as today. Relative project paths now resolve against the scope root, not CWD (fix of finding 3; in a folder installed from `sub` against an org config this keeps the install inside the intended folder).

### 1.3 Config and policy per folder

Chain-merge replaces nearest-file-only: load each ancestor `.gym/config.yaml|json` (same ownership test as 1.2), fold outermost to innermost.

| Key | Fold rule | Source of the rule |
|-----|-----------|--------------------|
| `admission.max_data_class` | min | `ClampMaxDataClass` |
| `admission.require_manifest` | max | `ClampManifestMode` |
| `admission.require_approval`, `admission.require_pin` | or / max | `ClampRequireApproval`, new `ClampRequirePin` |
| `network.mode` | max rank | new (D2) |
| `network.allow_hosts` | intersection once a layer sets it | new (D2) |
| `lock.frozen` | or | new (B3) |
| `install_paths` (scope `project`) | innermost layer that sets it replaces | existing list rule; each entry must stay inside its folder (below) |
| `repositories`, `curation`, `upgrade` | innermost wins | existing |
| anything else | ignored (section 0.1) | |

Containment: a folder-layer `install_paths` path must be relative, contain no `..` escaping, and resolve inside that folder's root; else `config: install path <p> escapes project <root>` and the entry is ignored. A kept path resolves against the scope root being operated on, and must stay inside it too, so an inherited `.claude/skills` installs into each folder's own tree. `~` and absolute paths remain legal in user/system/`-c` layers. Alternative: allow absolute project paths guarded only by `path-allowlist`: rejected, that policy lists sensitive system dirs, not "inside the repo".

Policies: `<folder>/.gym/policies` add-only (existing loader rule) across the chain; a name defined by several folders keeps the outermost, inner ones ignored with the existing warning. A folder inside a cloned repository is repository content: it may tighten, never relax, never run code.

Implementation: `internal/config/chain.go` generalises `loadAdmission` (base without project, then per-folder layer fold, then `-c` and flags). Kit loader stays for the base.

### 1.4 State and train

State: `<root>/.gym/installed.json`, same schema (no new fields). `train --dir <d>` plans for that scope plus the existing global mirror. `train --recursive` (`-r`) additionally walks below the root for `.gym/installed.json` (skips `.git`, `node_modules` and `.gym`, does not follow symlinks; depth limit 8, `--depth n`), plans each scope separately, runs them in path order; one scope failing does not stop the rest, exit 1 at the end. `upgrade.Plan` unchanged per scope; `Plan` gets a `Root` on `PlanItem`.

### 1.5 Tests (A)

| # | Case | Layer | Expect |
|---|------|-------|--------|
| A1 | `learn --dir x` from elsewhere | integration | records under `x/.gym`; CWD untouched |
| A2 | `--dir` missing / file; `--dir` with `-g` | unit | `--dir <p>: not a directory`; mutual-exclusion error |
| A3 | CWD sub of existing root, `learn` | integration | installs into CWD, note on stderr |
| A4 | Chain of 3 roots, same name at 2 | unit | nearest wins, other `shadowed_by` |
| A5 | Ancestor group-writable or foreign-owned | unit | skipped, warning |
| A6 | Chain stops at `$HOME` | unit | root above HOME ignored |
| A7 | Org ceiling `public`, inner folder config silent | integration | `public` still applies (probe regression) |
| A8 | Inner folder raises ceiling / lowers `require_manifest` / switches `require_approval` off (`network.mode` is covered by D9) | unit | clamped, outer value kept |
| A9 | Folder `install_paths` `../x`, absolute | unit | ignored with warning |
| A10 | Inner folder redefines outer policy name | unit | ignored, warning |
| A11 | `warmup` effective vs `--no-inherit` | integration | union vs nearest |
| A12 | `train -r` with one failing scope | integration | others upgraded, exit 1 |

Re-break tests: A7, A8, A9, A5 (remove the clamp / containment / ownership check, confirm red).

## 2. Version pinning and lockfile (B)

### 2.1 What a version is

| Source kind | Version | Pin syntax | Resolved |
|-------------|---------|------------|----------|
| registry entry | entry `version` | `gym learn lint@1.2.0` | version + registry `source_url` |
| git | none intrinsic; tag/branch/commit is the ref | `gym learn https://host/o/r.git#v1.2.0` (`#<sha>` for a commit) | commit from `git rev-parse HEAD` |
| dir / mirror | frontmatter `metadata.version` if set, else `0.0.0` | none (digest only) | digest |
| archive | as dir, after extraction | none (digest only) | digest |

`@` is for registry names only (URLs contain `@`: `git@host`). `#ref` follows npm/pip fragment practice and is not valid in a clone URL otherwise. Alternative `--ref` flag: rejected, one way to say it (fragment). Ranges (`^1.2`) are a non-goal. `name@v` matches after `v`-trim with `compareVersions == 0`; miss: `pin-unavailable: lint@1.2.0 not in index (available: 1.3.0)`.

Registry needs old versions: `Refresh` keeps every `(name, version)`; `Index.Find(name)` returns the best (current behaviour, `preferSkill`), new `Index.FindVersion`. `gym serve` serves all versions and now includes `digest` (fix of finding 5). `UpgradeDiff` compares best-to-best as before.

Git staging (`internal/install/source.go`, new): branch/tag: `git clone --depth 1 --branch <ref>`; 40/64-hex: `git init`, `remote add`, `fetch --depth 1 origin <sha>`, `checkout --detach FETCH_HEAD`. Unresolvable: `ref-not-found: <ref> at <redacted url>`. Tags move, so the commit and digest in the lock are the anchor, not the tag. A ref may not start with `-` or hold characters outside `A-Za-z0-9._/+-` or `..` (`invalid ref`), and every URL follows `--`. A `#ref` applies to a URL, a `file://` URL or an existing directory; the checkout's `git rev-parse HEAD` is the lock's `commit`. A directory or `file://` directory without a ref is a `dir` source, as before (copied in place, `.git` skipped), not a clone. `archive` is not produced yet (next work).

### 2.2 Lockfile

| Item | Decision |
|------|----------|
| Name, location | Project/folder: `<root>/.gym/lock.json`. Global: `<ConfigHome>/lock.json` (operator intent, dotfile-friendly; `installed-global.json` stays in StateHome) |
| Format | JSON, stdlib; like `installed.json`, `approvals.json`, registry index. YAML rejected (comment round-trip not needed); TOML rejected (only an indirect dep) |
| Committed? | Project/folder lock: yes. `installed.json` (absolute paths, timestamps) and `skills/` are not; gym writes `.gym/.gitignore` (`installed.json`, `skills/`) once if absent |
| Determinism | Entries sorted bytewise by `name`; fixed struct field order; 2-space indent; trailing newline; no timestamps; atomic write (temp + rename, new `fsutil.WriteFileAtomic`) |
| Strictness | Closed object (`DisallowUnknownFields`); `schema` must be `gym.lock/v1` |

```json
{
  "schema": "gym.lock/v1",
  "digest_scheme": "gym.digest/v1",
  "scope": "project",
  "skills": [
    {
      "name": "lint",
      "kind": "registry",
      "source": "https://git.corp.example/skills/lint.git",
      "requested": "1.2.0",
      "version": "1.2.0",
      "commit": "9f2c…",
      "path": "skills/lint",
      "registry": "https://registry.corp.example/skills.json",
      "digest": "sha256:…"
    }
  ]
}
```

Fields: `name`, `kind` (`registry|git|dir|archive`), `source` (credential-free locator; dir sources inside the lock's folder are stored relative to it; outside it they are not locked, finding `lock-source-not-portable`), `requested` (what the user typed after `@`/`#`; absent = tracking), `version`, `commit` (git/registry-over-git), `path` (skill subdir), `registry` (origin index URL, registry kind), `digest`. `scope` is per file and must match the file's location. One entry per name per file (multi-path installs share one digest). A tampered entry fails `digest` anyway; lock content is never trusted as code or path (below). A lock is invalid (`lock-invalid: <path>: <why>`) for an unknown field or trailing data, another `schema` or `digest_scheme`, a `scope` that is not the file's, a duplicate or path-like `name`, an unknown `kind`, an empty `source`, a malformed `digest` or `commit`, or a `path` that is absolute or holds `..`. The base for a relative local `source` is the scope root (project) or the config directory (global). The global lock is the operator's own: it may hold absolute sources.

Writers: `learn` (add/replace, unless `--no-lock`), `train` (replace upgraded entries that the lock already lists; a skill it does not list stays unlisted), `unlearn` (remove, unless `--keep-lock`). Written after install dirs and state succeed, so a crash leaves drift (detectable), never a lock entry for an uninstalled-and-unverified tree. A corrupt lock stops `learn` and `train` before they change anything. `.gym/.gitignore` is written with the first project lock write.

Lock `source` of a hostile folder (cloned repo): absolute local sources (a path or a `file://` URL) and relative ones that leave the folder, symlinks resolved, are refused before anything is read: `lock-source-escapes: <p>`. Writing is the other half: a project lock never gets an entry it would refuse to read. A `dir` install whose source is outside the scope root (symlinks resolved) installs as usual but writes no entry, with stderr `lock-source-not-portable: <path> is outside <root>; not written to the lock`; the skill is untracked (`list --check` warns). An existing entry of that name is removed, since it no longer describes what is installed; under `--frozen` or `lock.frozen` the removal is a modification (`lock-frozen`) and an install that touches no entry is allowed. The global lock keeps absolute dir sources. Sources must pass the network policy and admission like any other.

### 2.3 Behaviour

| Command | Behaviour |
|---------|-----------|
| `gym learn lint` | If the lock has `lint`: install the locked source at the locked commit, expect the locked digest; print `using locked 1.2.0 (gym train to upgrade)`. Else resolve as today, lock the result |
| `gym learn lint@1.3.0`, `url#ref` | New request; replaces the lock entry on success |
| `gym learn` (no args) | Install every lock entry of the scope that is missing, digest-drifted or modified; consults the lock's `source`, not the registry index (works offline with a mirror; the index is never read). One failing entry does not stop the rest, exit 1 at the end; the lock is not rewritten. Requires a lock: `lock-missing: <path> not found` |
| `gym learn --digest sha256:<hex> <tok>` | Operator pin without a lock (`digest_source: operator`); written to the lock on success. Exactly one `<tok>`; a lock entry is not consulted |
| `gym learn --frozen` | Never writes the lock and installs nothing. Verifies every entry (installed or not); any difference (missing, digest-drift, modified) is `lock-drift`. With arguments (also under config `lock.frozen`): a bare name the lock holds reinstalls from it, anything else, `--no-lock` and `--digest` fail `lock-frozen: learn <arg> would modify <path>` |
| `gym train` | Entries with `requested` are skipped: `skip lint: pinned to 1.2.0 (gym learn lint@<new> to change)`. Others upgrade, then the lock entry is rewritten. git/dir/archive entries are not in the registry and are skipped as today |
| `gym train --frozen` / `lock.frozen: true` | Refuses any change: `lock-frozen: train would modify <path>`; exits 0 only if nothing would change. `lock.frozen: true` also makes a bare `learn` verify, as `learn --frozen` |
| `gym list --check` | Drift report, exit 1 on a failing finding (below); JSON `{scope, lock, ok, findings: [{name, status, detail}]}`; lock-missing when there is no lock |

No new verb for restore: `learn` already means "get skills onto this machine"; no-args form matches `npm install`/`bundle install`. Alternative `gym sync`: rejected, an extra verb for one behaviour.

Drift: (1) lock entry, no state record: `missing`; (2) state record, no lock entry: `untracked` (warning only); (3) `state.digest != lock.digest`: `digest-drift`; (4) install dir re-hashed differs from `state.digest`: `modified`. `--frozen` and `--check` fail on 1, 3, 4.

`--strict`: no change to semantics. First install without any pin is `digest-unpinned` warn, a deny under `--strict`; the adoption path is `learn` once (non-strict) then commit the lock, or `learn --digest`. New `admission.require_pin: none|remote|all` (default `none`; project may only raise): `remote` = registry/git/archive sources need a pin from lock, registry, or `--digest`; dir sources exempt (until the archive kind exists, `remote` covers registry and git). `all` covers every source. Mechanism as `approval_state` in ADR-0002: fact `digest_status = unpinned-required`, policy `admission-digest-required` (deny), reason `digest-required`. The source kind is the one the caller resolved (`registry` for an index entry, even one whose `source_url` is a local directory).

### 2.4 ExpectedDigest wiring (closes test case 11)

```go
// internal/install/admission.go
func ExpectedDigest(entry registry.SkillEntry, lock *lock.Entry) (digest, source string, conflict bool)
```

Order: operator `--digest` > lock entry > registry entry > none. `ExpectedDigest` itself orders lock over registry; `Admit` applies the operator pin (`AdmissionInput.ExpectDigest`) first and then reports no conflict. `source` is `operator|lockfile|registry`. `conflict` is true only when lock and registry both pin, differ, and describe the same version (`lock.version == entry.Version`); a registry that moved on to a newer version is not a conflict. On conflict the lock wins, `AdmissionResult.DigestPinConflict = true`, payload `digest_pin_conflict`, new policy `admission-digest-pin-conflict` (warn, reason `digest-pin-conflict`). `AdmissionInput` gains `Lock *lock.Entry`, `SourceKind`, `Requested` and `Commit`. A lock is passed only when the install honours it (`learn <name>`, restore): a new request (`name@version`, `url#ref`, a train upgrade) is checked against the registry's or the operator's pin, not the old lock's. When honouring a registry entry, the index's digest for the lock's version, if the index holds one, is the registry side of the comparison. Mismatch against whichever pin applies stays the hard `digest-mismatch`.

### 2.5 Tests (B)

| # | Case | Layer | Expect |
|---|------|-------|--------|
| B1 | Lock write twice, different insertion order | unit | byte-identical |
| B2 | Unknown field, bad schema, scope vs location | unit | `lock-invalid` |
| B3 | `learn name@v` hit / miss | integration | installs / `pin-unavailable` |
| B4 | `url#tag`, `url#<sha>` | integration | resolved `commit` recorded; bad ref `ref-not-found` |
| B5 | Pin precedence operator > lock > registry | unit | source field correct |
| B6 | Lock and registry differ, same version | unit | lock wins, conflict warn (case 11) |
| B7 | Lock and registry differ, registry newer | unit | no conflict |
| B8 | Locked skill, content changed upstream | integration | `digest-mismatch`, nothing written |
| B9 | `learn` no args offline from lock | integration | installs, no index read |
| B10 | `learn --frozen` with drift 1/3/4; with untracked only | integration | `lock-drift` / ok with warning |
| B11 | `train` with pinned entry, tracking entry, `--frozen` | integration | skip / upgrade+lock / `lock-frozen` |
| B12 | Hostile lock: abs dir source, `..` | unit | `lock-source-escapes` |
| B13 | `serve` includes `digest`; multi-version index | unit | present |
| B14 | `require_pin: remote`, project tries to lower | unit | clamped; deny `digest-required` |

Re-break: B5, B6, B7 (invert precedence / conflict test), B8, B10, B12, B14.

## 3. Uninstall (C)

### 3.1 Command

`gym unlearn <name|pattern>...` (metaphor pair of `learn`); aliases `uninstall`, `remove`, `rm`. Patterns use `filepath.Match` like `train`. At least one argument; removing all needs an explicit `'*'`.

| Flag | Meaning |
|------|---------|
| `-g/--global`, `--dir`, `--path`, `--agent` | As `learn` (scope root, install-path name/path, name patterns with `!` exclude) |
| `-n/--dry-run` | Print what would be removed, change nothing, exit 0 |
| `-y/--yes` | Skip confirmation |
| `--force` | Remove despite local modification (`modified`) |
| `--keep-lock` | Leave the lock entry |
| `--ignore-missing` | Absent skill is not an error |

Confirmation: explicit names never prompt. A glob matching more than one skill prompts on a TTY (`remove 3 skills from <root>? [y/N]`); on non-TTY without `--yes`: `confirmation-required: pattern matched 3 skills; pass --yes`. TTY test is `os.Stdin.Stat()` char-device (stdlib), excluding the null device (`/dev/null` is a character device but never interactive). `unlearn` acts on explicit flags only: it does not read the `learn.*` config keys. Its scope root is `--dir` or the current directory, never an inherited ancestor root.

### 3.2 What happens

1. Resolve scope and records (state records whose name matches, filtered by `--path`/`--agent`). None: `skill-not-installed: lint in <root> (installed in: <ancestor root>, global)` (the hint names where it is recorded) exit 1 (`--ignore-missing`: exit 0).
2. Per record validate `install_path` (section 0.2). Untrusted: record kept, that record fails.
3. Modified check: re-hash the directory; differs from `state.digest` and no `--force`: `skill-modified: <dir> differs from recorded <digest>; --force to remove` (protects local edits). A record with no recorded digest, or a tree that cannot be hashed, cannot be verified and needs `--force`. A tree holding only directories (no files, symlinks or other entries, at any depth; symlinks not followed) has nothing to lose: the check is skipped, so a retry after an interrupted removal needs no `--force`.
4. Remove the directory (`os.RemoveAll` of the validated path only). Already absent counts as removed.
5. Remove the record from `installed.json` after its directory is gone. The file stays (possibly `"skills": []`): deleting it would change root detection.
6. When no record of that name remains in the scope, remove the lock entry (unless `--keep-lock`).

Order is directory, state, lock: a crash leaves a state record without a directory (retry succeeds, step 4 tolerant), never an untracked directory. Partial failure: continue with other records/skills, print each failure, keep failed records, exit 1. Rerun is safe (idempotent). Atomic writes for state and lock.

Kept: approvals (operator decisions bound to digest; harmless, and re-install after uninstall stays approved), global config, the registry index. `.gym/` itself is never removed. A skill removed from one of several install paths keeps its lock entry and other records.

Exit codes: 0 done / dry-run / `--ignore-missing`; 1 for every error (rule unchanged). Reason strings: `skill-not-installed`, `install-path-untrusted`, `skill-modified`, `confirmation-required`; plus `remove-failed` for an I/O error removing the directory or writing state (record kept).

### 3.3 Events and guard

New topics (internal/events/skill.go): `gym.skill.uninstall.requested` `{skill, scope, folder, paths, request_id}` (informational; not gated), `gym.skill.uninstall.completed` `{skill, scope, folder, paths, request_id}`, `gym.skill.uninstall.failed` `{skill, scope, reason, request_id}`. The guard does not gate uninstall in v1: CEL sees no content facts, removal is operator-initiated and containment is enforced in Go (open question 4). `path-allowlist` is therefore not consulted; section 0.2 is the control. Docs: `docs/dev.md` topic table and reasons, `docs/user.md` Learn/Train neighbours, README commands.

### 3.4 Tests (C)

| # | Case | Layer | Expect |
|---|------|-------|--------|
| C1 | Remove named skill, 2 install paths | integration | both dirs, records, lock entry gone |
| C2 | Dry-run | integration | nothing changes, listing printed |
| C3 | Absent skill; with `--ignore-missing` | integration | exit 1 `skill-not-installed`; exit 0 |
| C4 | Second run after success | integration | `skill-not-installed`, no damage |
| C5 | Hostile record `install_path` outside tree, symlink, wrong basename | integration | `install-path-untrusted`, victim intact |
| C6 | Locally edited install | integration | `skill-modified`; `--force` removes |
| C7 | One path fails (read-only dir) | integration | other removed, failed record kept, exit 1, lock kept |
| C8 | Glob matching 3, non-TTY, with/without `--yes` | integration | `confirmation-required` / removes |
| C9 | Last record removed | integration | `installed.json` remains, `{"skills": []}` |
| C10 | Events | integration | requested then completed, ids equal |
| C11 | `train` on the C5 state file | integration | refuses (section 0.2) |

Re-break: C5, C6, C7 ordering (state before dir), C11.

## 4. Private, self-hosted sources (D)

### 4.1 Inventory and gap

| Capability | Today |
|------------|-------|
| Local dir, `file://` dir | Works. Hashed in place; lock/digest pin possible after B |
| Private git, SSH/HTTPS | Works through the user's git (keys, credential helpers, `insteadOf`). Prompts can hang non-interactively |
| `gym serve` as registry | Works; drops `digest`, one version (fixed in B) |
| Registry over `file://` | Fails |
| Authenticated registry | Missing (no headers) |
| Archive source | Missing: `unsupported source ... expected URL or existing directory` |
| Guarantee of no public host | Missing: `--offline` skips only the update notice |
| Pin required for remote | Missing (warn only) |

### 4.2 Source kinds (v1)

| Kind | Locator | Auth | Integrity | Trust |
|------|---------|------|-----------|-------|
| registry | coach `url` (https; http only for loopback), `file://` or plain path to an index file | none / bearer / git-credential | entry `digest`, lock | publisher of index; curated flag |
| git | `https://`, `ssh://`, `git@host:`; `#ref` | git's own | digest pin; commit recorded | host |
| dir (mirror) | path or `file://` | filesystem | digest pin | filesystem owner |
| archive | `*.tar.gz`, `*.tgz`, `*.zip` as path, `file://`, or https URL | as registry | digest of the extracted tree | whoever wrote it |

Archive staging (stdlib `archive/tar`, `compress/gzip`, `archive/zip`; no dependency): extract into a temp dir, then the normal pipeline. Skill root is the archive root, or the sole top-level directory. Rejected: absolute names, `..`, symlinks/hardlinks/devices (consistent with `digest-unsupported-entry`), more than 10 000 entries, more than 256 MiB expanded, duplicate paths: `archive-invalid: <reason>`. Digest is over the extracted tree (`gym.digest/v1`), not the archive bytes (archive metadata is not reproducible, ADR-0002). `gym digest` also accepts an archive. A pack command is a non-goal.

Pin requirements: `admission.require_pin: remote` (2.3) is the knob. Recommendation for an isolated deployment: `require_pin: remote`, `network.mode: allowlist`. `digest-unpinned` stays warn by default, composing with `--strict`.

### 4.3 Network policy

```yaml
network:
  mode: open          # open | allowlist | offline
  allow_hosts: []     # "host", "host:port", "*.suffix"
```

Resolution: user/system/`-c` set freely; project and folder layers tighten only (A3 table): mode may only rise (`open < allowlist < offline`), `allow_hosts` only intersect. `--offline` (kit global flag) sets `mode: offline` for the run. `--allow-host` flag: none (use `-c network.allow_hosts=[...]`).

Enforcement lives in one place, `internal/netpolicy` (new): `Check(rawURL) error`, called by every egress:

| Egress | Hook |
|--------|------|
| registry `HTTPFetcher`, coach manifest fetch, archive URL | before request and on every redirect (`CheckRedirect`) |
| git clone/fetch | effective URL from `git ls-remote --get-url <src>` (applies `insteadOf` mirrors), host parsed incl. scp-like form, checked before spawning git; git run with `-c protocol.allow=user -c protocol.https.allow=always -c protocol.ssh.allow=always -c protocol.file.allow=never -c protocol.ext.allow=never` and `GIT_TERMINAL_PROMPT=0` when stdin is not a TTY |
| upgrade notifier | skipped unless `mode: open` |
| `judge` (rsx shell-out) | skipped under non-open modes with a warning |

Errors (reason `network-denied`, hard, not `--insecure`-relaxable, published as `install.rejected` when during an install): `network-denied: host "github.com" not in network.allow_hosts (mode allowlist)`; `network-denied: offline mode forbids network access to "github.com"`. Offline still allows dirs, archives on disk, a cached index (`gym update` is the one command that fails, by design), and lock-driven `learn` from local mirrors.

Limits, stated: the policy is by host name, not network path or resolved IP; a firewall remains the guarantee. Loopback is not implicitly allowed (`127.0.0.1:4873` must be listed).

### 4.4 Credentials

`repositories[].auth` (user/system/`-c` layers only; a project/folder layer `auth` is ignored with warning, otherwise a cloned repo could point a token at its own host):

```yaml
repositories:
  - name: corp
    url: https://registry.corp.example/skills.json
    auth: {type: bearer, token_env: GYM_CORP_TOKEN}   # or token_file: ~/.config/gym/corp.token
  - name: corp2
    url: https://registry2.corp.example/skills.json
    auth: {type: git-credential}                      # `git credential fill`, Basic
```

Rules: token only to the exact host of the repository URL, over https (http only for loopback); `Authorization` is never forwarded across a host change on redirect (Go already strips it; tested); `token_file` must be mode 0600 and owned by the user, else `auth: token file <p> is group/world accessible`; the token is read per request, never cached on disk. Archive https URLs reuse the auth of the repository that listed them (same host only). Git sources use git's own credentials; gym never reads or stores them. Alternatives: static `token` in config (rejected: secret in a file that may be committed or shared), `.netrc` (rejected for v1: no stdlib parser, git-credential covers it), `Authorization` header from env verbatim (rejected: scheme confusion).

Never logged, never in state, lock, events, payloads, or `-V` output: tokens, `Authorization`/`Cookie` headers, URL passwords, query tokens (`install.RedactURL`, section 0.4), git/curl stderr (redacted before printing). Test with a canary token string asserted absent from stdout, stderr, state, lock, event bus.

### 4.5 Tests (D)

| # | Case | Layer | Expect |
|---|------|-------|--------|
| D1 | Registry fetch with bearer from env/file; wrong perms file | integration (httptest) | header sent; perms error |
| D2 | Redirect to another host | integration | no `Authorization` forwarded |
| D3 | Project-layer `auth` | unit | ignored, warning |
| D4 | Canary token in logs/state/lock/events | integration | absent everywhere |
| D5 | URL with password | unit | `source-credentials-in-url` |
| D6 | `allowlist`: allowed / denied host for registry, git, archive | integration | proceeds / `network-denied` |
| D7 | `insteadOf` to allowed mirror; source host public | integration | allowed via effective URL |
| D8 | `--offline` with `update`, git source, dir source, cached index | integration | denied, denied, ok, ok |
| D9 | Project config tries `mode: open` or widens `allow_hosts` | unit | clamped |
| D10 | Notifier and judge under non-open mode | integration | not invoked |
| D11 | Archive: traversal, symlink, hardlink, bomb limits, sole top dir | unit | `archive-invalid` / ok |
| D12 | Archive digest equals dir digest of same tree | unit | equal |
| D13 | `require_pin: remote` with git source, no pin | integration | `digest-required` |
| D14 | Registry over `file://`/plain path | integration | loads |
| D15 | Git runs with hardened `-c` set and no prompt | unit (arg capture) | as specified |

Re-break: D2, D3, D4, D6, D7, D9, D11, D13.

## 5. Cross-cutting

### Events and payloads

`SkillInstallRequestedPayload` and `SkillUpgradePlannedPayload` gain (all `omitempty`, defaulted in guard activation): `folder` (scope root, project scope), `source_kind` (`registry|git|dir` so far), `source_host` (with the sources work), `requested`, `resolved` (commit), `lock_present` (bool: a lock entry was honoured as the pin), `digest_pin_conflict` (bool). `digest_source` gains `lockfile` and `operator`; `digest_status` gains `unpinned-required`. `SkillInstallCompletedPayload` gains `folder`. New uninstall topics (3.3). `topics_roundtrip_test.go` extended.

### Rejection reasons (stable strings)

New: `digest-pin-conflict` (warn finding), `digest-required`, `network-denied`, `lock-invalid`, `lock-missing`, `lock-drift`, `lock-frozen`, `lock-source-escapes`, `pin-unavailable`, `ref-not-found`, `archive-invalid`, `source-credentials-in-url`, `install-path-untrusted`, `skill-not-installed`, `skill-modified`, `confirmation-required`, `remove-failed`. Of these `install.rejected` is published for `digest-required`, `network-denied`, `lock-drift`, `lock-source-escapes`, `source-credentials-in-url` (install attempts); the rest are command errors and publish nothing (as fetch/I-O errors do today).

Exit code: 1 for every error and rejection (unchanged; distinct codes remain a cross-cutting future change).

### Docs to touch

`docs/user.md` (Scopes, Learn, Train, new Unlearn/List/Lock, Network and credentials), `docs/dev.md` (Install, Admission payload table, reasons, Upgrade policy, config precedence), `README.md` (commands, Learn sources, Serving a registry), `docs/agent.md` if it lists commands. No migration or compatibility notes (pre-stable).

## 6. Security review of this design

| Abuse | Where | What stops it |
|-------|-------|---------------|
| Hostile repo config sets `validator.command` | project layer | 0.1 key allowlist |
| Hostile `installed.json` redirects `train`/`unlearn` deletes | state | 0.2 containment + trusted-parent check |
| Hostile config loosens ceiling/network/pin, or hides an ancestor's tightening | folder config | A3 fold rules; chain merge instead of nearest-only |
| Folder config sends a token to its own host | `auth` | user/system layers only; exact-host, https only, no redirect forwarding |
| Planted `.gym` in a shared ancestor | chain | ownership + not-writable test, HOME boundary |
| Lock entry pointing at local secrets or escaping the folder | lock source | `lock-source-escapes`; digest must still match |
| Lock/registry downgrade to a vulnerable older version | pins | lock wins and is committed/reviewed; conflict warn; registry-newer is not a conflict; `train` never moves pinned entries |
| Tag moved after pinning | git ref | commit + digest recorded; mismatch is hard `digest-mismatch` |
| Archive traversal, links, bombs | archive | `archive-invalid` limits; no symlinks; extraction into temp |
| Git transports (`ext::`, `file`) and redirects | git | `looksLikeURL` prefixes + `protocol.*.allow` + effective-URL host check |
| Mirror `insteadOf` hides a public host | network | host checked on the effective URL, not the typed one |
| Credential in logs/state/lock/events/`.git/config` | all | redaction, password URLs rejected, `.git` not copied, canary test |
| `unlearn '*'` by script | uninstall | non-TTY glob needs `--yes`; dry-run |
| Uninstall deletes user edits | uninstall | digest compare, `--force` |
| Malicious registry serves other version than asked | pin | `FindVersion` exact; digest pin |
| Allowlist bypass via DNS/IP | network | not claimed; documented firewall caveat |
| Frozen bypass by project config | lock | `lock.frozen` can only be turned on by a project layer |

## 7. Non-goals (v1)

Version ranges and resolution solver; signed lock/attestations; materialising or symlinking ancestor skills into descendants; `gym pack`; `.netrc`; OAuth/OIDC registry login; per-skill network policy; CEL gating of uninstall; distinct exit codes; org-level policy server; walking above `$HOME`; multiple versions of one skill in one scope.

## 8. Implementation plan

### Order and dependencies

```
F0 hardening ──┬── A folder scope ──┐
               ├── C uninstall ─────┼── B lock/pins ── D sources (archive, auth) 
               └── D1 netpolicy ────┘
```

| PR | Depends on | Notes |
|----|-----------|-------|
| F0 | none | ships alone, first |
| A | F0 | owns `internal/scope`, `internal/config/chain.go`, `list`, `warmup`, `train -r` |
| C | F0 | after F0 uses existing scope logic; `--dir` lands via A's resolver (small follow-up inside A's PR to `unlearn.go`) |
| B | A, C | needs per-scope lock path (A) and the removal hook (C); owns the `stageSource` refactor into `internal/install/source.go`, registry multi-version, `serve` digest/versions |
| D | B | archive staging builds on B's `Source`; `internal/netpolicy` and auth fetcher (D1) are independent and may land earlier in parallel with A and C |

A and C touch disjoint files (one `AddCommand` line in `cobra.go`, docs) and can be built in parallel; merge order F0, A, C, B, D. Rebase-only merging: each PR applies on the previous; shared lines are limited to the `Config` struct (new fields, one per PR), `AddCommand`, and docs.

### Per feature

| | F0 | A | B | C | D |
|--|----|---|---|---|---|
| New files | `internal/install/redact.go`, `internal/fsutil/atomic.go` | `internal/scope/scope.go`, `internal/config/chain.go`, `internal/cli/list.go`, `internal/cli/scope_flags.go`, `internal/cli/train.go` | `internal/lock/lock.go`, `internal/install/source.go`, `internal/cli/learn_lock.go` | `internal/cli/unlearn.go`, `internal/install/remove.go` | `internal/netpolicy/netpolicy.go`, `internal/install/archive.go`, `internal/registry/auth_fetch.go`, `internal/config/network.go` |
| Touched | `config/config.go`, `install/install.go`, `fsutil/copy.go`, `cli/app.go` (train path only) | `project/project.go`, `config/config.go` (loader call), `cli/app.go` (3 root calls), `cli/warmup.go`, `upgrade/planner.go` | `install/admission.go`, `install/install.go` (stage call), `registry/registry.go`, `serve/serve.go`, `cli/app.go` (learn/train lock hooks), `events/skill.go`, guard activation | `cli/cobra.go` (1 line), `events/skill.go` | `install/install.go` (git args), `registry/registry.go` (`RepositorySource.Auth`), `cli/coach.go`, `cli/cobra.go` (`--offline`), `config/config.go` |
| Config keys | none new | none (chain semantics) | `lock.frozen`, `admission.require_pin` | none | `network.mode`, `network.allow_hosts`, `repositories[].auth` |
| Schema | none | none | `lock.json` `gym.lock/v1` | none | none |
| Effort | S | L | L | M | L |
| Riskiest part | key allowlist missing a legit key | chain merge beside the kit loader; `learn` note vs surprise | multi-version registry index + lock/state ordering | deleting the wrong directory (containment) | complete egress coverage; git arg hardening across git versions |

New command files keep `cobra.go` and `app.go` edits to a registration line plus calls into the new packages; the existing over-500-line files do not grow by logic.

### Mutation (re-break) checks

Listed per feature above. Mandatory set: F0 allowlist and containment, A clamp/containment/ownership, B precedence/conflict/frozen/escape/require_pin, C containment/modified/ordering, D redirect-auth/allowlist/`insteadOf`/archive limits/pin. Each: remove or invert the check, watch the test fail, restore.

F0 tests (each re-broken): project `validator` ignored and not executed (probe as test); `install_path` outside tree refused by `train`; symlinked parent; `.git` absent from install and digest unchanged; URL password rejected and redacted from state, errors.

## Decisions

1. `learn` without `--dir` installs into the current directory, with a note when a nearer ancestor scope exists.
2. Inheritance is resolved at read time only (`warmup`, `list --effective`); no copying into descendants.
3. Lock lives at `<root>/.gym/lock.json`; gym writes `.gym/.gitignore` for `installed.json` and `skills/`.
4. Uninstall is not gated by guard policies; containment is enforced in Go.
5. Restore from a lock is `gym learn` with no arguments; no `sync` verb.
6. Uninstall is `gym unlearn`, aliases `uninstall`, `remove`, `rm`.
7. An absent skill is exit 1 `skill-not-installed`; `--ignore-missing` relaxes it.
8. Project-layer config honours an allowlist of keys; `repositories` stays on it.
9. The hardening in section 0 ships as its own fix, ahead of the features.
10. The registry index and `serve` output keep every `(name, version)`.
11. Loopback is not implicitly allowed under `network.mode: allowlist`.
12. `admission.require_pin` defaults to `none`; `remote` is the documented setting for isolated deployments.
13. Registry auth in v1: bearer (`token_env`, `token_file`) and `git-credential`.
14. The inheritance chain stops at `$HOME` and skips folders not owned by the user or writable by others.

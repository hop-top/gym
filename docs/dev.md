# gym: Dev Guide

This is the maintainer/contributor view of `gym`.

## Build and Test

```bash
go build -o gym ./cmd/gym
go test ./...
go vet ./...
gofmt -w .
```

Or:

```bash
make build
make test
make vet
make fmt
```

## Architecture (packages)

- `cmd/gym`: main entrypoint
- `internal/cli`: command parsing + subcommands + TUI/REPL
- `internal/config`: user/project config merge; mutation helpers
- `internal/project`: project root detection; `.gym/` dir constants
- `internal/registry`: `skills.json` fetch/merge; curated labeling; index store
- `internal/install`: stage source (git clone / local dir); validate (`skills-ref`); copy into install path
- `internal/state`: installed skill store (global + project)
- `internal/upgrade`: upgrade planner (scope + mirror policy)
- `internal/serve`: serve `skills.json` + debug `_index.json`
- `internal/ide`: detect IDE/TDE standard paths (global + project)
- `internal/xdg`: XDG directory resolution + mkdirs

## Config + Precedence

- Config sources:
  - user: `$XDG_CONFIG_HOME/gym/config.json` (fallback `~/.config/gym/config.json`)
  - project: `<project-root>/.gym/config.json` (only if a project root is detected)
- Merge strategy:
  - arrays append (repositories/install_paths)
  - scalar blocks override defaults when present (`curation`, `validator`, `upgrade`)

## Registry Refresh

`internal/registry.Refresh`:
- fetch each configured coach URL
- parse `skills.json` into a `skillsDoc`
- compute curated label:
  - config `curated: true` is downgraded if `repository.stars < min_stars`
  - `repository.curated` or `repository.curated_early` forces curated
- flatten skill entries into a merged index:
  - one best entry per skill name
  - prefer curated; otherwise prefer newer version
- report diffs vs previous index (new skills, upgrade candidates)

Index storage:
- `$XDG_DATA_HOME/gym/registry-index.json`

## Install

`internal/install.InstallSkill`:
- stage source:
  - local directory: use as-is
  - URL: `git clone --depth 1` to temp dir
- resolve skill root:
  - if registry entry has `path`, use `<repo>/<path>`
  - else use repo root
- validate using configured `validator.command` (default `skills-ref test {path}`)
- copy validated directory into install path:
  - destination is `<install-path>/<skill-name>`

## Upgrade Policy

Upgrade selection is planned in `internal/upgrade.Plan`:
- outside a project (or no project skills): upgrade global only
- inside project with project-installed skills:
  - upgrade project skills
  - by default also upgrade matching global installs
  - `--no-global` disables mirroring

## Versioning + Releases

- `gym version` reads embedded values set by ldflags (and falls back to Go build info).
- Releases are built via GoReleaser:
  - tag `vX.Y.Z` triggers `.github/workflows/release.yml`
  - `.goreleaser.yaml` injects version/commit/date into `internal/cli`

## Vanity Go Module (Cloudflare Pages)

If you want a vanity module path like `gym.hop.top` or `hop.top/gym`, you must serve `go-import` metadata at that URL.

This repo includes a minimal Cloudflare Pages site in `site/vanity/` and a workflow that publishes it to the `vanity` branch:
- workflow: `.github/workflows/publish.yml`
- branch: `vanity`

Cloudflare Pages config:
- production branch: `vanity`
- output directory: `.`
- attach custom domain(s) as needed

Until a landing page exists, non-`?go-get=1` requests redirect to GitHub.

## CI

`.github/workflows/ci.yml` enforces:
- `go mod tidy` produces no diff
- `gofmt` clean
- `go test ./...`
- `go vet ./...`

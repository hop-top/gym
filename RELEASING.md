# Releasing

Releases run via [release-please](https://github.com/googleapis/release-please)
with hop-top/.github's **target-versioning** extension: the branch decides the
version. `next` previews a target stable version as prereleases; `main` cuts
exactly that version from its rc and otherwise only patches what is already
stable. Nothing ships that no prerelease previewed. Nobody picks a version and
nobody writes `Release-As` (the run ignores it).

Config: `.github/release-please-config.json`. Manifest:
`.github/.release-please-manifest.json`, written by release PRs only.

## Where does my work land first?

| Contribution | Target branch |
|--------------|---------------|
| Feature (`feat:`), breaking change (`feat!:` / `BREAKING CHANGE`) | `next` |
| Fix for the current stable release (`fix:` / `perf:`) | `main`, then forward-port to `next` |
| Fix for an older supported release | its `release/<major>.<minor>` branch |
| Docs, tests, CI, chores | `next` |

A `feat` or breaking change landed on `main` while a stable release exists
blocks the release there until it is reverted.

## Branches

| Branch | Role | Version |
|--------|------|---------|
| `next` (default) | prereleases | `x.y.z-alpha.N`, `-beta.N`, `-rc.N` |
| `main` | stable | `x.y.z` cut from the rc; then patches only |
| `release/x.y` | maintenance | patches only |

## Flow

1. Every push to `next`, `main` or `release/<major>.<minor>` runs
   `.github/workflows/release-please.yml` (shared `release-please-on-push.yml`
   with `versioning-extension: true`).
2. release-please keeps one release PR open per branch, with the version the
   rules below give and its changelog entry. A change the rules cannot version
   is **blocked**: no PR, the run fails with the reason. A state the branch
   does not release (an alpha or beta on `main`) is **skipped** with a notice.
3. Merging the release PR creates the `v<version>` tag and GitHub release (a
   GitHub prerelease exactly when the version has a suffix).
4. The tag starts `.github/workflows/release.yml`: GoReleaser builds the
   archives and attaches them to that release (`prerelease: auto`,
   `mode: keep-existing`).

Tags are `vX.Y.Z` with no component prefix: the Go module sits at the repo
root, so `go install hop.top/gym/cmd/gym@vX.Y.Z` resolves them directly.
`@latest` picks the highest stable tag; with none yet, the newest prerelease.

## Version rules

The **target** of `x.y.z-rc.N` is `x.y.z`. The required target is the last
stable release bumped by the commits since: `feat` / breaking -> next minor
(before 1.0), `fix` / `perf` -> next patch.

`next`:

| Situation | Proposal |
|-----------|----------|
| No release yet | `initial-version` from the config: `0.1.0-alpha.0` |
| Current target covers the commits | counter + 1: `0.1.0-alpha.0` -> `0.1.0-alpha.1` |
| Commits require a higher target | new line at alpha: `0.2.0-alpha.0` after `0.1.0` is stable |
| Target already released stable | new line from that stable at `alpha.0` |
| Promotion recorded for the line | `<channel>.0` on the same target |

`main`:

| Situation | Proposal |
|-----------|----------|
| Manifest at `x.y.z-rc.N` (`next` merged in) | `x.y.z`, even with no commit since the merge |
| ... and a commit since requires more than `x.y.z` | blocked: land it on `next` |
| ... and `x.y.z` already released (hotfix on `main`) | blocked: re-line on `next`, promote to rc, merge again |
| Manifest stable, only fixes since | patch: `0.1.0` + `fix` -> `0.1.1` |
| Manifest stable, a `feat` or breaking change since | blocked: land it on `next` |
| Manifest at alpha or beta | skipped |
| No release yet | skipped: release it on `next` first |

`release/x.y`: stable patches only; a `feat` blocks, a prerelease manifest is
skipped.

## Cutting a stable release

1. `next` is at an rc of the target (`0.1.0-rc.N` released).
2. Bring `next` into `main` with its history (the manifest travels with it).
   The run on `main` opens a release PR for `x.y.z`.
3. Merge that release PR: tag, GitHub release, GoReleaser archives.
4. Bring `main` back into `next` so the stable manifest and changelog reach
   `next`; the next `feat` there opens `x.(y+1).0-alpha.0`.

## Promoting a line (alpha -> beta -> rc)

Every new line starts at `alpha.0`. Moving it to beta, then rc, is recorded in
the config, one step at a time, never backwards:

```sh
make promote-beta        # gym x.y.z-alpha.N -> x.y.z-beta.0
make promote-rc          # gym x.y.z-beta.N  -> x.y.z-rc.0
make promote-beta DRY_RUN=1
make promote-open-line LINE=1.0.0
```

This commits the config change alone on the current branch; push it to `next`
(PR). The run there cuts the promoted release. `release-promote-gate.yml`
rejects PRs that change the manifest (other than release PRs and `next` /
`main` merges) or record anything `promote.js` would not write.

Needs `node` (>= 20), `npm`, `git` and network once: the script checks out
hop-top/.github at the pinned commit and caches it under
`${XDG_CACHE_HOME:-~/.cache}/gym/release-please-target/<sha>`.

## Re-triggering

- Release PR missing or stale: push to the branch, or run the `release-please`
  workflow manually (Actions > release-please > Run workflow, on `next`,
  `main` or `release/x.y`).
- Tag and GitHub release exist but the archives are missing: re-run the
  `Release` workflow run for that tag.
- Release PR merged but no tag: re-run the `release-please` workflow run for
  that merge commit.

## Dry run

Reads only; the config is read from the target branch. From a checkout of
hop-top/.github at the commit pinned in `release-please.yml`:

```sh
cd scripts/release-please-target && npm ci
TOKEN="$(gh auth token)" GITHUB_REPOSITORY=hop-top/gym \
TARGET_BRANCH=next \
CONFIG_FILE=.github/release-please-config.json \
MANIFEST_FILE=.github/.release-please-manifest.json \
DRY_RUN=true node bin/run.js
```

## Pins

`release-please.yml` and `release-please-preflight.yml` call hop-top/.github at
one full commit SHA (version in a trailing comment); `tooling-ref` names the
same commit. Move all three together. Dependabot ignores them.

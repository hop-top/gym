# gym e2e cassette suite

End-to-end tests that exercise the **gym → bus → rsx/guard/tip** loop,
with all subprocess + HTTP boundaries replayed from xrr cassettes.

The suite is gated behind a `cassettes` build tag so the regular
`go test ./...` run never compiles or executes it. This keeps the
default `-race -count=1` cycle fast and free of any external deps.

## Running

Replay (default; no rsx / GitHub / tip needed):

```sh
/usr/bin/env go test -race -count=1 -tags cassettes ./e2e/...
```

Verbose:

```sh
/usr/bin/env go test -race -count=1 -v -tags cassettes ./e2e/...
```

## Recording cassettes

Set `XRR_RECORD=1` to re-record cassettes against live tooling:

```sh
XRR_RECORD=1 GITHUB_TOKEN=... \
  /usr/bin/env go test -count=1 -tags cassettes -run TestE2E/HappyPathCoachHire ./e2e/...
```

Recording requires `rsx` on `PATH` for the rsx-backed scenarios
(HappyPathCoachHire, RSXFlagsRisky\*). When `rsx` is missing those
sub-tests `t.Skipf` rather than fail — the suite stays green even on
boxes without the external deps.

The non-rsx scenarios (`GuardCatchesCurlBash`, `TipAdapterBudget`,
`WarmupCheat`) carry no live dependencies; they drive the bus + guard
+ cheat directly and don't talk to any external process. Their
cassette directories (`cassettes/<scenario>/`) hold pure fixtures
(`manifest.json`) rather than xrr recordings.

## Cassette layout

One directory per scenario under `cassettes/`:

| Scenario | Directory | Adapter | What it captures |
|---|---|---|---|
| HappyPathCoachHire | `cassettes/happy-coach-hire/` | exec | `rsx analyze github.com/foo/happy --json` + `rsx policy test … balanced` |
| RSXFlagsRiskyDefault | `cassettes/risky-coach-hire/` | exec | rsx returns `score=2.5`, `policy.passed=false` |
| RSXFlagsRiskyStrict | `cassettes/risky-coach-hire/` | exec | (re-uses the risky cassette) |
| GuardCatchesCurlBash | `cassettes/curl-bash/` | (fixture) | `manifest.json` with a `curl … | bash` postinstall |
| TipAdapterBudget | `cassettes/tip-adapter-budget/` | (none) | bus-only scenario |
| WarmupCheat | `cassettes/warmup-cheat/` | (none) | bus-only scenario |

xrr cassette files follow the v1 spec. Each interaction is two YAML
files: `<adapter>-<fingerprint>.req.yaml` + `.resp.yaml`. The
fingerprint is `sha256(canonical-json({argv, stdin}))[:8]` for the
`exec` adapter (see the xrr exec adapter source).

## Secret hygiene

xrr captures argv + stdout, not env. The current cassettes carry no
secrets. Before committing a freshly recorded cassette run:

```sh
grep -rE "(ghp_|ghs_|github_pat_|AKIA|sk_live|aws_secret)" e2e/cassettes/ \
  || echo "clean"
```

## Subsystem boundaries (no surgical changes outside e2e/)

The harness stays a pure consumer of the existing subsystems
(`internal/eventbus`, `internal/judge`, `internal/guard`,
`internal/cheat`). The one exception is that the install pipeline now scans the staging directory and
forwards `postinstall`/`shell_hook`/`largest_binary_bytes`/etc onto
`gym.skill.install.requested` so the curl-bash policy fires on a
real install. See
`internal/install/scan.go` and `internal/events/skill.go`.

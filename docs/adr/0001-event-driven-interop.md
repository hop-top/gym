# ADR-0001: Event-Driven Interop Between gym, rsx, tip, and kit/runtime/policy

**Status**: Accepted  
**Date**: 2026-05-05  
**Deciders**: Idea Crafters team  

## Context

`hop.top/gym` is one of several tools in the hop.top suite (kit, tlc, rsx, tip, aps, ctxt). Each tool solves a distinct problem:

- **gym**: universal package manager for agentskills.io skills
- **rsx**: repository trust analysis and security policy evaluation
- **tip**: shell command context and hint system  
- **kit/runtime/policy**: CEL-based policy evaluation engine
- **kit/runtime/bus**: cross-process event pub/sub with replay and auditing

Historically, gym operated in isolation. Adding trust scoring (rsx), policy gates (kit/runtime/policy), and hint integration (tip) requires coordination across multiple independent tools. Three coupling patterns were explored:

### Option A: Direct Go imports

gym imports rsx, tip, and kit/runtime/policy as libraries and calls them directly.

**Pros**:
- Simplest: one call site per integration point.
- Type-safe: compile-time contracts.
- Lowest latency: no IPC overhead.

**Cons**:
- Tightly couples gym's release cycle to rsx, tip, and policy library versions.
- Impossible to evolve subsystems out-of-process (e.g. rsx as a separate daemon).
- No replay or audit trail; events are ephemeral.
- Violates hop.top's composition model (each tool controls its own interface).

### Option B: Subprocess shell-out

gym invokes `rsx`, `tip`, and policy validators as external processes, parsing their CLI output.

**Pros**:
- Loose coupling: each tool updates independently.
- Survives version skew: gym can work with multiple rsx versions.

**Cons**:
- N-way wiring complexity: gym must know how to invoke each tool's CLI.
- Brittle parsing: changes to CLI output break integration.
- No structured fan-out: if multiple tools need to react to the same event, gym must invoke each separately.
- Scattered logging and error handling across processes.
- Performance cost on hot paths (install/upgrade gates need synchronous validation).

### Option C: Event bus + thin adapters

gym becomes a publisher and subscriber on `kit/runtime/bus`. Lifecycle events flow through topics; independent subscribers (judge for rsx, guard for policy, cheat for tip) attach as separate services. Each subscriber has its own logic and publishes answers back, correlated by request ID.

**Pros**:
- Decoupled: gym, rsx, tip, policy develop independently.
- Replayable: events are persisted by the bus (SQLite or network adapters).
- Unified audit trail: one source of truth for what happened.
- Out-of-process ready: subscribers can move to separate services without rewriting gym.
- Matches existing hop.top patterns: tlc, aps, and kit itself use bus for coordination.
- Fan-out: multiple subscribers react to the same event.
- Synchronous gates via request/response: install/upgrade gates block on a verdict, with bounded timeout.

**Cons**:
- Payloads must round-trip JSON (no raw types in events).
- Topic catalog must be governed (naming, versioning, stability).
- Subscribers must be idempotent on request ID (kit/bus has no built-in dedup).
- Timeouts and fail-open/fail-closed semantics add complexity.

## Decision

**Chosen: Option C (Event bus + thin adapters)**

gym becomes a publisher of lifecycle events and a subscriber to judgement, policy, and cheat-sheet events. All cross-tool coordination flows through `kit/runtime/bus` with 4-segment topic names following the pattern `[Source].[Category].[Object].[Action]`.

## Rationale

1. **Decoupling**: gym can evolve independently of rsx, tip, and policy. When rsx adds native bus publishing, the judge subscriber is swapped without touching gym's call sites.

2. **Replayability and auditing**: every decision (coach hiring approved, skill install rejected, hint emitted) is captured as an event. Subscribers can be replayed against historical events for diagnostics or training.

3. **Composition**: subscribers live in separate packages (`internal/judge`, `internal/guard`, `internal/cheat`) and can eventually move to separate services or plugins. gym remains the orchestrator but not the monolith.

4. **Alignment with hop.top norms**: kit, tlc, and aps already use the bus for coordination. Adopting it for gym makes the ecosystem composable by design.

5. **Synchronous gates with timeouts**: install/upgrade gates must block on a verdict. The bus's request/response pattern with correlation IDs and bounded timeouts solves this without falling back to subprocess parsing or unsafe polls.

## Alternatives Considered

### A. Direct Go imports (rejected)

Couples gym to rsx and tip release cadence. Prevents out-of-process deployment. No replay. Violates hop.top composition principle.

### B. Subprocess shell-out (rejected)

Scattered wiring, brittle parsing, no fan-out. Higher latency on install/upgrade gates.

## Consequences

### Positive

- **Independent evolution**: rsx, tip, and kit/runtime/policy can release on their own schedule.
- **Audit trail**: every decision is captured as an event. Debugging, regression, and training all benefit.
- **Out-of-process ready**: judge, guard, and cheat can move to separate services later.
- **Fan-out**: if aps or ctxt want to observe gym events, they subscribe on the same bus without gym knowing.
- **Replay**: record/playback of test cassettes (via xrr) proves end-to-end correctness.
- **Unified logging**: all bus events flow through a single Elasticsearch/SQL sink if desired.

### Negative

- **JSON round-trip requirement**: all payloads must be serializable (no raw types, pointers, or methods). Limits expressiveness slightly.
- **Topic governance**: the catalog (`gym.*`, `rsx.*`, `policy.*`, `tip.*`, `toolspec.*`) must be stable across releases. Schema versioning and migration become responsibilities.
- **Idempotency burden**: subscribers must tolerate duplicate events (same request ID). No built-in dedup in kit/bus; it's the consumer's job.
- **Timeout semantics**: gates must decide fail-open vs fail-closed when the bus reply doesn't arrive in time. Adds flag plumbing (`--strict`, `--gate-timeout`).
- **Extra latency**: one local IPC round-trip (or network round-trip if using network adapter) per gate. Typically <10ms for memory adapter, but still slower than a direct call.

### Neutral

- **Bus adapter selection**: memory for CLI, SQLite for daemon, network for multi-host. Adds operational complexity but enables deployment flexibility.
- **Error handling**: subscribers that fail to answer (crash, timeout, parse error) are treated gracefully with fail-open defaults or fail-closed under `--strict`.

## Implementation Notes

### Topic catalog

All topics follow `[Source].[Category].[Object].[Action]`:

**gym publishes**:
- `gym.coach.repository.hire_requested` — triggers rsx scoring (sync gate via request/response)
- `gym.coach.repository.hired` — post-commit signal
- `gym.coach.repository.dismissed` — post-commit signal
- `gym.skill.install.requested` — triggers policy guard (sync gate)
- `gym.skill.install.completed` — post-commit signal
- `gym.skill.install.rejected` — policy/precondition rejection
- `gym.skill.upgrade.planned` — upgrade plan available for veto
- `gym.skill.upgrade.applied` — post-commit signal per skill
- `gym.skill.upgrade.failed` — per-skill error signal
- `gym.warmup.metadata.emitted` — skill metadata snapshot
- `gym.toolspec.cli.published` — gym's CLI as a ToolSpec
- `gym.cheat.hint.emitted` — optional cheat-sheet hint from tip bridge

**gym subscribes to**:
- `gym.coach.judgement.completed` (correlated by request_id) — rsx verdict
- `policy.guard.install.evaluated` (correlated by request_id) — CEL guard verdict
- `tip.shell.command.observed` (optional, GYM_TIP_BUS_BRIDGE=1) — observed commands
- `toolspec.cli.resolved` — remote spec sources

### Payload contracts

Every payload is a JSON-serializable struct with a `request_id` field when part of a request/response pair. Structs live in `internal/events/` and are versioned implicitly (no schema version in the payload itself, but breaking changes are caught at the binary/type level).

### Gate semantics

Synchronous gates (`install`, `coach hire`) use `Bus.RequestResponse(ctx, reqTopic, respPattern, payload, timeout)`:

1. Publish the request with a UUID v7 `request_id`.
2. Block awaiting a response on a correlated topic (e.g. `policy.guard.install.evaluated` with matching `request_id`).
3. On timeout:
   - `--strict`: fail-closed (return error, gate blocks install).
   - Default: fail-open with a WARN log (proceed, let the operator decide).
4. On verdict:
   - `allow`: proceed silently.
   - `warn`: log findings, proceed.
   - `deny`: error unless `--insecure` is set (deny → warn).

### Subscriber idempotency

Subscribers receive events (possibly duplicated by the caller). An in-memory cache keyed by `request_id` ensures the same verdict is returned on re-delivery without re-running expensive operations (rsx subprocess, CEL evaluation).

## References

- `internal/events/`: payload structs and topic constants.
- `internal/eventbus/`: bus wrapper and request/response helpers.
- `internal/judge/`: rsx judge subscriber.
- `internal/guard/`: CEL policy guard subscriber.
- `internal/cheat/`: toolspec + tip bridge subscriber.
- `e2e/`: integration tests with cassettes (xrr).
- `hop.top/kit/runtime/bus`: bus API docs.

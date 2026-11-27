// Package eventbus is gym's thin lifecycle wrapper around
// hop.top/kit/go/runtime/bus.
//
// It owns three responsibilities:
//
//  1. Adapter selection (memory / sqlite / network) driven by config
//     and environment.
//  2. Source/OriginID derivation from kit/identity, with a graceful
//     fallback to "gym-<hostname>" when identity is unavailable.
//  3. The RequestResponse helper used by sync gates (judge, guard):
//     publish a request topic with a fresh request_id, block for a
//     correlated response on a wildcard pattern, time out after a
//     bounded deadline.
//
// Subscribers of gym-published events MUST be idempotent on
// request_id; the bus has no built-in dedupe.
package eventbus

# ADR 0009: Dashboard transient hints and authoritative REST resync

Status: Accepted (Phase 8)

## Context

Independent API/Worker processes mutate PG. UI freshness must not become a
business transaction dependency. Existing Job consumer groups deliver work;
the browser needs fanout and current snapshots.

## Decision

Use version-1 resource/status hints after commit, via separate Redis Pub/Sub
to each API's bounded WebSocket hub. A 128-slot process publisher has short
deadlines and a separate two-connection Redis pool; clients have 16 pending
messages and disconnect on overflow.
UI publication is best effort and cannot roll back business success.

Use [Gorilla WebSocket v1.5.3](https://github.com/gorilla/websocket/releases/tag/v1.5.3)
as the single small WS dependency. It supplies explicit limits/deadlines,
ping/pong, frame validation and documented reader/writer concurrency. The hub
owns admission, backpressure and joined cleanup; no realtime framework.

React/TypeScript uses native fetch/WS and hash routes, not a global entity store
or event-ordered FSM. Initial navigation, hints, reconnect and a 30s repair
interval fetch visible REST resources. Discard stale responses, expose degradation
and refetch after authoritative REST control results. Vite provides a local proxy.

## Consequences

Pub/Sub is transient, at-most-once-ish with no replay/ack guarantee. Commit-to-publish
crashes, dropped bursts and reordered/duplicate hints converge through snapshots
while REST is available. Convergence can take 30s or longer during failures;
there is no latency/availability SLA. No SQL migration, event log, metrics,
authorization subsystem or deployment framework. Existing scheduling, leases,
retry/cancel/redrive and occurrence uniqueness retain their authority boundaries.

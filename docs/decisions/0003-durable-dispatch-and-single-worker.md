# 0003 — Durable dispatch and single worker

Status: Accepted (Phase 2). Supersedes only ADR 0002's Create transaction decision.

## Context

A Job commit followed by Redis publication can lose delivery when the second
write fails. A stream ACK and a database commit are likewise different events.
Phase 2 needs a testable single-worker SLEEP path and at-least-once delivery,
while multiple workers, leases/recovery and business retries are later scopes.

## Decision

Add job_dispatch with one row per Job. Create commits canonical Job + intent in
one PostgreSQL transaction. Migration 000003 backfills existing QUEUED Jobs and
has a matching down file. Historical migrations remain unchanged.

One dispatcher goroutine in the worker process reads at most 100 intents per
one-second cycle. It publishes before marking published_at. Publication and
marker failure may produce duplicates. Marked intents for still-QUEUED Jobs
become eligible again after 30 seconds, covering Redis data loss and abandoned
pre-claim delivery; RUNNING is never reclaimed. Completed intent/history is
retained. Redis I/O is never performed inside a database transaction.

Use Redis Streams consumer group, start at 0, COUNT 1, BLOCK 500 ms, and explicit
XACK. Payload is only `v=1` + canonical Job UUID. ACK follows terminal commit;
malformed/missing/non-QUEUED messages are ACKed without execution. Streams keep
unacknowledged deliveries in the pending entries list, as described by
[Redis XREADGROUP](https://redis.io/docs/latest/commands/xreadgroup/).
No XCLAIM/XAUTOCLAIM or stream trimming is introduced.

PostgreSQL conditional UPDATE claims QUEUED -> RUNNING and inserts one attempt
in the same transaction. Finalization requires RUNNING, worker UUID and attempt
number and commits both records together. Domain graph checks constrain the
implemented transitions. Duplicate claims cannot create extra attempts.

Execute only SLEEP, using one exact integer duration_ms field (0..10000 ms) and
a context-aware timer. Unsupported type and invalid payload deterministically
fail after claim. Graceful shutdown persists FAILED/execution_cancelled in a
five-second independent context if storage is available. Pre-claim failure is
retried locally with a 250 ms wait; post-claim finalize/ACK failure stops the
worker. Database calls are bounded to five seconds, Redis writes to three,
and blocking receives to two seconds.

Compose enables a Redis AOF volume, everysec and noeviction. AOF everysec can
still lose about a second, per [Redis persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/).
Queued notification reconciliation is therefore needed even with AOF.

## Failure windows and consequences

| Window | Durable outcome |
|---|---|
| Job insert or intent insert fails | Transaction rollback; neither committed |
| Commit succeeds, Redis unavailable | Job + pending intent retained |
| Publish succeeds, marker fails | Intent is republished; duplicate permitted |
| Redis loses stream / pre-claim consumer crashes | Still-QUEUED intent is republished |
| Claim or attempt insertion fails | Both roll back; no execution or ACK |
| Post-claim process crash / ambiguous claim response | May remain RUNNING indefinitely |
| Finalize or attempt update fails | Both terminal writes roll back; RUNNING + pending; worker stops |
| Terminal commit succeeds, ACK fails | Terminal state retained; pending delivery may remain |
| Graceful shutdown while sleeping | FAILED/execution_cancelled + ACK if bounded cleanup succeeds |

Delivery is at-least-once under eventual dependency availability and continued
dispatcher operation, with explicit RUNNING crash limitations. This provides
no exactly-once business-side-effect guarantee. Streams, consumer metadata and
outbox history grow without cleanup; there is no performance claim or benchmark.
The bounded scan uses simple query/index choices without planner-performance claims.

Phase 3 can extend the tested conditional claim boundary to multiple workers and
bounded concurrency. Phase 4 must define lease/reclaim/fencing policy and pending
consumer handling before recovering RUNNING work. Phase 5 defines retry policy
and submission/business idempotency. None is implemented in Phase 2.

## Alternatives

- Direct INSERT then XADD: loses the dispatch obligation across crashes.
- Memory pending buffer: loses intent across process restarts.
- Redis list with destructive pop: adds a loss window before claim.
- Pure pending-only outbox plus AOF: leaves queued work stranded after queue loss.
- Holding PostgreSQL transactions during Redis/SLEEP: consumes connections and
  still cannot atomically commit both systems.
- Generic event bus/workflow engine: unnecessary for one dispatch use case.

## Validation

Real isolated PostgreSQL + random-key Redis tests cover atomic creation,
publication/marker failure, queue loss, duplicates, concurrent conditional
claims, owner/attempt guards, terminal persistence, poison messages, graceful
shutdown and migration backfill/up/down. Unit tests cover wait/error boundaries,
SLEEP input/cancellation and sanitized logs. See the Phase 2 completion report.

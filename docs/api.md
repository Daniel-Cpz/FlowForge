# API contract

Detailed v1.0.0 submission, delivery, control and pagination contracts, moved
from the root README. [Portfolio overview](../README.md) |
[Lifecycle](job-lifecycle.md) | [Dashboard and realtime contracts](dashboard.md).

## Submission and endpoint reference

Phase 1 tightens the creation envelope and replaces the early offset contract
with cursor pagination. No external consumer or stable offset compatibility
commitment was found. `offset` now returns 400.

| Method | Path | Behavior |
|---|---|---|
| GET | `/health` | Process alive, `200 {"status":"ok"}`; independent of dependencies |
| GET | `/ready` | PostgreSQL, Redis and jobs/outbox/worker registry schema ready: 200; otherwise 503 |
| POST | `/api/v1/jobs` | First create/no key 201; keyed replay 200; key conflict 409; Location identifies original Job |
| GET | `/api/v1/jobs/{id}` | Job by UUID; 404 if absent, 400 if malformed |
| GET | `/api/v1/jobs?limit=20&cursor=...` | Exclusive cursor pagination by `(created_at DESC, id DESC)` |
| POST | `/api/v1/jobs/{id}/cancel` | Durable user cancellation; 200 idempotent CANCELLED, 409 other terminal states |
| GET | `/api/v1/dead-letter?limit=20&cursor=...` | DEAD_LETTER only, same creation-time cursor order |
| GET | `/api/v1/jobs/{id}/attempts` | Complete history ordered by attempt_number (at most 100) |
| POST | `/api/v1/jobs/{id}/retry` | Explicit DEAD_LETTER redrive, +1 budget up to 100, history retained |
| POST | `/api/v1/schedules` | Fixed-interval template; 201 + Location |
| GET | `/api/v1/schedules/{id}` | Schedule by UUID; 404 SCHEDULE_NOT_FOUND |
| POST | `/api/v1/schedules/{id}/cancel` | Idempotent future-occurrence cancellation; existing Jobs continue |
| GET | `/api/v1/dashboard/summary` | One PG statement: job/worker/schedule counts and due QUEUED depth |
| GET | `/api/v1/workers?limit=20&cursor=...` | Bounded immutable worker UUID DESC pages |
| GET | `/api/v1/schedules?limit=20&cursor=...` | Bounded `(created_at DESC, id DESC)` pages |
| GET | `/api/v1/ws` | Bounded transient UI invalidation hints; reconnect requires REST snapshot |
| GET | `/metrics` | Local/internal Prometheus diagnostics when metrics are enabled |

| Create field | Missing | Explicit null | Supplied value |
|---|---|---|---|
| `type` | 400 | 400 | String; trim outer whitespace, retain inner content; 1–128 Unicode characters |
| `payload` | 400 | Accepted as JSON null | Any JSON object, array, string, number or boolean |
| `priority` | Default 0 | 400 | Integer 0–100 |
| `max_attempts` | Default 3 | 400 | Integer 1–100 |
| `timeout` | Default 300 | 400 | Integer 1–86400 **seconds** |
| `idempotency_key` | No key | No key | Nonblank string, at most 255 Unicode characters; supplied whitespace is preserved |
| `scheduled_at` | Immediate | Immediate | RFC3339; UTC microsecond normalization; past is immediately eligible |
| `required_capabilities` | Empty set | Empty set | At most 16 strings; canonical capability set (below) |

Only those exact lowercase field names are accepted. Unknown, wrong-case,
duplicate top-level fields (including equivalent escaped key names), and
server-owned fields return 400. Client-owned `id`, `status`, `result`,
`attempt_count`, worker/lease fields or generated timestamps are not accepted.
Numeric fields require JSON integer notation; strings, fractions and exponent
notation are rejected. Empty/blank type or key values are rejected.

Bodies are capped at 1 MiB before parsing, including requests without a declared
length (413). Empty bodies, invalid UTF-8, malformed JSON and trailing JSON
documents return 400. Unpaired surrogate escapes in metadata are rejected.
PostgreSQL JSONB also rejects Unicode NUL, unpaired surrogates and numbers outside
its numeric range; client payload failures return 400 without driver details.
Nested payload duplicate keys retain JSONB's last-value semantics. Only the
request envelope rejects duplicates; no recursive custom parser is introduced.

`attempt_count` starts at 0. Unset result, worker, lease and execution timestamps
serialize as null at creation. Time strings are RFC3339 with optional fractional seconds in
UTC. A non-null idempotency key is an exact global submission key: same canonical request returns the original Job (200), different request returns 409 `IDEMPOTENCY_CONFLICT`. Null/absent keys always create distinct Jobs. Canonical identity is trimmed type, JSONB-semantic payload, priority, original submission max_attempts, timeout, normalized scheduled_at and canonical required_capabilities; generated ID/time are excluded. Replay works at RUNNING/RETRYING/terminal states and never redispatches.
Priority now governs PostgreSQL Claim; timeout is an attempt execution deadline in seconds. Original submission max_attempts is stored separately from redrive-adjusted execution budget, so keyed replay still compares the original request. Control POST endpoints require no body or query; Attempts accepts no query parameters.
First create returns the QUEUED representation captured in its Job/outbox transaction;
a subsequent GET may already show execution progress. JSONB may normalize
spacing, key order, numeric notation and nested duplicate keys. GET returns the
same canonical payload representation.

## Execution and delivery contract

`SLEEP` is the only executable type (case-sensitive). Its payload is exactly
`{"duration_ms":250}`: one non-null integer field, **0–10000 milliseconds**
inclusive, with no extra fields. Fractions, exponent notation, wrong-case keys,
null, strings and out-of-range values fail execution. The existing Create API
still accepts arbitrary valid JSON and type strings; unsupported types and
invalid SLEEP payloads receive 201 then become DEAD_LETTER after a permanent failure with a static result error
(`unsupported_job_type` or `invalid_sleep_payload`). JSONB's existing last-key
semantics apply before execution. Success stores
`{"duration_ms":250,"outcome":"slept"}`.

Each worker process has **C fixed slots**, default 1, and one dispatcher. Set
`FLOWFORGE_WORKER_CONCURRENCY` to an unsigned decimal integer in **1..32**.
Each free slot receives one delivery, with no unbounded prefetch. Instances use
one UUID each and consumers `<worker_id>:<slot>` in a shared stream/group.
N processes hold at most N*C deliveries and claimed executions. Worker connection
limits are C+4 PostgreSQL and C+4 Redis sockets; budget other clients too.
Job + outbox creation is atomic. Publishing and marking outbox
publication are separate operations, so duplicate messages are expected.
The dispatcher reads at most 100 intents each second; it also republishes
still-QUEUED work after 30 seconds. PostgreSQL therefore reconstructs delivery
after a lost Redis stream or a worker crash before claim. No full payload goes
into Redis. The consumer group starts at `0`, receives one message at a time,
and ACKs after terminal or durable RETRYING persistence (or after rejecting a missing, malformed,
RUNNING or non-QUEUED Job).

An atomic `QUEUED -> RUNNING` claim commits an incremented attempt and lease.
Concurrent duplicate claims create one current owner. Renew and Finalize require
that owner, attempt number and a lease still valid at PostgreSQL time. Finalize
commits Job + Attempt and durable outcome together. `max_attempts` now limits
**total execution attempts**, including crashes and operational interruptions.
Only Claim consumes budget; duplicate delivery/replay/promotion never does.
Failures follow RUNNING -> FAILED -> RETRYING when retryable and budget remains,
or RUNNING -> FAILED -> DEAD_LETTER when permanent/exhausted. Intermediate FAILED
is applied in the transaction, not a required observable persisted state.

RETRYING has `retry_at` and no execution owner/lease. Default equal jitter uses
base=1s, max=30s: cap=min(max,base*2^(attempt-1)), actual delay uniform [cap/2,cap].
Saturating arithmetic prevents overflow. Set FLOWFORGE_RETRY_BASE_SECONDS (1..60)
and FLOWFORGE_RETRY_MAX_SECONDS (base..600). PostgreSQL clock determines schedule
and due time. Bounded concurrent promotion commits RETRYING -> QUEUED and dispatch
intent reset atomically; dispatcher publishes afterward. Schedule survives restart.

Each process registers a fresh UUID and sends bounded heartbeats. Each active
execution renews its lease; one reaper per process scans at most 100 expired
RUNNING Jobs. Recovery closes the old Attempt as FAILED/`lease_expired`, returns
the Job to budgeted RETRYING or DEAD_LETTER in one transaction. A due retry promoter resets dispatch intent; another
worker creates a new attempt. Stale owners cannot renew or overwrite its result.
All authority uses PostgreSQL time. Default lease/renew/heartbeat/offline/reaper
periods are **15/5/2/10/1 seconds**, configurable within validated bounds in
[worker operations](worker-operations.md).

Heartbeat or renewal failure stops the pool; uncertain execution is left for
lease recovery without false success/ACK. A finalize failure also stops the pool;
the database determines whether a commit actually occurred. SIGINT/SIGTERM
interrupts SLEEP and attempts bounded retryable `execution_cancelled` settlement
then ACK, provided its lease is still valid. Expiry wins over late graceful
finalization. Recovery needs a healthy worker/reaper and reachable PostgreSQL;
it is **at-least-once delivery**, with no exactly-once side-effect guarantee.
Old Redis pending entries are retained; recovery publishes a fresh notification.
Crash/cancellation attempts respect the same max_attempts budget; N+1 cannot run.
Submission idempotency != exactly-once execution != exactly-once business side
effect. Lease fencing protects DB writes, not arbitrary external effects.
See [worker operations](worker-operations.md) and
[ADR 0006](decisions/0006-budgeted-retries-and-submission-idempotency.md).

Run `./scripts/phase6-smoke.ps1` in PowerShell for isolated real API/idempotency
and priority/timeout/cancellation/DLQ evidence. It builds a separate image and uses a generated
DB/key/API port; existing development services/data remain intact. Phase 3/4/5 smoke
scripts are historical fixtures for their tagged checkpoints, whose FAILED and
immediate-requeue contracts Phase 6 supersedes.

## Pagination and errors

List accepts only `limit` (default 20, range 1–100) and optional `cursor`.
Unknown/repeated query parameters, invalid limits and malformed query encoding
return 400. Its response is:

```json
{"jobs": [], "next_cursor": null}
```

When another page exists, `next_cursor` is a string representing the **last
returned row**. Pass it unchanged to the next request:

```sh
curl 'http://localhost:8080/api/v1/jobs?limit=2'
# Copy next_cursor from the response:
curl 'http://localhost:8080/api/v1/jobs?limit=2&cursor=RETURNED_CURSOR'
```

The versioned, URL-safe cursor is bounded to 512 characters and validated; invalid
cursors return 400. Final/empty pages return null, and empty jobs is always `[]`.
Newer jobs inserted between pages do not shift the traversal of older rows.
This is not a database snapshot: later inserts behind the boundary may appear.
Cursors are unsigned boundaries, not authorization tokens. A structurally valid
constructed cursor is accepted; clients must treat its encoding as opaque.

Errors use `{"error":{"code":"JOB_NOT_FOUND","message":"job not found"}}`.
Codes distinguish `INVALID_JSON`, `INVALID_INPUT`, `INVALID_ID`, `INVALID_CURSOR`,
`BODY_TOO_LARGE`, `JOB_NOT_FOUND`, `IDEMPOTENCY_CONFLICT`, `JOB_CONTROL_CONFLICT` and `INTERNAL_ERROR`. Dependency failures,
unexpected constraint failures and corrupt stored data return a generic 500;
raw driver errors and secrets are not sent to clients. Readiness failures return
a generic 503.

## Priority, execution timeout and user control

Priority eligibility also includes the time/capability fences in [Scheduling](#scheduling-contract).

Priority is non-preemptive: pending publication and Claim rank eligible QUEUED Jobs
by priority DESC, created_at ASC, id ASC. A short transaction advisory lock serializes
Claim decisions; a conditional SQL check rejects a lower-ranked delivery without
an Attempt or budget change. Three 250ms waits permit peer claims to commit;
a still-deferred notification is ACKed and durable QUEUED intent is reconstructed
on the existing 30s cadence. Running Jobs are never preempted. High backlog can
starve lower priority; aging/fairness and strict global FIFO are not implemented.

The Worker starts a context deadline immediately before executor invocation,
excluding queue/retry/backoff time. SLEEP honors it. TIMED_OUT records
execution_timeout; the Job follows RUNNING -> TIMED_OUT -> RETRYING or DEAD_LETTER
under the same total budget. Renewal stops at deadline and joins before fenced
Finalize. A DB error retains RUNNING/pending for recovery, without false ACK.

Cancel QUEUED/RETRYING atomically persists CANCELLED, clears retry_at and removes
intent. Old delivery is harmless. Cancel RUNNING records cancel_requested_at;
GET may still show RUNNING until the current owner observes it at the renewal
interval and cooperatively cancels execution. Finalize locks the same row and
honors committed user intent even if local execution returned late success.
Expired-lease recovery settles requested cancellation as CANCELLED, never retry.
SKIP LOCKED may defer settlement to the next scan. Cancellation creates no Attempt;
user_cancelled differs from retryable process interruption execution_cancelled.
CANCELLED repeated cancel is 200; other terminal states and invalid redrive are
409 JOB_CONTROL_CONFLICT. No authentication/authorization subsystem is added.

DLQ list uses the same bounded creation-time cursor contract as Job list, filtered
to DEAD_LETTER. It is not a snapshot. Attempts inspect returns complete preserved
history and stable error codes. Explicit retry/redrive locks DEAD_LETTER, raises
max_attempts by one (up to 100), retains attempt_count/history, clears terminal
metadata and resets dispatch intent atomically. A concurrent redrive conflicts;
only the next normal Claim creates the new Attempt. Original submission budget
remains immutable for keyed replay. Persistent per-job logs and external business exactly-once effects remain
unimplemented. The Dashboard includes a DLQ view and explicit redrive controls. See [ADR 0007](decisions/0007-priority-timeout-cancellation-dlq.md).

Migration 000006 appends cancellation metadata, original submission budget and
priority/DLQ indexes. It does not bypass 000005 preflight: the retained development
DB still has one legacy duplicate-key group and may remain on Phase 4 until
explicit operator resolution. Acceptance uses isolated compatible DBs. Stop
workers before down; rolling back 000006 loses cancellation intent and original
submission-budget metadata, so it is not an operational recovery mechanism.

## Scheduling contract

Delayed Jobs stay QUEUED until PostgreSQL time reaches scheduled_at, without an
Attempt before due. Worker capabilities are a canonical immutable set per UUID:
trim + ASCII lowercase, 1–32 characters matching `[a-z0-9][a-z0-9._-]*`, at most
16 input items, deduplicate and sort; reject blank/control/non-ASCII tokens.
Set `FLOWFORGE_WORKER_CAPABILITIES=cpu,ffmpeg`. Claim checks the live worker's
superset and ranks only its compatible, due Jobs. No capable worker means backlog;
mismatched notification ACK leaves intent for 30s reconciliation, potentially longer
under repeated mismatch. Scheduled wait is excluded from execution timeout.

POST /api/v1/schedules takes the Job template fields plus integer interval_seconds
(1..604800) and optional RFC3339 next_run_at (null/absent defaults to DB now).
GET /api/v1/schedules/{id} inspects it; POST .../{id}/cancel prevents unmaterialized
future occurrences idempotently. Existing Jobs continue; Job cancel leaves the
parent unchanged. No submission key is accepted for schedule templates.

Each worker maintenance loop materializes at most 100 due ACTIVE templates in
one row-locked transaction: ordinary Job + intent + cursor advancement. The
unique schedule_id/scheduled_for occurrence prevents duplicate materialization.
After downtime, one oldest due occurrence is generated per pass, middle missed
intervals skipped, and next_run_at advances to the first future boundary on the
original interval grid. Recurring execution still uses existing retry/lease/DLQ.
Cron and template edits/pause/resume remain unimplemented. Schedule lists and
management views are implemented in the Dashboard.

See [scheduling contract and API examples](scheduling.md) and
[ADR 0008](decisions/0008-time-capabilities-recurring-schedules.md).
Run `./scripts/phase7-smoke.ps1` for isolated delayed/capability/recurring process
acceptance and retained-data audit. Migration 000007 is append-only. Stop
processes before up/down; down removes scheduling/capability/attribution metadata
while keeping Jobs/Attempts. Retained development data remains schema 4 with one
legacy duplicate-key group, pending separately authorized repair; it is not
upgraded by schema-7 acceptance.

## Dashboard and realtime contracts

The [Dashboard contract](dashboard.md) specifies summary counts, Worker/Schedule
pagination, bounded WebSocket hints and REST repair. Its later implementation
supersedes the historical Phase 7 exclusion of schedule lists and management UI.

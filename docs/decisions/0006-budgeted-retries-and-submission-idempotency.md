# 0006 — Budgeted retries and submission idempotency

Status: Accepted in Phase 5. Supersedes Phase 4's immediate crash requeue and
temporary over-budget allowance, and earlier reserved-key semantics. Historic
ADRs/reports/migrations remain unchanged.

## Context

Lease fencing and durable notification reconstruction already protect database
execution authority. Failures still need one total Attempt budget, durable delays
and explicit retry classification; repeated POST previously created multiple Jobs.
The project has no tenants and only a bounded SLEEP production executor. Correctness
requires safe concurrency and failure recovery without adding a separate scheduler.

## Decision

max_attempts counts every successfully claimed execution, including lease_expired
and graceful execution_cancelled. Claim alone consumes budget; it conditionally
requires count<max. Failed pre-claim, duplicate delivery, replay and promotion never
create Attempts. The database also enforces count<=max. History is immutable.

Executor outcomes explicitly carry permanent/retryable classification and a bounded
stable application code. Sleep's unsupported/invalid input is permanent; process
cancellation is retryable. No raw driver/error string controls policy. A controlled
test executor validates transient failure without adding a production test Job type.

Job failure follows RUNNING -> FAILED -> RETRYING or DEAD_LETTER, applying the
domain graph sequentially inside one transaction. The final status is visible
atomically with the exact failed Attempt. Permanent/exhausted ends DEAD_LETTER;
remaining retryable budget sets retry_at and clears execution authority. Success
and dead letter clear lease/schedule; Attempt retains old worker/error/result.
ACK occurs after committed terminal/retry outcome. Failure never fabricates ACK.

Equal jitter is selected: cap=min(max,base*2^(attempt-1)), delay uniform [cap/2,cap].
Default base/max=1/30s, validated base=1..60s and max=base..600s. Saturating doubling
handles even maximum integer attempt input in bounded work. A concurrency-safe
rand/v2 source chooses production jitter; deterministic injectable sources validate
both inclusive endpoints and overflow. Minimum positive delay avoids tight loops.

PostgreSQL clock_timestamp sets retry_at and judges due time. One existing
maintenance loop per process recovers <=100 expired executions then promotes
<=100 due RETRYING rows and detects offline workers. SKIP LOCKED row selection
and conditional writes ensure one effective transition. Promotion applies
RETRYING -> QUEUED plus intent reset/insert in one transaction, creates no Attempt,
and never calls Redis or waits inside the transaction. Dispatcher publishes later.
Schedules survive restarts, outage and queue loss; no local durable timer is required.

Submission keys are global exact non-null strings. A partial unique index arbitrates
the INSERT; DO NOTHING losers read the committed winner in a new READ COMMITTED
statement, lock it for comparison and use PostgreSQL JSONB equality. Identity is
trimmed type, payload, priority, max_attempts and timeout. ID/time are excluded.
First create commits Job+intent (201); equal request replays original Job/Location
(200) at every status; different identity returns stable 409 without mutation.
CreateDisposition explicitly carries created/replayed. Null keys always create.
No replay changes publication markers or creates Attempt/intent. No expiry/tenant
scope is inferred; callers share one namespace for as long as keys are retained.

Migration 000005 locks jobs, preflights duplicates and atomically fails with a clear
operator diagnostic. It never deletes/merges or arbitrarily picks a winner.
Legacy count>100 fails because the supported input ceiling is 100. Over-budget
<=100 rows raise max to exactly actual count, preserving history and allowing no
extra execution. Already-exhausted QUEUED rows normalize administratively to
DEAD_LETTER; this schema-upgrade repair is explicitly separate from a runtime
claim/graph transition. Historical RETRYING rows receive a DB-time one-second
schedule. Old FAILED rows remain unchanged. Down removes new objects but does
not erase Attempts/counters or resurrect normalized outcomes. Operator must stop
old processes first; mixed versions are unsupported. Normalized legacy max is
the canonical field used on subsequent replay, and may differ from original input.

## Consequences

Submission idempotency != exactly-once execution != exactly-once business side
effect. Arbitrary effects can repeat across retries/crashes; leases fence DB writes
only. Future executors need business-specific deduplication/fencing.

Every process may scan, but batch size/context/interval bound each operation.
Corrupt Attempt/failed intent rolls back its batch; healthy dependencies/processes
and consistent history are required for progress. No guaranteed retry-time SLA,
throughput benchmark, priority scheduling, generic timeout, user cancel or DLQ
management is introduced. Old pending Streams entries and key/intent/history have
no retention policy. More durable retry does not solve admission control.

The existing local development DB has legacy duplicate keys, detected read-only
before this phase's smoke. Its Phase 4 services/data are deliberately preserved.
Phase 5 acceptance uses isolated schemas/generated DB/key/processes; upgrading
that existing DB requires explicit operator resolution, not automatic data cleanup.

## Alternatives

Local timers lose schedules on restart and cannot arbitrate processes. Immediate
requeue has no storm-safe delay. Full jitter permits near-zero delay; equal jitter
preserves a positive floor and avoids an extra minimum-delay parameter. A separate
scheduler process is unnecessary while the existing bounded maintenance loop fits.

SELECT-then-INSERT races concurrent API calls. Client-only dedup cannot recover a
lost successful response. String/float64 JSON comparison mishandles object order
and large numbers. Unique-index INSERT arbitration plus database semantic equality
uses the authority already present. Automatic historical dedup would destroy or
arbitrarily select real requests, so migration refuses rather than guessing.

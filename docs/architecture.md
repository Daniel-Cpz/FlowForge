# Architecture

Status: Phases 0–5 implemented. PostgreSQL is authoritative; Redis transports notifications.

## Boundaries

A modular monolith supplies separate API and worker processes. `internal/app`
wires config, logging, infrastructure, services and transport. HTTP depends on
Job service and its domain-owned Repository interface. Worker/dispatcher services
define narrow infrastructure interfaces. Domain does not import SQL, Redis or HTTP.
`internal/retry` provides pure equal-jitter policy; the repository owns durable scheduling.

Each process has C fixed slots (1..32), one dispatcher, one heartbeat and one
maintenance loop, with at most C renewers. PostgreSQL/Redis connection limits
are C+4 each for workers and 10 each for API. No unbounded prefetch or transaction
across SLEEP/Redis I/O exists. Consumers identify process UUID and slot.

## Submission and idempotency

Create uses `INSERT ... ON CONFLICT (idempotency_key) WHERE ... DO NOTHING`.
The global exact non-null key unique index arbitrates concurrent writers.
The first writer inserts Job and dispatch intent in one transaction (201).
A conflicting insert reads the committed winner using a new READ COMMITTED
statement, locks it for comparison, and compares type, JSONB payload, priority,
max_attempts and timeout in PostgreSQL. Equal canonical input returns the original
Job (200), including its current execution state. Different input returns 409
IDEMPOTENCY_CONFLICT without mutation. Null keys always create independent Jobs.
CreateDisposition explicitly distinguishes creation and replay; times/IDs are not
used to guess disposition. Commit uncertainty is retried using the same key.

Keys have no retention policy and no tenant scope. All callers share one API
namespace. Exact whitespace/case remain significant. `type` is trimmed by the
service; JSONB equality preserves arbitrary-precision numbers and ignores object
order/formatting. Generated ID and creation time are outside request identity.

## Dispatch and execution

Create commits Job + outbox before responding. Dispatch reads <=100 QUEUED intents
per one-second cycle, publishes version/Job ID, then marks publication separately.
Duplicate messages are expected. Still-QUEUED publication older than 30 seconds is
reconstructible after lost Redis data/pre-claim delivery. Messages never carry payload.

Claim requires QUEUED, fresh ONLINE/IDLE/BUSY worker registration, and
attempt_count < max_attempts. A transaction sets owner, DB-time lease/start,
increments count and creates exactly one Attempt. Duplicate rejection consumes no
budget. Owner, attempt number and unexpired DB-time lease fence Renew/Finalize.
Fresh worker UUID is required after restart; OFFLINE identities never revive.

## Failure and durable retry

Executor outcomes classify failure explicitly as permanent or retryable with a
bounded stable application code. Unsupported type/invalid SLEEP are permanent.
SIGTERM execution_cancelled and lease_expired are retryable operational failures;
all already-consumed attempts count toward the same total budget.

Finalize locks/rechecks current authority, applies domain transitions in order,
and commits Attempt FAILED plus Job RETRYING or DEAD_LETTER atomically:
RUNNING -> FAILED -> RETRYING, when retryable and budget remains;
RUNNING -> FAILED -> DEAD_LETTER otherwise. Intermediate FAILED is a graph step
inside the transaction. Success commits SUCCEEDED. ACK follows committed outcome;
a scheduling failure leaves RUNNING/pending and fails the process safely.

RETRYING has retry_at but no owner/lease. Delay uses equal jitter:
cap=min(max,base*2^(attempt_number-1)); delay uniform [cap/2,cap]. Saturating
arithmetic prevents overflow. Default base/max=1/30 seconds; the production random
source is concurrency safe, and tests inject deterministic sources. PostgreSQL
clock_timestamp()+delay sets retry_at; host clocks never decide eligibility.

Maintenance scans <=100 expired RUNNING rows, closing exact old Attempts as
FAILED/lease_expired through this same policy. It then scans <=100 due RETRYING
rows with FOR UPDATE SKIP LOCKED. Promotion applies RETRYING -> QUEUED, clears
retry_at and resets/inserts dispatch intent in one transaction. It creates no
Attempt and performs no Redis call. Concurrent reapers/promoters have one effective
transition. Batch corruption or intent failure rolls back the bounded transaction.

## Process lifecycle and dependency failures

Lease/renew/heartbeat/offline/maintenance defaults remain 15/5/2/10/1 seconds.
All authority uses database time. Independent heartbeat avoids maintenance scans
blocking process liveness. Scan errors retry at the maintenance interval;
heartbeat/renew/post-claim failures or panic drain all slots and exit nonzero.
An uncertain renewal cancels local execution without fabricated Finalize/ACK;
expired lease recovery closes the old Attempt. Old pending entries remain retained.

SIGINT/SIGTERM cancels execution/dispatch/heartbeat/maintenance, attempts bounded
execution_cancelled settlement and ACK within a shared five-second cleanup
cancellation window, joins renewers/all goroutines, then stops registry. Rollback
has its own five-second budget; registry stop has three seconds. Compose grants
15 seconds. This is not a universal wall-clock SLA. Graceful settlement still
requires an unexpired lease; expiry wins over late completion.

DB startup must succeed within ten seconds. Readiness checks PostgreSQL, Redis
and jobs/outbox/worker columns (including retry_at) within two seconds. CRUD and
Claim/Finalize are bounded to five seconds; recovery/promotion/offline operations
are bounded to three seconds. Raw driver errors, panic values and payloads are
never logged or returned. Redis failure retains durable intent; DB failure never
fabricates scheduling or completion.

## Migration and operational limits

Append-only 000005 adds retry schedule consistency, attempt budget, due partial
index and non-null key uniqueness. It locks jobs to serialize preflight with
writes and atomically rejects historical duplicate keys. No survivor is chosen.
Legacy count >100 rejects migration. Counts above the old budget but <=100 raise
the budget to exactly the recorded count, preserving all Attempts; already
exhausted QUEUED records normalize to DEAD_LETTER, never an extra execution.
Legacy RETRYING receives a DB-time one-second schedule. Down removes new schema
objects, preserving counters/history and normalized terminal outcomes.
Stop old API/workers before production-like upgrades; mixed binaries are unsupported.

Submission idempotency != exactly-once execution != exactly-once business side
effect. External effects can repeat if execution happens before a failed DB commit.
Leases fence DB writes only; future executors need business-specific dedup/fencing.

Local development only: no authentication, tenant isolation, Redis TLS, admission
control, generic timeout enforcement, user cancellation, priority scheduling,
DLQ management, metrics/tracing, benchmarks or deployment automation. Stream,
consumer, outbox and key retention are unbounded. SLEEP remains the only production
executor. See ADRs 0003–0006, lifecycle, worker operations and independent reports.

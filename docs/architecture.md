# Architecture

Status: Phases 0–8 implemented. PostgreSQL is authoritative; Redis transports notifications.

## Boundaries

A modular monolith supplies separate API and worker processes. `internal/app`
wires config, logging, infrastructure, services and transport. HTTP depends on
Job service and its domain-owned Repository interface. Worker/dispatcher services
define narrow infrastructure interfaces. Domain does not import SQL, Redis or HTTP.
`internal/retry` provides pure equal-jitter policy; the repository owns durable scheduling.

Each process has C fixed slots (1..32), one dispatcher, one heartbeat and one
maintenance loop, with at most C renewers. PostgreSQL/Redis connection limits
are C+4 each for business worker traffic and 10 each for API. UI Pub/Sub uses a
separate two-connection Redis pool/process, leaving business slots independent.
No unbounded prefetch or transaction
across SLEEP/Redis I/O exists. Consumers identify process UUID and slot.

## Submission and idempotency

Create uses `INSERT ... ON CONFLICT (idempotency_key) WHERE ... DO NOTHING`.
The global exact non-null key unique index arbitrates concurrent writers.
The first writer inserts Job and dispatch intent in one transaction (201).
A conflicting insert reads the committed winner using a new READ COMMITTED
statement, locks it for comparison, and compares type, JSONB payload, priority,
original submission_max_attempts, timeout, normalized scheduled_at and canonical required_capabilities in PostgreSQL. Equal canonical input returns the original
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
and jobs/outbox/worker columns (including retry_at/cancel_requested_at/submission_max_attempts) within two seconds. CRUD and
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
control, priority aging, cron/editable schedules, persistent per-job logs,
metrics/tracing, benchmarks or deployment automation. Stream,
consumer, outbox and key retention are unbounded. SLEEP remains the only production
executor. See ADRs 0003–0008, lifecycle, worker operations and independent reports.

## Phase 6 scheduling and control

PendingDispatch ranks priority DESC / created_at ASC / id ASC. Claim uses a short
transaction-scoped advisory lock and rechecks ordered eligibility in PostgreSQL;
Redis backlog cannot grant lower-ranked work authority. No lock spans execution.
Same-priority tie ordering is deterministic at Claim snapshots. Running work is
non-preemptive. Deferred notification retries at most three 250ms waits then ACKs;
QUEUED durable reconciliation provides subsequent notification at 30s. Aging and
strict global FIFO are not provided. Short scheduling serialization trades peak
Claim throughput for an explainable authority boundary.

Each executor context has Job.timeout seconds from invocation; queue/retry time
is excluded. TIMED_OUT Attempt settles via RUNNING -> TIMED_OUT -> RETRYING or
DEAD_LETTER, with execution_timeout and unchanged retry policy/budget. Context-aware
SLEEP and renewers stop/join at deadline. Uncooperative executors are unsupported;
there is no mechanism to forcibly stop arbitrary external side effects.

ControlRepository owns Cancel/Redrive/ListDeadLetter/Attempts operations separately
from the ordinary transition graph. Cancel QUEUED/RETRYING locks and terminates
without extra Attempt and removes intent. RUNNING records durable user intent;
Renew returns a distinguished cancellation signal. Fenced Finalize locks that row
and honors cancellation before any success/timeout. Recovery honors the same intent
at expired lease, including crash before cooperative settlement. SKIP LOCKED can
require the next maintenance scan. User cancellation never schedules retry.

Manual redrive is an explicit graph exception: DEAD_LETTER remains terminal for
ordinary transitions. It locks, grants +1 max_attempts (<=100), clears terminal
metadata and resets intent in one transaction. Count/history are retained; next
Claim allocates the next sequence. Two redrives cannot both grant budget. Original
submission_max_attempts is persisted separately, preserving Phase 5 key identity.

000006 appends cancel_requested_at, original submission budget, state consistency
and partial priority/DLQ indexes. DLQ pagination deliberately uses existing
creation-time cursors, not a new completion-time encoding; Attempts history is
bounded by the global 100-attempt budget. Error codes are sanitized; there is no
persistent application-log subsystem or authorization layer. Operator
resolution of retained legacy duplicate keys remains required before 000005+.
## Phase 7 time, capabilities and recurring coordination

PostgreSQL PendingDispatch filters null/due scheduled_at. Claim compares priority
only within the live worker's due, compatible QUEUED candidate set; requirements
must be contained in immutable registry capabilities. Canonical arrays have DB
checks; registration/heartbeat cannot mutate an identity's set. No capable worker
means durable backlog. Global stream mismatch consumes no Attempt and ACKs only
the notification; ordinary 30s intent reconciliation enables later capable work.

Every worker's maintenance loop materializes at most 100 due ACTIVE templates
with FOR UPDATE SKIP LOCKED. Job, intent, unique (schedule_id,scheduled_for) and
next_run_at advancement share a transaction. Before-commit crash rolls all back;
after-commit restart sees the cursor. Redis failure retains intent. Missed runs
coalesce to one oldest due occurrence, skipping middle intervals and preserving
the interval grid. Schedule Cancel locks the same row and leaves existing Jobs
untouched. Retry/redrive retains requirement and occurrence identity.

Append-only 000007 keeps historical migrations. Stop processes before up/down;
down removes templates and scheduling/capability attribution metadata while
retaining Jobs/Attempts. Retained development schema remains 4 with its legacy
key conflict. See [contract](scheduling.md) and [ADR 0008](decisions/0008-time-capabilities-recurring-schedules.md).

## Phase 8 Dashboard and transient fanout

Dashboard reads have a domain read-model interface; PG supplies a single-statement
summary and bounded lists, the service applies lookahead bounds, HTTP encodes
cursors. Worker pages use immutable UUID DESC so heartbeat changes cannot move
boundaries; Schedules use creation time/UUID DESC. Pages are live, not frozen.

Composition configures a nonblocking observer before concurrent repository use.
Create, Claim, Finalize, cancel/redrive, retry promotion/recovery, schedule
create/cancel/materialization and Worker lifecycle changes enqueue hints after
commit. Heartbeat captures previous status/count in its atomic update; unchanged
heartbeats emit nothing. Lease renewal is repaired by low-frequency REST refresh.
There is no Redis call under a business transaction/row lock.

Each process has one 128-slot UI queue/publisher with 250ms Redis deadlines.
Overflow/failure drops hints with sanitized warnings. Each API subscribes to the
separate `<stream>:ui:v1` channel and owns a 100-connection hub: 16 messages/client,
1 KiB event/read limits, 2s writes, 15s ping / 45s pong. Saturation disconnects
slow peers; subscriber recovery emits a resync hint. Context cleanup closes the
subscription socket, closes hijacked WS connections and joins all associated loops.

React uses native fetch/WS and hash navigation. Hints refresh visible REST data,
coalescing at 250ms; stale requests are aborted/discarded. Reconnect and a 30s
repair interval restore snapshots after lost/duplicate/out-of-order hints.
Controls show real REST results and refetch. Escaped collapsed JSON renders at
most 16 KiB per field. See [contract](dashboard.md) and [ADR 0009](decisions/0009-dashboard-realtime-resync.md).

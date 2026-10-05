# Job lifecycle

Status: state validation, execution, leases and expiry recovery implemented.

| From | Allowed targets |
|---|---|
| QUEUED | RUNNING, CANCELLED |
| RUNNING | SUCCEEDED, FAILED, TIMED_OUT, CANCELLED |
| FAILED | RETRYING, DEAD_LETTER |
| RETRYING | QUEUED |
| SUCCEEDED, DEAD_LETTER, CANCELLED, TIMED_OUT | none |

Unknown states, self-transitions and every edge absent from the table are
rejected. `Job.Transition` returns `ErrInvalidTransition` and leaves the job
unchanged. Tests cover the full cross product, including unknown/empty values.
The method validates the state graph only; execution timestamps, attempts,
ownership and durable compare-and-set transitions are coordinated by the
repository. Phase 4 adds a separate expiry-guarded RecoverExpired operation for
RUNNING -> QUEUED; this does not expand ordinary transitions or business retry.
No HTTP state-change endpoint exists.

Creation initializes QUEUED, attempt_count 0 and UTC created_at. Payload and
result use JSON; IDs are UUID; nullable metadata uses pointers or nil RawMessage.
All PostgreSQL times are TIMESTAMPTZ and outbound times use UTC RFC3339 strings.
Timeout is an integer number of seconds, reserved for a later execution policy.

## Job is not an attempt

A job is the durable logical request. An attempt is one execution of it by a
worker. The attempt table reserves a unique `(job_id, attempt_number)` and
records status, worker, start/end times, result and error. Its states are the
execution subset RUNNING, SUCCEEDED, FAILED, TIMED_OUT and CANCELLED.

Implemented crash example: attempt 1 on A expires, is closed FAILED/lease_expired,
then attempt 2 on B succeeds. Business FAILED retry remains planned.
Phase 2 inserts one RUNNING attempt during atomic claim and
updates it with the Job's terminal status, finish time and result in one
transaction. Duplicate rejection before claim creates no attempt. Failure codes
are recorded in result JSON; expiry recovery additionally sets attempt.error
to lease_expired. There is no business retry loop. Worker IDs have no foreign key
so existing pre-registry Attempt history remains valid. Phase 3 keeps one UUID
per process across all C slots; restart generates a new UUID. A slot/Redis
consumer is not the durable worker registration. Phase 4 persists process liveness
separately in workers; stale/OFFLINE identity cannot be reused.

## Database constraints

Job status membership is constrained; SQL itself does not enforce transitions.
Priority is 0–100, max_attempts 1–100, timeout 1–86400, attempt_count nonnegative.
Attempts require positive sequence numbers and an existing job. End times cannot
precede known start times. The list index is `(created_at DESC, id DESC)`;
attempt uniqueness also indexes lookups by job_id. Phase 4 adds partial indexes
on RUNNING lease_expiry and non-OFFLINE last_heartbeat for bounded recovery scans.

Migrations 000001 and 000002 define jobs and job_attempts respectively; matching
down files drop them in reverse order. The runner maintains schema_migrations.
Version 000003 adds job_dispatch and backfills existing QUEUED Jobs; down drops
only that table. Historical SQL files are unchanged.
Version 000004 adds workers/indexes and backfills null RUNNING leases to DB now;
down drops only its registry/indexes. Job/Attempt history is preserved, but
downgrade binaries cannot enforce lease semantics. Stop workers before rollback.

## Executed transitions and ACK boundaries

QUEUED creation commits with durable dispatch intent. Claim atomically checks
QUEUED, a fresh live registry identity and integer sequence capacity, assigns the
process UUID, sets DB-time started_at/lease_expiry, increments attempt_count and
inserts the matching attempt. Crash recovery can exceed stored max_attempts;
Phase 5 will define combined retry accounting, and business FAILED is not retried.
Finalization checks RUNNING + assigned_worker + attempt number + valid DB lease. Both repository
operations use the domain graph; no SQL trigger duplicates transition policy.

SLEEP uses exactly one duration_ms integer, 0..10000 inclusive. It runs a
context-aware timer. Invalid payload/unsupported type becomes FAILED after one
claim with a static result error; normal completion becomes SUCCEEDED. General
timeout, priority, retry and user-cancellation policies are still planned.

Terminal persistence precedes XACK. Malformed, missing, RUNNING and terminal
duplicates are ACKed without execution. Graceful process cancellation writes
FAILED/execution_cancelled using bounded cleanup before ACK. A finalize failure
rolls back both records, leaves RUNNING + pending, and stops the worker. A failed
ACK after terminal commit leaves a terminal Job and possible pending message.

Phase 3 preserves these boundaries across processes. A fatal slot error stops
all peers and the process dispatcher; business failures do not stop the pool.
Graceful cleanup runs concurrently against a shared deadline. In-flight Receive
and Claim can race with cancellation: unclaimed deliveries remain unresolved,
and a successful late claim attempts FAILED persistence. Executor panic leaves
the winning Job RUNNING/pending and safely stops the process.

## Expiry recovery

An abrupt post-claim crash or failed renewal leaves RUNNING until DB lease expiry.
Reapers conditionally lock expired records; one transaction closes the exact old
Attempt with finished_at/result/error=lease_expired, sets Job QUEUED and clears
owner/lease/start/finish/result, then restores job_dispatch.published_at=NULL.
Attempt count stays intact; next Claim creates a higher sequence without
overwriting history. Any failure, missing/corrupt Attempt or intent-write failure
rolls back the batch. No database transaction crosses SLEEP or Redis.

The expired owner cannot renew even before recovery, or finalize after expiry.
After takeover its attempt number/owner no longer match. Graceful shutdown
before expiry records execution_cancelled; expiry races reject late Finalize
and leave recovery evidence authoritative. Renew failure retains the delivery
without guessing completion. Ambiguous commits require reading database truth.
Old Redis pending messages are not reclaimed; requeue restores fresh delivery.
Healthy reapers and reachable dependencies are required for progress. External
side effects may overlap/repeat across leases: no exactly-once guarantee.

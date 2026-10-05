# Job lifecycle

Status: state validation and QUEUED -> RUNNING -> SUCCEEDED/FAILED execution implemented.

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
Phase 2 repository. No HTTP state-change endpoint exists.

Creation initializes QUEUED, attempt_count 0 and UTC created_at. Payload and
result use JSON; IDs are UUID; nullable metadata uses pointers or nil RawMessage.
All PostgreSQL times are TIMESTAMPTZ and outbound times use UTC RFC3339 strings.
Timeout is an integer number of seconds, reserved for a later execution policy.

## Job is not an attempt

A job is the durable logical request. An attempt is one execution of it by a
worker. The attempt table reserves a unique `(job_id, attempt_number)` and
records status, worker, start/end times, result and error. Its states are the
execution subset RUNNING, SUCCEEDED, FAILED, TIMED_OUT and CANCELLED.

Planned example: attempt 1 on A crashes, attempt 2 on B fails, attempt 3 on C
succeeds. The attempt model/schema existed in Phase 0 without execution.
Phase 2 inserts one RUNNING attempt during atomic claim and
updates it with the Job's terminal status, finish time and result in one
transaction. Duplicate rejection before claim creates no attempt. Failure codes
are recorded in result JSON; the reserved attempt.error column remains null.
There is no retry loop. Worker IDs have no foreign key because
worker registration/persistence has not been designed. Phase 3 keeps one UUID
per process across all C slots; restart generates a new UUID. A slot/Redis
consumer is not a durable worker registration.

## Database constraints

Job status membership is constrained; SQL itself does not enforce transitions.
Priority is 0–100, max_attempts 1–100, timeout 1–86400, attempt_count nonnegative.
Attempts require positive sequence numbers and an existing job. End times cannot
precede known start times. The list index is `(created_at DESC, id DESC)`;
attempt uniqueness also indexes lookups by job_id. No speculative queue, lease
or idempotency index is added before its query/semantic requirements exist.

Migrations 000001 and 000002 define jobs and job_attempts respectively; matching
down files drop them in reverse order. The runner maintains schema_migrations.
Version 000003 adds job_dispatch and backfills existing QUEUED Jobs; down drops
only that table. Historical SQL files are unchanged.

## Executed transitions and ACK boundaries

QUEUED creation commits with durable dispatch intent. Claim atomically checks
QUEUED and attempt_count < max_attempts, assigns a per-process UUID worker,
sets started_at and increments attempt_count, and inserts the matching attempt.
Finalization checks RUNNING + assigned_worker + attempt number. Both repository
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

Abrupt post-claim crash or ambiguous claim commit may leave RUNNING indefinitely.
No heartbeat, lease renewal, stale-consumer reclaim, fencing or recovery exists.
Queued-job notification republication never transitions RUNNING back to QUEUED.

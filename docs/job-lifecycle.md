# Job lifecycle

Status: model and state validation implemented; execution is Planned.

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
ownership and durable compare-and-set transitions are later responsibilities.
No HTTP state-change endpoint exists in Phase 0.

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
succeeds. The attempt model/schema exist, but Phase 0 never inserts attempts
during normal application operation. Worker IDs have no foreign key because
worker registration/persistence has not been designed.

## Database constraints

Job status membership is constrained; SQL itself does not enforce transitions.
Priority is 0–100, max_attempts 1–100, timeout 1–86400, attempt_count nonnegative.
Attempts require positive sequence numbers and an existing job. End times cannot
precede known start times. The list index is `(created_at DESC, id DESC)`;
attempt uniqueness also indexes lookups by job_id. No speculative queue, lease
or idempotency index is added before its query/semantic requirements exist.

Migrations 000001 and 000002 define jobs and job_attempts respectively; matching
down files drop them in reverse order. The runner maintains schema_migrations.

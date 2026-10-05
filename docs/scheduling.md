# Scheduling contract — Phase 7

PostgreSQL is the time and capability authority. Redis carries notifications.

## One-shot Jobs and capabilities

POST /api/v1/jobs accepts optional RFC3339 `scheduled_at`. Missing/null is
immediate; past timestamps are accepted. Values normalize to UTC microseconds,
truncating finer precision. A future Job remains QUEUED; PendingDispatch and
Claim require null or due at PostgreSQL clock_timestamp(). Before due there is
no Attempt, budget consumption or execution deadline.

`required_capabilities` is optional string[]. Missing/null/[] is unrestricted.
At most 16 input items, each 1–32 characters after trimming. ASCII lowercase,
token rule `[a-z0-9][a-z0-9._-]*`; reject blank, controls or non-ASCII token
content; remove duplicates and sort. Worker configuration uses the same parser.

The live registry worker set must contain every requirement at Claim. Priority
compares only due and compatible QUEUED Jobs. Incompatible high priority work
cannot block a worker's eligible lower priority backlog. No capable worker means
durable QUEUED backlog without failure. Mismatched notifications ACK without an
Attempt; intent republishes at the existing 30s cadence. Repeated consumer mismatch
can delay matching further; no latency/fairness guarantee is claimed.

Submission identity includes normalized scheduled_at and canonical requirements.
Equal instants with different offsets and reordered/duplicate capabilities replay;
different normalized values conflict. Missing/null time differs from an explicit
past time. RetryAt remains separate; retry/redrive preserve initial eligibility.

## Fixed-interval recurring templates

POST /api/v1/schedules uses flat Job template fields: type, payload, priority,
max_attempts, timeout, required_capabilities. Validation/defaults match Job create.
Required `interval_seconds` is an integer 1..604800 (seven days). Optional
RFC3339 `next_run_at` normalizes like scheduled_at; missing/null defaults to DB now.
Templates accept no scheduled_at/idempotency_key/generated fields.

```sh
curl -i -X POST http://localhost:8080/api/v1/schedules \
  -H 'Content-Type: application/json' \
  -d '{"type":"SLEEP","payload":{"duration_ms":250},"interval_seconds":60,"required_capabilities":["cpu"]}'
curl http://localhost:8080/api/v1/schedules/UUID
curl -X POST http://localhost:8080/api/v1/schedules/UUID/cancel
```

Create returns 201 + Location, GET 200, missing Schedule 404 SCHEDULE_NOT_FOUND,
invalid UUID 400 INVALID_ID. Status is ACTIVE/CANCELLED. Cancel returns 200;
repetition preserves cancellation state and timestamp. Cron, list, editing,
pause/resume and management UI are outside this phase.

Every worker's maintenance loop selects at most 100 due ACTIVE rows FOR UPDATE
SKIP LOCKED. One transaction creates ordinary QUEUED Jobs and dispatch intents,
then advances next_run_at. Paired schedule_id/scheduled_for, foreign key and unique
occurrence identity prevent duplicates. No user key is reused. Generated Jobs use
the existing queue, Claim, Attempt, timeout, retry, lease, cancellation and DLQ.

After downtime, create the oldest due occurrence once per schedule per pass,
then skip middle missed intervals. Advance on the original interval grid to the
first boundary strictly after DB time at advancement. Missed runs are not all
filled in. PG availability and a healthy worker are needed; scan transactions
are bounded to three seconds. Before commit crash: Job, intent and cursor all
roll back. After commit crash: the cursor and unique occurrence prevent replay.
Redis outage preserves the intent. External effects remain at-least-once.

Schedule cancellation locks the same schedule row: cancellation winning the lock
prevents an occurrence; materialization winning first commits that occurrence.
Only unmaterialized future occurrences are prevented. Existing Jobs continue and
retain retry/DLQ/history. Cancelling an occurrence Job leaves the parent ACTIVE;
redrive does not change the parent template or occurrence identity.

## Operations and migration

Set `FLOWFORGE_WORKER_CAPABILITIES=cpu,ffmpeg`; empty declares an empty set.
Registration stores/logs the set, and PostgreSQL prevents mutation for that UUID.
Heartbeat only changes liveness. Restart obtains a new UUID after config changes.

Migration 000007 appends columns/checks, occurrence uniqueness, immutable Worker
set trigger and bounded scan/queued priority indexes. Historical 000001–000006
are unchanged. Stop processes before up/down; mixed binaries are unsupported.
Down retains Jobs/Attempts but removes templates and eligibility/capability/
occurrence attribution metadata. It is not crash recovery. The retained DB has
one legacy duplicate-key group and remains schema 4; do not bypass 000005.

Run `./scripts/phase7-smoke.ps1` in PowerShell with existing PG/Redis and create/drop
DB rights. It uses generated schema-7 DB/key/containers/loopback API port, checks
delayed/capability/two-worker recurring acceptance, removes only those resources,
and compares retained-data audits. The separate image/cache remains outside Git.
See [ADR 0008](decisions/0008-time-capabilities-recurring-schedules.md).

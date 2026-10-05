# Worker operations — Phase 7

Each process has C fixed slots, one dispatcher, heartbeat and maintenance loop,
and at most C execution renewers. Consumers use fresh process UUID + slot;
OFFLINE identity never revives. Worker PostgreSQL/Redis connection budgets are
C+4 each, API budgets 10 each. Multiply by process count and include diagnostics.
No unbounded prefetch or global admission/queue-depth limit exists.

## Startup and configuration

Copy .env.example and set the development password. Stop old API/workers before
migration/schema upgrades; mixed versions are unsupported. On a compatible DB:

```sh
docker compose up --build -d --scale worker=2 migrate api worker
docker compose ps
docker compose logs worker
```

All periods/concurrency use unsigned whole decimal integers. Invalid values,
including empty/signed/fractional/exponent/overflow, fail startup.

| Variable | Default | Bounds |
|---|---:|---|
| FLOWFORGE_WORKER_CONCURRENCY | 1 | 1..32 |
| FLOWFORGE_LEASE_SECONDS | 15 | 3..300s |
| FLOWFORGE_RENEW_SECONDS | 5 | 1..60s; lease > 2*renew |
| FLOWFORGE_HEARTBEAT_SECONDS | 2 | 1..60s |
| FLOWFORGE_OFFLINE_SECONDS | 10 | 3..600s; offline > 2*heartbeat |
| FLOWFORGE_RECOVERY_SECONDS | 1 | 1..30s; maintenance scan interval |
| FLOWFORGE_RETRY_BASE_SECONDS | 1 | 1..60s |
| FLOWFORGE_RETRY_MAX_SECONDS | 30 | base..600s |

Use the same policy across worker processes. Equal jitter cap doubles per failed
Attempt, saturates at max, and uniformly selects [cap/2,cap]. Shorter test periods
exercise correctness; no recovery/throughput SLA or production recommendation is made.
`FLOWFORGE_REDIS_STREAM` defaults to flowforge:jobs:v1; use isolated streams for
isolated workloads. Shared group is flowforge-workers-v1.

## Upgrade preflight

000005 takes an exclusive jobs lock and refuses duplicate non-null exact keys.
It never deletes/merges/selects a winner. Diagnose before an upgrade:

```sql
SELECT idempotency_key,count(*) FROM jobs WHERE idempotency_key IS NOT NULL
GROUP BY idempotency_key HAVING count(*)>1;
SELECT id,attempt_count,max_attempts,status FROM jobs
WHERE attempt_count>max_attempts OR (status='QUEUED' AND attempt_count>=max_attempts);
```

Review historical duplicates explicitly with the operator/application owner,
back up data, and define a resolution before retrying migration. Do not auto-delete
or null keys merely to pass migration. Legacy counts>100 reject migration.
Over-budget <=100 counts raise max_attempts to exactly actual count; exhausted
QUEUED rows normalize to DEAD_LETTER with attempt_budget_exhausted. All Attempt
history remains. This administrative normalization may change legacy request
max_attempts metadata; replay compares the current normalized canonical fields.
Preexisting FAILED outcomes are preserved and not automatically resubmitted.
Stop all old processes, migrate, then start the compatible binaries. Seven down
calls remove scheduling, control, retry schema, registry, dispatch, Attempts and Jobs; never use down
as recovery. Normalized outcomes/counters are not reversed by 000005 down.

## Durable scheduling and inspection

GET Job shows authoritative status/count/retry_at. Inspect job_attempts in sequence
for worker/status/error/result/end time; a failed Attempt can correspond to
RETRYING or DEAD_LETTER. job_dispatch publication is notification evidence only.

Claim creates an Attempt only if count<max_attempts and registry is fresh/live.
Unsupported type/invalid SLEEP are permanent. execution_cancelled and lease_expired
are retryable but consume budget. Remaining budget schedules RETRYING; permanent
or exhausted ends DEAD_LETTER. DLQ and cancellation commands are available as documented below.

Maintenance recovers <=100 expired executions, then promotes <=100 due retries,
then detects <=100 offline workers per cycle. SKIP LOCKED and conditional DB fences
allow concurrent processes. Promotion makes QUEUED plus intent reset atomically;
no immediate publish during failure settlement. PostgreSQL clock controls expiry
and due time; host timers only trigger scans. Retry schedule survives restarts.

Dispatcher scans <=100 each second; unmarked or >30s-old QUEUED intent is eligible.
Redis outage retains intent. A DB promotion/settlement failure never fabricates
success. Historical Attempt corruption rolls back its bounded batch and requires
operator repair. No healthy worker/DB means no recovery progress.

## Lifecycle, logs and shutdown

Registry ONLINE -> IDLE/BUSY -> DRAINING -> OFFLINE. active_jobs is a sampled
informational count, not execution authority. Claim/Finalize use 5s DB bounds;
recovery/promotion/registry operations 3s; heartbeat/renew min(3s,configured interval).
No SLEEP/Redis I/O happens inside DB transactions.

Events include worker_started/registered, slot_started/stopped, job_claimed,
lease_acquired/renewed/expired, job_finished (durable Job status/retry_at/failure class),
recovery_settled (RETRYING/DEAD_LETTER), retry_queued, worker_offline,
worker_draining/stopped, heartbeat_stopped/recovery_stopped and pool_failed.
Failures include dispatch_failed, receive_failed, delivery_failed, heartbeat_failed,
lease_renew_failed, recovery_failed, retry_promotion_failed and offline_scan_failed.
Logs never include raw driver errors, payload, credentials, panic values or stacks.

SIGINT/SIGTERM cancels all loops/executions and attempts execution_cancelled
settlement/ACK within a shared 5s cleanup cancellation window; all goroutines and
renewers join. Rollback has its own 5s limit, registry stop 3s, Compose grants 15s.
No universal 5s wall-clock guarantee is claimed. Expired cleanup is fenced out.

Heartbeat/renew/post-claim settlement or ACK failure/panic drains process and exits
nonzero. Maintenance scan failures warn and retry. Failed settlement leaves
RUNNING/pending for recovery; committed RETRYING/terminal state survives ACK failure.
An uncertain renewal cancels execution without Finalize/ACK. Inspect DB truth after
ambiguous commit. Old pending entries are never silently trimmed/reclaimed.
Stop reason distinguishes graceful_shutdown, fatal_error and heartbeat_expired.

Submission idempotency prevents duplicate logical Jobs only. Execution/external
side effects remain at-least-once; future executors need business dedup/fencing.
Stream, consumers, outbox and key retention are unbounded; no production orchestrator.

## Repeatable acceptance

```powershell
./scripts/phase6-smoke.ps1
```

Requires existing standard development PostgreSQL/Redis and create/drop DB rights.
Builds a separate Phase 6 image, generated disposable DB/Redis namespace, loopback
API port and C=1 process. A long running low-priority SLEEP establishes a claim
barrier. High/low queued jobs remain unclaimed; cancel releases the barrier and
high starts first. SLEEP timeout records two TIMED_OUT Attempts and exact budget
exhaustion. DLQ list/Attempts/retry retain history, grant one budget, and preserve
keyed replay. PASS prints after generated containers/DB/key cleanup. Normal
services/images/data remain intact; the smoke image/cache remains outside Git.
Historical Phase 3/4/5 scripts apply only to their tagged checkpoint contracts.
Integration tests instead use verified random
schemas and stream keys, never application data or FLUSHDB. See Phase 6 report
and ADR 0007 for current evidence and migration limitations.

## Priority, timeout and user control

Claim decisions are short serialized PostgreSQL transactions ranked by priority
DESC / created_at ASC / id ASC. Running work is non-preemptive. Three 250ms peer
waits bound a deferred delivery; remaining deferral ACKs notification only and uses
30s intent reconciliation. High backlog may starve lows; no aging/FIFO SLA.

Job.timeout is seconds from executor invocation, not queue/backoff. Executor must
honor context; SLEEP does. TIMED_OUT / execution_timeout is retryable under the
same budget. Deadline stops renewal; renewer joins before fenced Finalize. DB errors
leave RUNNING/pending and expiry recovery truth. No arbitrary shell/process killer.

POST /api/v1/jobs/{id}/cancel has no body/query. QUEUED/RETRYING ends CANCELLED and
removes intent. RUNNING returns durable cancel_requested_at, still RUNNING until
cooperative observation at the configured renewal interval (default 5s). This is
not a wall-clock SLA: DB latency/availability and scheduling matter. Cancellation
intent wins late completion if committed first; expired owner is fenced out and
recovery honors the request. SKIP LOCKED may require another maintenance pass.
Repeated CANCELLED is 200; other terminal state is 409 JOB_CONTROL_CONFLICT.

GET /api/v1/dead-letter?limit=20&cursor=... filters DEAD_LETTER using existing
creation-time cursor order (descending UUID tie-breaker), with no snapshot promise.
GET /api/v1/jobs/{id}/attempts exposes complete ordered history, at most 100; raw
legacy error text is redacted to execution_error. There is no persistent per-job
log store. POST /api/v1/jobs/{id}/retry is explicit manual redrive: +1 max_attempts
up to 100, unchanged count/history, cleared old terminal fields and reset intent
in one transaction. Invalid/concurrent second redrive conflicts. Original submission
budget remains canonical, so replay does not need the new administrative budget.
No authorization layer/DLQ UI is added by this local development phase.

New events: priority_deferred, cancellation_observed, job_cancel_requested,
job_redriven; recovery_settled may now show CANCELLED. user_cancelled never retries;
execution_cancelled remains operational interruption. Attempt TIMED_OUT retains
execution_timeout separately from Job RETRYING/DEAD_LETTER.

The retained development DB still has one legacy duplicate-key group; no Phase 6
migration or smoke mutates it. Normal services may remain on Phase 4 schema until
explicit operator resolution permits 000005+, then 000006. 000006 down loses durable
cancel intent and original submission budgets: stop workers and evaluate data
before rolling back. It is not crash recovery or a safe mixed-version rollout.
## Phase 7 capabilities and recurring maintenance

Set FLOWFORGE_WORKER_CAPABILITIES=cpu,ffmpeg. Empty declares no capabilities.
Same parser as Job requirements: trim, ASCII lowercase, 1–32 token characters
[a-z0-9][a-z0-9._-]*, at most 16 input items, deduplicate/sort. Reject blanks,
controls and non-ASCII token content. Restart after config changes: registry
capabilities are fixed per UUID and heartbeat cannot alter them. Registration
logs the canonical set. No capable worker means QUEUED backlog, not failure.
Priority compares only each worker's due and compatible Jobs.

Existing maintenance runs MaterializeDue alongside recovery/retry promotion,
bounded to 100 ACTIVE schedules and 3s per transaction. schedule_materialized
logs the count; schedule_scan_failed preserves PG truth for the next scan.
Job+intent+occurrence uniqueness+cursor advancement commit together, before
ordinary dispatch/Claim/Attempt. Fixed intervals skip missed middle runs and
produce one oldest due occurrence per template per pass. Cancel schedule does
not cancel materialized Jobs. PG and a healthy worker are required for progress.

Global-stream capability mismatch is ACKed with zero Attempts; reconciliation
reissues QUEUED intent at 30s, possibly longer under repeated mismatches. No
routing/fairness SLA. Delayed waiting consumes neither budget nor timeout.
./scripts/phase7-smoke.ps1 checks delayed, CPU/GPU and concurrent recurring work
using independent containers, generated DB/key and loopback port. Cleanup and
retained-data audit must PASS. Normal schema-4 services/data are preserved.
Migration 000007 appends to unchanged 000001–000006 and does not bypass the
legacy duplicate-key preflight. Down loses schedule/capability/attribution
metadata, retaining Jobs/Attempts; stop processes before schema changes.
See [contract](scheduling.md) and [ADR 0008](decisions/0008-time-capabilities-recurring-schedules.md).

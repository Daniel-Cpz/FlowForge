# Worker operations — Phase 5

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
Stop all old processes, migrate, then start the compatible binaries. Five down
calls remove retry schema, registry, dispatch, Attempts and Jobs; never use down
as recovery. Normalized outcomes/counters are not reversed by 000005 down.

## Durable scheduling and inspection

GET Job shows authoritative status/count/retry_at. Inspect job_attempts in sequence
for worker/status/error/result/end time; a failed Attempt can correspond to
RETRYING or DEAD_LETTER. job_dispatch publication is notification evidence only.

Claim creates an Attempt only if count<max_attempts and registry is fresh/live.
Unsupported type/invalid SLEEP are permanent. execution_cancelled and lease_expired
are retryable but consume budget. Remaining budget schedules RETRYING; permanent
or exhausted ends DEAD_LETTER. There is no DLQ management/user cancellation API.

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
./scripts/phase5-smoke.ps1
```

Requires existing standard development PostgreSQL/Redis and create/drop DB rights.
Builds a separate Phase 5 image, starts a generated isolated DB/stream, API on an
available loopback port, and separate worker processes. Concurrent keyed POSTs
verify 1x201 + 7x200, same ID/Location, one Job/intent, conflicting request 409,
and distinct no-key Jobs. SIGTERM first SLEEP owner verifies FAILED cancellation
Attempt and durable RETRYING; a fresh process starts before due and creates no
early Attempt, then promotes and succeeds as Attempt 2. The script cleans only
its generated containers/database/key; existing services/data/images stay intact.
PASS prints only after cleanup succeeds. Test image is retained as a build artifact
outside Git. This is correctness evidence, not a benchmark.

Phase 3/4 smoke scripts remain historical fixtures for their own tagged checkpoints;
their FAILED/immediate-requeue assertions are superseded by Phase 5. Do not run
those old scripts against Phase 5 and assume their contracts still apply. Their
reports remain immutable evidence. Integration tests instead use verified random
schemas and stream keys, never application data or FLUSHDB. See Phase 5 report
and ADR 0006 for current evidence and migration limitations.

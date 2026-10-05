# Worker operations — Phase 4

API and worker remain independent. Each worker process has one dispatcher,
heartbeat and recovery loop, C fixed slots, and at most C active renewers.
Set `FLOWFORGE_WORKER_CONCURRENCY` to a decimal
integer in **1..32**, default **1**; empty, signed, fractional, exponent, overflow
and out-of-range input fails startup. This upper bound is a safety limit, not a
recommended performance setting. Host processes need exported variables;
Compose loads `.env`. Each process generates one UUID; all its Attempts share
that owner. Consumers use `<worker_id>:<zero-based slot>`.

## Startup and capacity

The tested dual-process Compose command is:

```sh
# Set FLOWFORGE_WORKER_CONCURRENCY=2 in .env to match the Phase 3 smoke.
docker compose up --build -d --scale worker=2 migrate api worker
docker compose ps
docker compose logs worker
# Return to one worker:
docker compose up -d --scale worker=1 migrate api worker
```

No worker `container_name` or published port prevents scaling. Processes share
PostgreSQL, the configured stream (default `flowforge:jobs:v1`) and group
`flowforge-workers-v1`. Use separate storage/stream namespaces for different
workloads. `FLOWFORGE_REDIS_STREAM` is nonblank, <=256 bytes, no control chars.

N processes have at most N*C active executions and held deliveries; there is no
unbounded prefetch/local queue. Distribution and completion order are not promised.
Worker PostgreSQL maximum is C+4 connections, Redis PoolSize/MaxActiveConns C+4.
At most C Redis reads block; ACK/publication retain spare capacity. API budgets
10 PostgreSQL and 10 Redis connections. Include N*(C+4), API and diagnostic
clients in the server's connection budget before scaling. SLEEP never holds a
SQL transaction/connection. These local bounds do not limit submissions, Redis
history, outbox size or global queue depth.

Migration 000003 backfills existing QUEUED work; unsupported legacy types become
FAILED. Phase 4 appends migration 000004 for workers and expiry indexes;
pre-lease RUNNING records receive expired leases, but need consistent owner/Attempt
history to recover. Stop API/workers before upgrading or rollback; mixed old/new
worker binaries are unsupported. Never
use schema rollback as recovery. PostgreSQL startup failure exits; Redis outage
uses bounded retry loops. No automatic restart policy is configured.

## Lease and heartbeat configuration

All periods are unsigned decimal whole seconds; empty, signed, fractional,
exponent, overflow or out-of-bound input fails startup. These are local-demo
defaults, not measured production recommendations.

| Environment variable | Default | Bounds (seconds) |
|---|---:|---:|
| FLOWFORGE_LEASE_SECONDS | 15 | 3..300 |
| FLOWFORGE_RENEW_SECONDS | 5 | 1..60 |
| FLOWFORGE_HEARTBEAT_SECONDS | 2 | 1..60 |
| FLOWFORGE_OFFLINE_SECONDS | 10 | 3..600 |
| FLOWFORGE_RECOVERY_SECONDS | 1 | 1..30 |

Lease must exceed twice renew interval; offline threshold must exceed twice
heartbeat interval. Configure the same policy across worker processes. Short
3/1/1/4/1 smoke periods deliberately exercise expiry; scheduling pauses/DB latency
can expire work even with a healthy process. There is no bounded recovery SLA.

Registry statuses are ONLINE at registration, IDLE/BUSY on heartbeat, DRAINING
on normal/fatal pool shutdown and OFFLINE at stop or heartbeat expiry. Counts
are sampled, so active_jobs is informational, not scheduling authority. A stale
or OFFLINE worker cannot renew its registration or claim; restart creates a new
UUID. Registry does not persist hostname/capability scheduling yet. Last heartbeat
and all leases use PostgreSQL time; local timers only trigger operations.

Claim establishes lease + owner + incremented Attempt atomically. Renewal is
bounded to min(3s, renew interval), heartbeat to min(3s, heartbeat interval).
Registration/offline/recovery/registry stop are bounded to 3s; Claim/Finalize
retain 5s bounds and independent rollback cleanup. Renew/finalize require a
current unexpired lease, owner and Attempt; old owners cannot resurrect authority.

## Inspect work and failures

GET `/api/v1/jobs/{id}` shows database truth. Inspect `job_attempts` by Job ID,
owner and attempt number, and `job_dispatch.published_at` for publication.
A marker is not execution evidence. One dispatcher per process scans at most
100 each second; concurrent scans can publish duplicates. Unmarked or 30-second
old still-QUEUED intent is eligible. Recovery separately locks at most 100 expired
RUNNING Jobs, closes old Attempts FAILED/lease_expired, sets QUEUED and resets
intent in one transaction. No active/unexpired RUNNING work is requeued.

Redis pending entries mean unacknowledged delivery. Malformed/missing/non-QUEUED
messages ACK only that delivery. RUNNING duplicates cannot ACK the original
execution's different MessageID. Conditional Claim+Attempt and owner/attempt
Finalize transactions still arbitrate across processes. Business validation
failures persist FAILED and do not stop peers. No retry of business execution
occurs on failed ACK after terminal commit. Crash recovery may exceed stored
max_attempts; unified retry accounting is reserved for Phase 5. Terminal business
FAILED Jobs are never recovered. Inspect Attempt history by sequence, rather than
expecting every historical Attempt status to equal the current Job.

Structured lifecycle events: `worker_started`, `slot_started`, `job_claimed`,
`job_finished`, `worker_draining`, `slot_stopped`, `dispatcher_stopped`,
`worker_stopped`, `pool_failed`. Worker ID/concurrency appear on pool logs,
slot/consumer on slot logs, Job ID/attempt number on execution logs. Atomic
`active_jobs` counts held claimed work through ACK; observed values are <=C.
Phase 4 adds heartbeat, lease_acquired, lease_renewed, lease_expired,
recovery_requeued, stale_finalize_rejected, worker_offline and joined
heartbeat_stopped/recovery_stopped. Recovery logs include expired_worker_id
alongside the reaper's worker_id, job_id and attempt_number. Failure classes
include dispatch_failed/receive_failed/delivery_failed, heartbeat_failed,
lease_renew_failed, recovery_failed and offline_scan_failed.
No payload, credentials, recovered panic value, stack or raw driver error is logged.

## Shutdown and unresolved work

SIGINT/SIGTERM logs draining then cancels all slots, dispatcher, heartbeat and
recovery; each execution renewer stops and joins. SLEEP attempts
FAILED/execution_cancelled persistence and ACK. Concurrent Finalize+ACK calls
share cancellation five seconds after draining, rather than C serial windows.
The repository's independent rollback cleanup is also bounded to five seconds
and final registry stop has up to three more seconds; Compose allows fifteen
seconds. This is not a universal five-second wall-clock guarantee. An in-flight
Receive/Claim can race with cancellation: unclaimed work is not marked complete,
a successful late Claim attempts cancellation persistence. All goroutines join
before shared dependencies close.

Any slot's post-claim Finalize/ACK or renewal error, or heartbeat failure, stops
the whole pool and returns nonzero. Reaper/offline scan failure logs and retries.
Executor panic follows the same process-level fail-fast policy; its claimed Job
stays RUNNING/pending for expiry recovery without fabricated finalization. Restore dependencies and
inspect PostgreSQL before restarting an exited process. A failed Finalize leaves
RUNNING until valid expiry recovery; failed ACK after commit leaves terminal data.
An uncertain renewal stops local execution without Finalize/ACK. Ambiguous commits
are not guessed; inspect database truth, which controls recovery. Graceful cleanup
only persists while the lease remains valid; expired cleanup is rejected and the
old Attempt is closed by recovery. Stop reason distinguishes graceful_shutdown,
fatal_error and heartbeat_expired. If the database is unavailable at stop, registry
may remain stale until another healthy worker detects it.

Abrupt SIGKILL or stopped renewal expires the lease. A healthy reaper/dispatcher
restores execution via a new Attempt and fresh notification. No healthy reaper
means no progress until a worker returns. Old pending entries are not reclaimed
or automatically cleaned. Corrupt history rolls back its bounded recovery batch
and logs recovery_failed; operator repair is required. Retained terminal leases
are audit metadata, not authority to execute again. External effects can overlap
or repeat across leases; fencing protects DB result writes only. No exactly-once
side effects, submission idempotency, business retry or production orchestration.

Streams/consumers/outbox have no automatic retention. Redis AOF everysec/noeviction
still can lose its last second; database notification reconciliation only handles
QUEUED work. Do not trim pending messages or delete volumes as routine cleanup.

## Repeatable acceptance smoke

```powershell
./scripts/phase3-smoke.ps1 -Concurrency 2
```

This local script requires Docker, the standard development `flowforge` database
and role, and create/drop-database permission. It temporarily replaces API and
worker containers with a new generated database and Redis stream, runs two
processes, checks both IDs/C=2, stops one idle, checks survivor work, then stops
the survivor saturated/active. It verifies Job/Attempt data and joined lifecycle
logs. It deletes only its generated database/key and restores one normal worker;
normal volumes/data and `.env` remain. Run while the development API/workers can
be restarted. Integration tests instead use a random schema and verify schema
isolation on every connection; they delete only random keys, never FLUSHDB.

See [Phase 3 evidence](reports/phase-3-report.md) and
[ADR 0004](decisions/0004-fixed-worker-pool.md). No smoke is a production benchmark.

```powershell
./scripts/phase4-smoke.ps1
```

Uses the same isolated local-resource approach with two C=1 processes and short
3/1/1/4/1 lease/renew/heartbeat/offline/reaper periods. It SIGKILLs a claimed owner,
verifies exit=137/no graceful finalize and survivor Attempt 2 success. It restarts
that container with a new identity, pauses a claimed owner through lease expiry
and takeover, then resumes it and checks fencing/fatal stop with no overwritten
history. A further claimed process is disconnected from its Compose network:
bounded DB heartbeat/renew failure must stop it without false persistence, and
another owner must complete Attempt 2. Its exact network connection is restored.
Finally an active survivor SIGTERM must persist execution_cancelled,
record graceful_shutdown, exit 0 and join every loop. Cleanup unpauses any paused
container, restores any disconnected test container, stops test services, deletes
only its generated database/key, and
restores normal API/one worker. See [Phase 4 evidence](reports/phase-4-report.md)
and [ADR 0005](decisions/0005-db-time-leases-and-recovery.md).

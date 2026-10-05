# Worker operations — Phase 3

API and worker remain independent. Each worker process has one dispatcher and
C fixed consumer/executor slots. Set `FLOWFORGE_WORKER_CONCURRENCY` to a decimal
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
Worker PostgreSQL maximum is C+2 connections, Redis PoolSize/MaxActiveConns C+4.
At most C Redis reads block; ACK/publication retain spare capacity. API budgets
10 PostgreSQL and 10 Redis connections. Include N*(C+2), API and diagnostic
clients in the server's connection budget before scaling. SLEEP never holds a
SQL transaction/connection. These local bounds do not limit submissions, Redis
history, outbox size or global queue depth.

Migration 000003 backfills existing QUEUED work; unsupported legacy types become
FAILED. Phase 3 adds no migration. Stop API/workers before rollback, and never
use schema rollback as recovery. PostgreSQL startup failure exits; Redis outage
uses bounded retry loops. No automatic restart policy is configured.

## Inspect work and failures

GET `/api/v1/jobs/{id}` shows database truth. Inspect `job_attempts` by Job ID,
owner and attempt number, and `job_dispatch.published_at` for publication.
A marker is not execution evidence. One dispatcher per process scans at most
100 each second; concurrent scans can publish duplicates. Unmarked or 30-second
old still-QUEUED intent is eligible. RUNNING is never reset or republished.

Redis pending entries mean unacknowledged delivery. Malformed/missing/non-QUEUED
messages ACK only that delivery. RUNNING duplicates cannot ACK the original
execution's different MessageID. Conditional Claim+Attempt and owner/attempt
Finalize transactions still arbitrate across processes. Business validation
failures persist FAILED and do not stop peers. No retry of business execution
occurs on failed ACK after terminal commit.

Structured lifecycle events: `worker_started`, `slot_started`, `job_claimed`,
`job_finished`, `worker_draining`, `slot_stopped`, `dispatcher_stopped`,
`worker_stopped`, `pool_failed`. Worker ID/concurrency appear on pool logs,
slot/consumer on slot logs, Job ID/attempt number on execution logs. Atomic
`active_jobs` counts held claimed work through ACK; observed values are <=C.
Failure classes also include dispatch_failed/receive_failed/delivery_failed.
No payload, credentials, recovered panic value, stack or raw driver error is logged.

## Shutdown and unresolved work

SIGINT/SIGTERM logs draining then cancels all slots and dispatcher. SLEEP attempts
FAILED/execution_cancelled persistence and ACK. Concurrent Finalize+ACK calls
share cancellation five seconds after draining, rather than C serial windows.
The repository's independent rollback cleanup is also bounded to five seconds
and may extend wall-clock join time; Compose allows fifteen seconds. An in-flight
Receive/Claim can race with cancellation: unclaimed work is not marked complete,
a successful late Claim attempts cancellation persistence. All goroutines join
before shared dependencies close.

Any slot's post-claim Finalize/ACK error stops the whole pool and returns nonzero.
Executor panic follows the same process-level fail-fast policy; its claimed Job
stays RUNNING/pending without fabricated finalization. Restore dependencies and
inspect PostgreSQL before restarting an exited process. A failed Finalize leaves
RUNNING; failed ACK after commit leaves terminal data. Ambiguous commits and abrupt
post-claim crashes remain unresolved. There is no heartbeat, lease, stale-owner
fencing, pending-entry reclaim or RUNNING recovery; Phase 4 remains Planned.

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

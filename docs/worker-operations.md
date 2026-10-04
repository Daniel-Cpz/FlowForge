# Worker operations — Phase 2

Run one worker process only. API remains independent; worker hosts one bounded
dispatcher goroutine and one serial executor. No distributed registration or
worker pool exists. Host-run processes require exported environment variables;
Compose loads .env. The default Redis stream is flowforge:jobs:v1, group
flowforge-workers-v1. FLOWFORGE_REDIS_STREAM can separate disposable test or
development environments (nonblank, at most 256 bytes, no control characters).
Use distinct PostgreSQL storage and Redis namespaces for separate workloads.

## Startup and migrations

Use `docker compose up --build -d` for development. Migration 000003 creates
dispatch intent and backfills all existing QUEUED Jobs. Existing unsupported
types will become FAILED once the worker runs. Readiness includes outbox schema.
PostgreSQL startup failure exits cleanly; Redis outages retain intent and use
bounded loops instead of tight retries. No automatic process restart policy is
installed; restore the dependency and restart an exited worker explicitly.

Stop API and worker before schema rollback. Each `down` removes one latest
migration; version 000003 down removes dispatch history. Reapplying it backfills
QUEUED work. Never perform rollback in a live deployment as a recovery mechanism.

## Inspect work

Read Job state through GET /api/v1/jobs/{id}. SQL diagnostics may inspect
job_dispatch.published_at and job_attempts by Job ID. A null marker means not
confirmed published, not necessarily never published. A non-null marker is
not proof of execution. Still-QUEUED work is republished after 30 seconds; no
RUNNING work is republished or returned to QUEUED by the dispatcher.

Redis pending entries indicate unacknowledged delivery. Malformed/missing Job
messages and non-QUEUED duplicates are discarded by ACK without execution.
Payloads and credentials are not logged. Structured event codes include
dispatch_failed, receive_failed, delivery_failed, invalid_delivery, missing_job
and job_finished. Logs and API failures intentionally omit raw driver details.

## Shutdown and unresolved work

SIGINT/SIGTERM cancels the active SLEEP timer. The worker attempts bounded
FAILED/execution_cancelled persistence and ACK, then closes resources. A normal
business validation failure does not retry. A finalize/ACK failure stops the
worker with a sanitized error; inspect PostgreSQL before interpreting pending
messages. A post-claim abrupt crash, ambiguous claim response or unavailable
finalize storage may leave RUNNING indefinitely. No heartbeat, lease, fencing,
stale-consumer reclaim or automatic recovery exists; do not reset status manually
and describe it as guaranteed recovery. Phase 4 must establish that policy.

Redis AOF everysec/noeviction helps retain queue state, while PostgreSQL intent
reconstructs still-QUEUED notifications after Redis loss. Stream messages,
consumer metadata and outbox rows are not automatically cleaned up. Do not trim
pending stream messages or delete volumes as a routine cleanup operation.

## Isolation for tests

Integration tests verify a generated PostgreSQL schema on every connection and
only delete randomly generated Redis keys. They never flush Redis. Controlled
process smoke should use a disposable database plus dedicated stream, then stop
the processes before deleting those exact resources. Keep the normal development
database and stream separate. No test or benchmark proves production readiness.

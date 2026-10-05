# Architecture

Status: Phases 0–4 implemented, including DB-time leases and crash recovery.

## Boundaries

A modular monolith supplies two separately deployable Go processes. `cmd` calls
the `internal/app` composition root; it constructs config, logging, dependencies,
repositories, services and transport. `transport/http` depends on the job service,
which depends on domain models and a domain-owned Repository interface.
`infrastructure/postgres` implements that interface. Infrastructure depends
inward on domain; domain does not depend on SQL, Redis or HTTP.

HTTP exposes Create, GetByID and List. Dispatcher and worker services define
narrow infrastructure interfaces for their actual use cases, implemented by
PostgreSQL and Redis adapters. Job transitions remain centralized in domain.
Claim/finalize use atomic SQL predicates; calling a pure in-memory transition
alone would not establish durable execution ownership. No update HTTP API exists.

## Components and source of truth

- API: bounded HTTP input, service validation, persistence, health/readiness.
- Worker process: one dispatcher, heartbeat and reaper, C slots (1..32), <=C renewers.
- PostgreSQL: sole durable truth for jobs, attempts, leases, registry and dispatch intent.
- Redis: Streams delivery with consumer group and explicit ACK, never payload authority.
- Migrations: separate command; version table + advisory transaction lock ensure
  repeated startup and concurrent migrators cannot apply a version twice.

The PostgreSQL 18 volume mounts `/var/lib/postgresql`, matching the official
image's versioned data layout ([Docker documentation](https://docs.docker.com/guides/postgresql/networking-and-connectivity/)).

## Failure model

Missing/invalid configuration exits nonzero without dumping configuration.
PostgreSQL must respond within the bounded startup context or the process exits.
Redis connectivity is checked by API readiness and retried by the worker.
Driver errors are not logged verbatim because they can contain connection or
payload details. Operational logs classify failures; richer safe diagnostics
remain future work.
Logs use structured JSON and UTC timestamps. `event` identifies lifecycle or
failure events; persisted executions log `job_id` and status. No trace context is fabricated.

Liveness checks do not query dependencies. Readiness checks both dependencies
and the jobs/outbox/registry columns, within a two-second context. CRUD uses
request context plus a five-second database timeout. A database outage returns
generic 500; a Redis outage makes readiness fail but does not prevent a directly
addressed creation request from atomically committing Job and dispatch intent.

On SIGINT/SIGTERM, API stops accepting connections and allows ten seconds for
in-flight requests, then force-closes remaining connections if necessary.
Worker logs draining, cancels all slots, dispatcher, heartbeat and recovery, and concurrently
tries FAILED/execution_cancelled persistence and ACK within a shared five-second
cleanup cancellation window. Repository rollback retains its separate bounded
five-second budget; final registry stop is bounded to three seconds. All goroutines,
including execution renewers, join before clients close. A fatal slot/heartbeat/renew error
or panic drains the pool and exits with a safe error; business failures continue.
Panicking claimed work remains RUNNING/pending for lease recovery. Compose grants fifteen seconds. Redis receive
errors wait 250 ms between attempts; dispatch cycles wait one second even after
failure or a full batch. Restart policies and production orchestration remain planned.

Creation persists before returning 201. If the DB commit succeeds but the client
loses the response, resubmitting may create another job. There is no submission
idempotency guarantee. PostgreSQL constraints validate values, not the full
state transition graph; direct SQL is not a supported state transition API.

## Delivery semantics — Phases 2–4

Delivery is **at-least-once**, backed by PostgreSQL dispatch intent. Create
commits Job + outbox in one transaction. The dispatcher queries at most 100 rows
per cycle, XADDs version `1` and Job ID, then marks publication. Failed publish
retains pending intent; a lost marker response allows duplicate publication.
Still-QUEUED Jobs are republished after 30 seconds even when marked published,
so lost Redis data or a lost pre-claim pending delivery is reconstructible.
Expired RUNNING recovery independently resets the same intent transactionally.

Streams use `XGROUP CREATE ... 0 MKSTREAM`, `XREADGROUP ... > COUNT 1 BLOCK 500`
and `XACK`, retaining consumer-group pending entries until explicit ACK.
Messages reference authoritative PostgreSQL data. A worker reloads the Job,
then conditionally claims QUEUED -> RUNNING and inserts one attempt in a single
transaction with a DB-time lease. Finalization conditionally matches RUNNING,
worker, attempt and unexpired lease,
updating both Job and attempt atomically before ACK. Missing/malformed messages
and non-QUEUED duplicates are ACKed without execution. A pre-claim infrastructure
error retains the current message for bounded local retry; a post-claim
finalize/ACK failure stops the worker with a sanitized error.

An abrupt crash or uncertain claim response leaves database state authoritative.
If it is RUNNING, expiry recovery closes the old Attempt and restores dispatch
intent; if terminal, recovery does nothing. Old pending entries are retained.
No exactly-once business-side-effect guarantee follows from lease fencing.

Message delivery and a business side effect are different events. A worker can
perform an external side effect, then crash before recording success. Delivering
the job again cannot prove that the external action did not happen. A local
transaction cannot make an arbitrary external API exactly-once.

Planned safeguards include scoped submission idempotency and side-effect-specific deduplication.
Scope and retention are not decided, so idempotency_key has no global permanent
unique constraint. See [ADR 0003](decisions/0003-durable-dispatch-and-single-worker.md)
for the database/queue handoff and [ADR 0004](decisions/0004-fixed-worker-pool.md)
for fixed slots, connection budgets, consumer identity and supervision.

## Lease and recovery — Implemented

1. Worker A registers a fresh UUID and conditionally claims QUEUED with an incremented attempt.
2. PostgreSQL sets lease_expiry; owner/attempt and unexpired lease guard renewal.
3. Worker A crashes or loses connectivity; renewal stops.
4. Recovery detects expiration and safely makes the job schedulable again.
5. Worker B acquires a new attempt/ownership token and completes the job.

A bounded reaper locks <=100 expired rows with FOR UPDATE SKIP LOCKED. Its
transaction checks exact owner/attempt/expiry, closes the matching old Attempt
as FAILED/lease_expired, clears current execution metadata, sets QUEUED and
resets dispatch publication. Failed intent writes roll back all changes.
Concurrent reapers cannot apply two transitions; a stale Finalize or Renew
cannot replace the new owner/attempt. Ordinary domain RUNNING -> QUEUED remains
invalid; RecoverExpired is a separate expiry-guarded operation.

Registry heartbeat and offline expiry use database clock_timestamp(). Fresh
ONLINE/IDLE/BUSY registration is required to Claim/Renew; worker-row share locks
serialize with OFFLINE detection. OFFLINE identity never revives; restart gets a
new UUID. Registry liveness is separate from Job execution authority, and does
not perform capability scheduling. Each worker has independent bounded heartbeat
and recovery loops; recovery scan errors retry, heartbeat/renew failure drains.
Default lease/renew/heartbeat/offline/recovery = 15/5/2/10/1 seconds. DB time
avoids worker clock skew; database clock changes still affect lease timing.
See [ADR 0005](decisions/0005-db-time-leases-and-recovery.md) and real crash evidence.

## Retry — Planned

FAILED -> RETRYING -> exponential backoff + jitter -> QUEUED. Maximum attempts
limit retries; exhaustion leads to DEAD_LETTER. Retryability, delay caps and
business idempotency are policy decisions. TIMED_OUT is terminal in this version;
retry after timeout requires a later explicit policy and state graph revision.

## Operational limits

Local development only: no authentication, TLS termination, rate limiting,
multi-tenancy, production secret manager, metrics, tracing or benchmarks.
API connection limits are ten each. Worker limits are C+4 PostgreSQL and C+4
Redis, explicitly capped so blocking reads leave ACK/publication capacity.
Multiply those budgets by process count; this is local execution backpressure,
not API admission control or a global queue limit. PostgreSQL statement timeout is
five seconds. List uses bounded keyset pagination; it does not hold a snapshot
transaction across HTTP requests.
Production environment config requires PostgreSQL TLS, but that check alone does
not make this a production-ready system. Redis TLS is not implemented.

## Persistence and transaction boundaries

Phase 2 supersedes ADR 0002's single-statement Create boundary: parameterized
`INSERT ... RETURNING` + outbox insert + commit now form one explicit transaction.
Get/List remain single-statement reads. Claim/attempt creation and terminal
Job/attempt finalization each have their own transaction. No database transaction
is held across Redis I/O or SLEEP. Rollback uses an independent five-second
cleanup context; requests and database operations retain bounded deadlines.
Recovery has its own bounded transaction; heartbeat and renewal are bounded
statements. Migrations keep their schema/version transaction, appending 000004
for workers and expiry indexes without editing historical SQL.

Create returns the database's JSONB representation and timestamp precision, so
POST and GET agree. Domain value validation is shared by the service and the
repository read/write boundary. Important numeric/status constraints remain in
PostgreSQL. Invalid stored status, identifiers or timestamps produce an internal
failure rather than a fabricated valid job. State-transition rules remain only
in the centralized domain graph; no trigger duplicates that graph.

HTTP owns exact field names, duplicate-envelope detection and cursor encoding.
Service owns creation defaults and page lookahead. Domain exposes a typed
`PageCursor` containing a UTC microsecond timestamp and UUID; repository never
receives a base64 string. List orders by `(created_at DESC, id DESC)` and applies
the exclusive tuple condition `(created_at, id) < ($cursor_time, $cursor_id)`.
The existing `jobs_created_at_id_idx` matches this order and boundary; Phase 2
adds an outbox publication index, without changing historical migrations.
No throughput or planner-performance claim is made.

The service requests limit+1 rows, returns at most limit, and constructs the next
boundary from the last returned row when a lookahead exists. Newer inserts cannot
shift older pages. This is not snapshot isolation across requests; backdated
inserts behind a boundary may appear. Phase 1 removes offset without maintaining
a second pagination mode; no existing external compatibility commitment was found.
See [ADR 0002](decisions/0002-phase-1-api-contract.md).

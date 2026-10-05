# Architecture

Status: Phases 0–3 implemented, including multiple workers and bounded concurrency.

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
- Worker process: one dispatcher and C fixed consumer/executor slots (1..32).
- PostgreSQL: sole durable truth for jobs, attempts and dispatch intent.
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
and the jobs/outbox columns used by the API, within a two-second context. CRUD uses
request context plus a five-second database timeout. A database outage returns
generic 500; a Redis outage makes readiness fail but does not prevent a directly
addressed creation request from atomically committing Job and dispatch intent.

On SIGINT/SIGTERM, API stops accepting connections and allows ten seconds for
in-flight requests, then force-closes remaining connections if necessary.
Worker logs draining, cancels all slots and its dispatcher, and concurrently
tries FAILED/execution_cancelled persistence and ACK within a shared five-second
cleanup cancellation window. Repository rollback retains its separate bounded
five-second budget. All goroutines join before clients close. A fatal slot error
or panic drains the pool and exits with a safe error; business failures continue.
Panicking claimed work remains RUNNING/pending. Compose grants fifteen seconds. Redis receive
errors wait 250 ms between attempts; dispatch cycles wait one second even after
failure or a full batch. Restart policies and production orchestration remain planned.

Creation persists before returning 201. If the DB commit succeeds but the client
loses the response, resubmitting may create another job. There is no submission
idempotency guarantee. PostgreSQL constraints validate values, not the full
state transition graph; direct SQL is not a supported state transition API.

## Delivery semantics — Phases 2–3

Delivery is **at-least-once**, backed by PostgreSQL dispatch intent. Create
commits Job + outbox in one transaction. The dispatcher queries at most 100 rows
per cycle, XADDs version `1` and Job ID, then marks publication. Failed publish
retains pending intent; a lost marker response allows duplicate publication.
Still-QUEUED Jobs are republished after 30 seconds even when marked published,
so lost Redis data or a lost pre-claim pending delivery is reconstructible.
This is notification reconciliation, not RUNNING recovery or stale-owner reclaim.

Streams use `XGROUP CREATE ... 0 MKSTREAM`, `XREADGROUP ... > COUNT 1 BLOCK 500`
and `XACK`, retaining consumer-group pending entries until explicit ACK.
Messages reference authoritative PostgreSQL data. A worker reloads the Job,
then conditionally claims QUEUED -> RUNNING and inserts one attempt in a single
transaction. Finalization conditionally matches RUNNING, worker and attempt,
updating both Job and attempt atomically before ACK. Missing/malformed messages
and non-QUEUED duplicates are ACKed without execution. A pre-claim infrastructure
error retains the current message for bounded local retry; a post-claim
finalize/ACK failure stops the worker with a sanitized error.

An abrupt crash or uncertain database response after successful claim can leave
RUNNING indefinitely; pending entries of old consumers are not reclaimed. The
current guard prevents duplicate execution from a second claim, but provides
no exactly-once business-side-effect guarantee. Phase 4 must address recovery.

Message delivery and a business side effect are different events. A worker can
perform an external side effect, then crash before recording success. Delivering
the job again cannot prove that the external action did not happen. A local
transaction cannot make an arbitrary external API exactly-once.

Planned safeguards include scoped submission idempotency and side-effect-specific deduplication.
Scope and retention are not decided, so idempotency_key has no global permanent
unique constraint. See [ADR 0003](decisions/0003-durable-dispatch-and-single-worker.md)
for the database/queue handoff and [ADR 0004](decisions/0004-fixed-worker-pool.md)
for fixed slots, connection budgets, consumer identity and supervision.

## Lease and recovery — Planned

1. Worker A atomically claims eligible work with an execution ownership token.
2. A lease is recorded in PostgreSQL and periodically renewed by that owner.
3. Worker A crashes or loses connectivity; renewal stops.
4. Recovery detects expiration and safely makes the job schedulable again.
5. Worker B acquires a new attempt/ownership token and completes the job.

A stale Worker A must not overwrite Worker B's result; future transitions need
lease/attempt fencing and compare-and-set conditions. Clock policy and recovery
races require tests before implementation claims. Planned demonstration: kill
Worker A, wait for lease expiry, observe Worker B take over. None of this runs yet.

## Retry — Planned

FAILED -> RETRYING -> exponential backoff + jitter -> QUEUED. Maximum attempts
limit retries; exhaustion leads to DEAD_LETTER. Retryability, delay caps and
business idempotency are policy decisions. TIMED_OUT is terminal in this version;
retry after timeout requires a later explicit policy and state graph revision.

## Operational limits

Local development only: no authentication, TLS termination, rate limiting,
multi-tenancy, production secret manager, metrics, tracing or benchmarks.
API connection limits are ten each. Worker limits are C+2 PostgreSQL and C+4
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
Migrations keep their schema/version transaction, adding only version 000003.

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

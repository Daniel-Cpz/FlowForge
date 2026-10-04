# Architecture

Status: Phase 0 foundation and Phase 1 API/persistence implemented; execution is Planned.

## Boundaries

A modular monolith supplies two separately deployable Go processes. `cmd` calls
the `internal/app` composition root; it constructs config, logging, dependencies,
repositories, services and transport. `transport/http` depends on the job service,
which depends on domain models and a domain-owned Repository interface.
`infrastructure/postgres` implements that interface. Infrastructure depends
inward on domain; domain does not depend on SQL, Redis or HTTP.

Only Create, GetByID and List exist because these are the current use cases.
There is no update API or worker repository/service: without a worker use case
they would be empty abstractions. Job state transitions are centralized in domain.
Future persistence transitions must atomically verify expected state and lease
ownership; calling the pure in-memory transition function alone is not sufficient.

## Components and source of truth

- API: bounded HTTP input, service validation, persistence, health/readiness.
- Worker: dependency connection and graceful process lifecycle only.
- PostgreSQL: sole durable truth for jobs and future execution records.
- Redis: connectivity and readiness only; no queue, consumer or durable job state.
- Migrations: separate command; version table + advisory transaction lock ensure
  repeated startup and concurrent migrators cannot apply a version twice.

The PostgreSQL 18 volume mounts `/var/lib/postgresql`, matching the official
image's versioned data layout ([Docker documentation](https://docs.docker.com/guides/postgresql/networking-and-connectivity/)).

## Failure model

Missing/invalid configuration exits nonzero without dumping configuration.
PostgreSQL and Redis must respond within the bounded startup context or the
process exits. If Redis fails after PostgreSQL opened, PostgreSQL is closed.
Driver errors are not logged verbatim because they can contain connection or
payload details. Operational logs classify failures; richer safe diagnostics
remain future work.
Logs use structured JSON and UTC timestamps. `event` identifies lifecycle or
failure events. Future execution logs will add `job_id`, `worker_id`, `attempt`
and `trace_id` when those contexts exist; Phase 0 does not fabricate those values.

Liveness checks do not query dependencies. Readiness checks both dependencies
and the jobs columns used by the API, within a two-second context. CRUD uses
request context plus a five-second database timeout. A database outage returns
generic 500; a Redis outage makes readiness fail but does not prevent a directly
addressed persistence request from succeeding. Redis stores no job state.

On SIGINT/SIGTERM, API stops accepting connections and allows ten seconds for
in-flight requests, then force-closes remaining connections if necessary.
Worker stops waiting and closes clients. Compose grants fifteen seconds.
Neither process retries startup indefinitely. Restart policies and production
orchestration are outside this phase.

Creation persists before returning 201. If the DB commit succeeds but the client
loses the response, resubmitting may create another job. There is no submission
idempotency guarantee. PostgreSQL constraints validate values, not the full
state transition graph; direct SQL is not a supported state transition API.

## Delivery semantics — Planned

The future target is **at-least-once delivery**, with duplicate execution possible.
The current implementation provides persistence only and no message delivery guarantee.

Message delivery and a business side effect are different events. A worker can
perform an external side effect, then crash before recording success. Delivering
the job again cannot prove that the external action did not happen. A local
transaction cannot make an arbitrary external API exactly-once.

Planned safeguards include scoped idempotency keys, duplicate detection,
execution records, safe state transitions and side-effect-specific deduplication.
Scope and retention are not decided, so idempotency_key has no global permanent
unique constraint. A future Redis notification must be reconstructible from
PostgreSQL; the database/queue dual-write gap needs an explicit design in Phase 2.

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
Pool maximum is ten connections per process. PostgreSQL statement timeout is
five seconds. List uses bounded keyset pagination; it does not hold a snapshot
transaction across HTTP requests.
Production environment config requires PostgreSQL TLS, but that check alone does
not make this a production-ready system. Redis TLS is not implemented.

## Persistence and transaction boundaries

Create is one parameterized `INSERT ... RETURNING`; Get and List are each one
parameterized SELECT. Each statement has its own atomic boundary; an explicit
transaction adds no shared-work guarantee to these current use cases. Migrations
keep their existing explicit transaction because they change schema and version
history together. Future job/attempt/queue coordination needs a separate design.

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
The existing `jobs_created_at_id_idx` matches this order and boundary, so Phase 1
needs no new index or migration. No throughput or planner-performance claim is made.

The service requests limit+1 rows, returns at most limit, and constructs the next
boundary from the last returned row when a lookahead exists. Newer inserts cannot
shift older pages. This is not snapshot isolation across requests; backdated
inserts behind a boundary may appear. Phase 1 removes offset without maintaining
a second pagination mode; no existing external compatibility commitment was found.
See [ADR 0002](decisions/0002-phase-1-api-contract.md).

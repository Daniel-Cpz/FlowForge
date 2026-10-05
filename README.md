# FlowForge

Distributed Job Processing Platform for backend, distributed systems, and cloud
engineering.

## Overview

FlowForge is a Go backend project exploring reliable asynchronous job processing.
The platform persists jobs and executes a bounded SLEEP demonstration asynchronously.
PostgreSQL is the durable source of truth. API and worker are separate processes
sharing a small modular codebase.

## Why FlowForge

The engineering problem is coordinating work despite crashes, retries, duplicate
delivery and changing load. Those guarantees need explicit state transitions,
durable records and testable failure handling before adding scheduling features.
No throughput or recovery-time claim is made. Delivery is at-least-once;
execution authority and crash recovery are guarded by PostgreSQL leases.

## Architecture

```text
POST -> Job service -> PostgreSQL: keyed create/replay/conflict + intent
                               |
                   Worker process dispatcher -> Redis Streams
                               |
                   N worker processes x C slots -> DB claim + attempt + lease
                               |
                   SLEEP -> DB outcome + attempt + retry schedule/terminal -> Redis ACK
                               |
                   Expired lease -> budgeted RETRYING/DEAD_LETTER -> due queue + intent
```

The composition root wires infrastructure into the service. Domain and service
do not import database or transport packages. PostgreSQL is authoritative;
Redis messages contain only a protocol version and Job ID.
See [architecture](docs/architecture.md), [lifecycle](docs/job-lifecycle.md) and
[decisions](docs/decisions/README.md).

## Tech Stack

- Go 1.26+, standard `net/http`, `log/slog`, `testing`
- PostgreSQL 18, pgx v5; Redis 8.2, go-redis v9
- Docker / Docker Compose; GitHub Actions CI
- UUID identifiers; JSONB payloads; UTC / TIMESTAMPTZ timestamps

## Getting Started

Install Docker with Compose. Copy `.env.example` to `.env` and replace the
PostgreSQL password placeholder with a local development password. `.env` is
ignored by Git. Redis password is optional for this loopback-only development
environment. Never use these Compose settings as a production deployment.
If a port is already occupied, set the corresponding
`FLOWFORGE_POSTGRES_PUBLISHED_PORT`, `FLOWFORGE_REDIS_PUBLISHED_PORT` or
`FLOWFORGE_API_PUBLISHED_PORT` in `.env`. Adjust curl URLs and host-run Go
connection addresses accordingly; internal container addresses stay unchanged.

```sh
cp .env.example .env
# Edit .env before continuing.
docker compose up --build -d
docker compose ps
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

PowerShell: use `Copy-Item .env.example .env` and `curl.exe`. Compose loads `.env`
automatically. PostgreSQL connectivity is required at process startup; Redis
outages use bounded worker retry loops and make API readiness fail.
The migration service applies schema versions before API and worker startup.
The API's readiness check also detects missing jobs/outbox/worker registry columns.

```sh
curl -i -X POST http://localhost:8080/api/v1/jobs \
  -H 'Content-Type: application/json' \
  -d '{"type":"SLEEP","payload":{"duration_ms":250}}'
curl http://localhost:8080/api/v1/jobs
# Replace UUID with the id returned above:
curl http://localhost:8080/api/v1/jobs/UUID
```

### API contract

Phase 1 tightens the creation envelope and replaces the early offset contract
with cursor pagination. No external consumer or stable offset compatibility
commitment was found. `offset` now returns 400.

| Method | Path | Behavior |
|---|---|---|
| GET | `/health` | Process alive, `200 {"status":"ok"}`; independent of dependencies |
| GET | `/ready` | PostgreSQL, Redis and jobs/outbox/worker registry schema ready: 200; otherwise 503 |
| POST | `/api/v1/jobs` | First create/no key 201; keyed replay 200; key conflict 409; Location identifies original Job |
| GET | `/api/v1/jobs/{id}` | Job by UUID; 404 if absent, 400 if malformed |
| GET | `/api/v1/jobs?limit=20&cursor=...` | Exclusive cursor pagination by `(created_at DESC, id DESC)` |

| Create field | Missing | Explicit null | Supplied value |
|---|---|---|---|
| `type` | 400 | 400 | String; trim outer whitespace, retain inner content; 1–128 Unicode characters |
| `payload` | 400 | Accepted as JSON null | Any JSON object, array, string, number or boolean |
| `priority` | Default 0 | 400 | Integer 0–100 |
| `max_attempts` | Default 3 | 400 | Integer 1–100 |
| `timeout` | Default 300 | 400 | Integer 1–86400 **seconds** |
| `idempotency_key` | No key | No key | Nonblank string, at most 255 Unicode characters; supplied whitespace is preserved |

Only those exact lowercase field names are accepted. Unknown, wrong-case,
duplicate top-level fields (including equivalent escaped key names), and
server-owned fields return 400. Client-owned `id`, `status`, `result`,
`attempt_count`, worker/lease fields or timestamps are not accepted.
Numeric fields require JSON integer notation; strings, fractions and exponent
notation are rejected. Empty/blank type or key values are rejected.

Bodies are capped at 1 MiB before parsing, including requests without a declared
length (413). Empty bodies, invalid UTF-8, malformed JSON and trailing JSON
documents return 400. Unpaired surrogate escapes in metadata are rejected.
PostgreSQL JSONB also rejects Unicode NUL, unpaired surrogates and numbers outside
its numeric range; client payload failures return 400 without driver details.
Nested payload duplicate keys retain JSONB's last-value semantics. Only the
request envelope rejects duplicates; no recursive custom parser is introduced.

`attempt_count` starts at 0. Unset result, worker, lease and execution timestamps
serialize as null at creation. Time strings are RFC3339 with optional fractional seconds in
UTC. A non-null idempotency key is an exact global submission key: same canonical request returns the original Job (200), different request returns 409 `IDEMPOTENCY_CONFLICT`. Null/absent keys always create distinct Jobs. Canonical identity is trimmed type, JSONB-semantic payload, priority, max_attempts and timeout; generated ID/time are excluded. Replay works at RUNNING/RETRYING/terminal states and never redispatches.
Timeout and priority are stored metadata and are not enforced during execution.
First create returns the QUEUED representation captured in its Job/outbox transaction;
a subsequent GET may already show execution progress. JSONB may normalize
spacing, key order, numeric notation and nested duplicate keys. GET returns the
same canonical payload representation.

### Asynchronous execution

`SLEEP` is the only executable type (case-sensitive). Its payload is exactly
`{"duration_ms":250}`: one non-null integer field, **0–10000 milliseconds**
inclusive, with no extra fields. Fractions, exponent notation, wrong-case keys,
null, strings and out-of-range values fail execution. The existing Create API
still accepts arbitrary valid JSON and type strings; unsupported types and
invalid SLEEP payloads receive 201 then become DEAD_LETTER after a permanent failure with a static result error
(`unsupported_job_type` or `invalid_sleep_payload`). JSONB's existing last-key
semantics apply before execution. Success stores
`{"duration_ms":250,"outcome":"slept"}`.

Each worker process has **C fixed slots**, default 1, and one dispatcher. Set
`FLOWFORGE_WORKER_CONCURRENCY` to an unsigned decimal integer in **1..32**.
Each free slot receives one delivery, with no unbounded prefetch. Instances use
one UUID each and consumers `<worker_id>:<slot>` in a shared stream/group.
N processes hold at most N*C deliveries and claimed executions. Worker connection
limits are C+4 PostgreSQL and C+4 Redis sockets; budget other clients too.
Job + outbox creation is atomic. Publishing and marking outbox
publication are separate operations, so duplicate messages are expected.
The dispatcher reads at most 100 intents each second; it also republishes
still-QUEUED work after 30 seconds. PostgreSQL therefore reconstructs delivery
after a lost Redis stream or a worker crash before claim. No full payload goes
into Redis. The consumer group starts at `0`, receives one message at a time,
and ACKs after terminal or durable RETRYING persistence (or after rejecting a missing, malformed,
RUNNING or non-QUEUED Job).

An atomic `QUEUED -> RUNNING` claim commits an incremented attempt and lease.
Concurrent duplicate claims create one current owner. Renew and Finalize require
that owner, attempt number and a lease still valid at PostgreSQL time. Finalize
commits Job + Attempt and durable outcome together. `max_attempts` now limits
**total execution attempts**, including crashes and operational interruptions.
Only Claim consumes budget; duplicate delivery/replay/promotion never does.
Failures follow RUNNING -> FAILED -> RETRYING when retryable and budget remains,
or RUNNING -> FAILED -> DEAD_LETTER when permanent/exhausted. Intermediate FAILED
is applied in the transaction, not a required observable persisted state.

RETRYING has `retry_at` and no execution owner/lease. Default equal jitter uses
base=1s, max=30s: cap=min(max,base*2^(attempt-1)), actual delay uniform [cap/2,cap].
Saturating arithmetic prevents overflow. Set FLOWFORGE_RETRY_BASE_SECONDS (1..60)
and FLOWFORGE_RETRY_MAX_SECONDS (base..600). PostgreSQL clock determines schedule
and due time. Bounded concurrent promotion commits RETRYING -> QUEUED and dispatch
intent reset atomically; dispatcher publishes afterward. Schedule survives restart.

Each process registers a fresh UUID and sends bounded heartbeats. Each active
execution renews its lease; one reaper per process scans at most 100 expired
RUNNING Jobs. Recovery closes the old Attempt as FAILED/`lease_expired`, returns
the Job to budgeted RETRYING or DEAD_LETTER in one transaction. A due retry promoter resets dispatch intent; another
worker creates a new attempt. Stale owners cannot renew or overwrite its result.
All authority uses PostgreSQL time. Default lease/renew/heartbeat/offline/reaper
periods are **15/5/2/10/1 seconds**, configurable within validated bounds in
[worker operations](docs/worker-operations.md).

Heartbeat or renewal failure stops the pool; uncertain execution is left for
lease recovery without false success/ACK. A finalize failure also stops the pool;
the database determines whether a commit actually occurred. SIGINT/SIGTERM
interrupts SLEEP and attempts bounded retryable `execution_cancelled` settlement
then ACK, provided its lease is still valid. Expiry wins over late graceful
finalization. Recovery needs a healthy worker/reaper and reachable PostgreSQL;
it is **at-least-once delivery**, with no exactly-once side-effect guarantee.
Old Redis pending entries are retained; recovery publishes a fresh notification.
Crash/cancellation attempts respect the same max_attempts budget; N+1 cannot run.
Submission idempotency != exactly-once execution != exactly-once business side
effect. Lease fencing protects DB writes, not arbitrary external effects.
See [worker operations](docs/worker-operations.md) and
[ADR 0006](docs/decisions/0006-budgeted-retries-and-submission-idempotency.md).

Run `./scripts/phase5-smoke.ps1` in PowerShell for isolated real API/idempotency
and process-restart retry evidence. It builds a separate image and uses a generated
DB/key/API port; existing development services/data remain intact. Phase 3/4 smoke
scripts are historical fixtures for their tagged checkpoints, whose FAILED and
immediate-requeue contracts Phase 5 supersedes.

List accepts only `limit` (default 20, range 1–100) and optional `cursor`.
Unknown/repeated query parameters, invalid limits and malformed query encoding
return 400. Its response is:

```json
{"jobs": [], "next_cursor": null}
```

When another page exists, `next_cursor` is a string representing the **last
returned row**. Pass it unchanged to the next request:

```sh
curl 'http://localhost:8080/api/v1/jobs?limit=2'
# Copy next_cursor from the response:
curl 'http://localhost:8080/api/v1/jobs?limit=2&cursor=RETURNED_CURSOR'
```

The versioned, URL-safe cursor is bounded to 512 characters and validated; invalid
cursors return 400. Final/empty pages return null, and empty jobs is always `[]`.
Newer jobs inserted between pages do not shift the traversal of older rows.
This is not a database snapshot: later inserts behind the boundary may appear.
Cursors are unsigned boundaries, not authorization tokens. A structurally valid
constructed cursor is accepted; clients must treat its encoding as opaque.

Errors use `{"error":{"code":"JOB_NOT_FOUND","message":"job not found"}}`.
Codes distinguish `INVALID_JSON`, `INVALID_INPUT`, `INVALID_ID`, `INVALID_CURSOR`,
`BODY_TOO_LARGE`, `JOB_NOT_FOUND`, `IDEMPOTENCY_CONFLICT` and `INTERNAL_ERROR`. Dependency failures,
unexpected constraint failures and corrupt stored data return a generic 500;
raw driver errors and secrets are not sent to clients. Readiness failures return
a generic 503.

### Local Go development

Go commands do not load `.env`. Export its variables in your shell first. With
Go 1.26+ installed, start dependencies using `docker compose up -d postgres redis`,
then run `make migrate-up` and `make run-api`. On Windows without Make, run the
corresponding `go` commands shown in the Makefile.

Without a local Go installation, use the Linux tools container:

```sh
docker compose --profile tools run --rm tools
```

This executes formatting/state checks, vet, tests including real PostgreSQL
and Redis integration, and build. Integration tests create and drop a random isolated
schema, and verify `current_schema()` on every connection before test queries.
Redis tests use random `flowforge:test:<UUID>` keys and delete only those keys;
no FLUSHDB/FLUSHALL is used. Tests do not delete application data. PostgreSQL
tests require permission to create schemas.
Standalone `go test ./...` skips integration when neither
`FLOWFORGE_TEST_POSTGRES_URL` nor `FLOWFORGE_INTEGRATION=1` is set. With the latter,
normal application PostgreSQL configuration is used. CI supplies a dedicated
ephemeral PostgreSQL and Redis services and runs integration tests. With host-run
tests, set `FLOWFORGE_TEST_REDIS_ADDR` (and optional
`FLOWFORGE_TEST_REDIS_PASSWORD`) as well as the PostgreSQL test URL.

## Development Commands

| Command | Purpose |
|---|---|
| `make build` | Build API, worker and migration binaries in bin/ |
| `make test` | All tests (integration requires configured database) |
| `make test-unit` | Unit tests without external services |
| `make test-phase-state` | Focused state-validator tests without application services |
| `make validate-phase-state` | Validate the phase state, report identity, and Git evidence |
| `make run-api` / `make run-worker` | Run a process using exported configuration |
| `make docker-up` / `make docker-down` | Start/build or stop development environment |
| `make migrate-up` | Apply all pending migrations |
| `make migrate-down` | Roll back **one** migration; destructive to its table |
| `make fmt` / `make vet` | Format or statically check Go |

In Docker, migrations can also run with
`docker compose run --rm migrate /app/migrate up` (or `down`). Stop API/worker
before rolling back schema. Five `down` calls remove retry schema, workers, outbox, attempts and jobs.
Migration 000003 backfills dispatch intent for existing QUEUED Jobs. Starting the
Phase 2 worker therefore processes existing queued work; unsupported legacy
types become DEAD_LETTER under Phase 5. Migration 000004 adds liveness and expiry indexes; pre-lease
RUNNING Jobs get an immediately expired lease. Recovery still requires consistent
owned Attempt history, and stops on corrupt history. Do not roll back leases
while workers run; historical binaries do not enforce the new authority rules.
Migration 000005 adds retry schedule/budget constraints and global non-null key
uniqueness. Legacy duplicate keys atomically block upgrade without deleting,
merging or selecting a winner. Resolve them explicitly before upgrading; do not
use automatic cleanup. Legacy count >100 also blocks migration; over-budget
<=100 count freezes max_attempts at actual count, and exhausted QUEUED rows
normalize to DEAD_LETTER with all Attempt history intact. Old RETRYING rows get
a DB-time one-second schedule. Down removes new schema objects but never erases
history/restarts normalized work. [Upgrade preflight](docs/worker-operations.md#upgrade-preflight)
explains operator review and why mixed old/new processes are unsupported.

Migrations use version tracking, an advisory transaction lock, and one atomic
transaction per invocation. Applied SQL is immutable; use new versions for
future changes. Changing passwords in `.env` does not change an existing
PostgreSQL volume's database role password.

`docker compose down` preserves the PostgreSQL named volume. Explicitly removing
volumes deletes durable data. Redis uses a named volume, AOF `everysec` and
`noeviction`; its last second can still be lost on a crash. Durable queued-job
republication covers lost queue notifications. Redis streams, consumers and
outbox history have no automatic retention/cleanup yet. All ports bind to loopback.

## Current Status

### Implemented

- Go module, separate API / worker processes, structured JSON logging
- Job, attempt and worker models; centralized state graph and exhaustive tests
- PostgreSQL and Redis startup checks, connection cleanup, bounded DB operations
- Job create/get/list service and PostgreSQL repository
- Transactional up/down migrations, schema constraints and integration tests
- Health/readiness, bounded HTTP server settings, SIGINT/SIGTERM shutdown
- Docker development environment, Makefile and CI workflow
- Architecture, failure model, lifecycle, roadmap and ADR documentation
- Machine-readable phase tracking, independent report template, and state validator
- Exact Create field names, duplicate-envelope rejection and explicit null/default semantics
- Deterministic cursor pagination with same-timestamp tie breaking and bounded cursor parsing
- Canonical persisted create/read responses and corrupted-row rejection
- Expanded HTTP/PostgreSQL contract, boundary and failure tests
- Cursor ADR and explicit transaction decisions, with Create extended in Phase 2
- Manual/automation prompt-source tracking with strict protocol validation
- Atomic Job/outbox transaction and existing-QUEUED migration backfill
- Bounded dispatcher and database-driven queued-job republication
- Redis Streams consumer group, minimal messages and explicit ACK boundary
- Fixed worker slots (C=1..32), multiple processes and process UUID / consumer identities
- PostgreSQL atomic claim and transactional owner/attempt records
- Unified fail-fast supervision, shared cleanup window and joined shutdown
- Barrier/race tests and real two-process C=2 smoke with survivor continuation
- Bounded context-aware SLEEP, deterministic failures and shutdown persistence
- Real PostgreSQL/Redis E2E, duplicate/failure-window and isolated migration tests
- Persisted worker liveness, bounded heartbeat/reaper/renew loops and joined shutdown
- DB-time execution leases, stale-owner fencing and atomic expired-attempt settlement
- Concurrent reaper, transaction rollback and independent-process crash recovery tests

- Unified total Attempt budget, permanent/retryable failures and DEAD_LETTER exhaustion
- Durable DB-time RETRYING schedules and bounded equal-jitter exponential backoff
- Concurrent retry promotion with atomic dispatch-intent reconstruction
- Global exact-key submission idempotency, explicit 201/200/409 HTTP semantics
- Concurrent replay/conflict/migration tests and independent-process retry smoke

### Experimental

None. SLEEP crash recovery is tested; production hardening remains planned.

### Planned

Scheduling, priority, timeout/user-cancellation execution policies, DLQ management,
dashboard, metrics/tracing, failure injection, benchmarking and cloud deployment.

## Roadmap

Phase 1 delivers **Job Persistence + API Correctness**. Phase 2 implements
**Redis Queue + Single Worker Execution** with a durable database outbox.
Phase 3 implements **Multiple Workers + Bounded Concurrency**.
Phase 4 implements **Heartbeat + Lease + Crash Recovery**.
Phase 5 implements **Retry + Backoff + Jitter + Submission Idempotency**. Further phase scope requires external review and a prepared next prompt; Codex has not generated one.
See the [Phase 0–10 roadmap](docs/development-roadmap.md) and authoritative phase state.
The [Phase 1 report](docs/reports/phase-1-report.md) records its validation and Git checkpoint.
The [Phase 2 report](docs/reports/phase-2-report.md) records execution/durability evidence.
The [Phase 3 report](docs/reports/phase-3-report.md) records concurrency/process evidence.
The [Phase 4 report](docs/reports/phase-4-report.md) records lease/fencing/recovery evidence.
The [Phase 5 report](docs/reports/phase-5-report.md) records retry/idempotency/migration evidence.
This repository does not claim exactly-once execution. Delivery is at-least-once;
business side effects need their own idempotency safeguards.

## Development Phases / Automation

This README describes the **whole project**: its purpose, architecture, setup,
and current capabilities. Each phase has its own `docs/reports/phase-N-report.md`
for that phase's scope, tests, failures, limitations, and Git references. Both
documents are required at phase completion and are updated independently.
Keep reports from earlier phases.

- [Phase state](automation/state.json): machine-readable current phase and status
- [Phase reports](docs/reports/index.md): independent evidence and report index
- [Report template](docs/phase-report-template.md): required report sections
- [Automation protocol](docs/phase-automation.md): schema, completion gates, and handoff
- `automation/prompts/`: phase prompts written by external Automation

GitHub repository state is the shared automation source of truth. Automation
reads `status`, `report`, `last_processed_phase`, and `next_prompt` from state,
then reads the exact matching report. This README does not substitute for a
phase report. Completion is published only after tests, README, the independent
phase report and real Git evidence pass their gates. The separate infrastructure
report does not complete Phase 1.

FlowForge supports both manual and automation-generated phase prompts. The
current Phase 5 prompt is `automation`, at `automation/prompts/phase-5.md`.
Phase 1 used manual input, with no prompt file required.
Automated prompts must have an existing current-phase file.
`prompt_path` tracks the current phase's source; `next_prompt` tracks an externally
prepared next phase. Both sources use the same completion gates. Explicit user
instructions may supersede an unstarted automated prompt; Automation must not
silently change a phase already in progress. Full rules are in the protocol.

Validate with `go run ./scripts/validate-phase-state`; run focused tests with
`go test -count=1 ./scripts/validate-phase-state` or the matching Make targets.

## License

MIT — see [LICENSE](LICENSE).

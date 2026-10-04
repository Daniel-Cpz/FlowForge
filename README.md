# FlowForge

Distributed Job Processing Platform for backend, distributed systems, and cloud
engineering.

## Overview

FlowForge is a Go backend project exploring reliable asynchronous job processing.
The committed foundation persists and reads jobs; it does **not** execute them.
PostgreSQL is the durable source of truth. API and worker are separate processes
sharing a small modular codebase.

## Why FlowForge

The engineering problem is coordinating work despite crashes, retries, duplicate
delivery and changing load. Those guarantees need explicit state transitions,
durable records and testable failure handling before adding scheduling features.
No throughput, recovery-time or execution guarantee is claimed in Phase 0.

## Architecture

```text
Client -> net/http handlers -> Job service -> domain.Repository interface
                                                ^
                                                | implements
                                       PostgreSQL repository -> PostgreSQL

API process    -> PostgreSQL + Redis readiness
Worker process -> dependency checks -> wait for termination (no execution)
```

The composition root wires infrastructure into the service. Domain and service
do not import database or transport packages. Redis is infrastructure only.
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
automatically. Required dependency connections fail fast during process startup.
The migration service applies schema versions before API and worker startup.
The API's readiness check also detects missing jobs columns.

```sh
curl -i -X POST http://localhost:8080/api/v1/jobs \
  -H 'Content-Type: application/json' \
  -d '{"type":"example","payload":{"message":"persist only"}}'
curl http://localhost:8080/api/v1/jobs
# Replace UUID with the id returned above:
curl http://localhost:8080/api/v1/jobs/UUID
```

### Committed foundation API contract

This is the Phase 0 baseline contract. Phase 1 API correctness work is in
progress, including pending decoding and pagination changes. Its final contract
and completion evidence will be recorded in an independent phase report after
the phase's validation and commit gates pass.

| Method | Path | Behavior |
|---|---|---|
| GET | `/health` | Process alive, `200 {"status":"ok"}`; independent of dependencies |
| GET | `/ready` | PostgreSQL, Redis and jobs schema ready: 200; otherwise 503 |
| POST | `/api/v1/jobs` | Create a QUEUED job; 201 with Location header |
| GET | `/api/v1/jobs/{id}` | Job by UUID; 404 if absent, 400 if malformed |
| GET | `/api/v1/jobs?limit=20&offset=0` | Newest first by `(created_at, id)` descending |

Creation accepts `type` (1–128 characters after trimming), required `payload`
(any JSON value, including JSON null), `priority` (0–100, default 0),
`max_attempts` (1–100, default 3), `timeout` (**seconds**, 1–86400, default 300),
and optional nonblank `idempotency_key` (up to 255 characters). PostgreSQL JSONB
does not accept Unicode NUL/unpaired surrogate escapes; these return 400.
Request bodies are capped at 1 MiB (413); malformed JSON, trailing documents
and unknown fields return 400. State and execution metadata are server owned.
The decoder follows Go's standard JSON behavior: duplicate object keys use the
last value and field matching is case insensitive. Stricter decoding is Phase 1.

`attempt_count` starts at 0. Unset result, worker, lease and execution timestamps
serialize as null. Time strings are RFC3339 with optional fractional seconds in
UTC. A stored idempotency key does **not** suppress duplicate submissions.
Timeout and priority are stored metadata and are not enforced during execution.
Pagination accepts limit 1–100, offset 0–1,000,000. Results are not a snapshot;
concurrent inserts can shift offset pages. Empty results use `jobs: []`.

Errors use `{"error":{"code":"JOB_NOT_FOUND","message":"job not found"}}`.
Dependency failures during CRUD return a generic 500; raw driver errors and
secrets are not sent to clients. Readiness failures return a generic 503.

### Local Go development

Go commands do not load `.env`. Export its variables in your shell first. With
Go 1.26+ installed, start dependencies using `docker compose up -d postgres redis`,
then run `make migrate-up` and `make run-api`. On Windows without Make, run the
corresponding `go` commands shown in the Makefile.

Without a local Go installation, use the Linux tools container:

```sh
docker compose --profile tools run --rm tools
```

This executes formatting checks, vet, tests including real PostgreSQL integration,
and build. Integration tests create and drop a random isolated schema. They do
not delete application data. They require permission to create schemas.
Standalone `go test ./...` skips integration when neither
`FLOWFORGE_TEST_POSTGRES_URL` nor `FLOWFORGE_INTEGRATION=1` is set. With the latter,
normal application PostgreSQL configuration is used. CI supplies a dedicated
ephemeral PostgreSQL service and runs integration tests.

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
before rolling back schema. Two `down` calls roll back both application tables.
Migrations use version tracking, an advisory transaction lock, and one atomic
transaction per invocation. Applied SQL is immutable; use new versions for
future changes. Changing passwords in `.env` does not change an existing
PostgreSQL volume's database role password.

`docker compose down` preserves the PostgreSQL named volume. Explicitly removing
volumes deletes durable data. Redis persistence is intentionally disabled because
Phase 0 stores no application state there. All published ports bind to loopback.

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

Redis infrastructure available.
Redis-backed job queue is not implemented yet.
Worker process skeleton exists.
Job execution is not implemented yet.

### Experimental

None. Phase 0 contains no experimental scheduler or execution implementation.

### Planned

Redis job queue, worker execution, bounded concurrency, scheduling, heartbeat,
lease renewal, crash recovery, retry/backoff/jitter, idempotency and duplicate
detection, priority, timeout/cancellation execution policies, DLQ management,
dashboard, metrics/tracing, failure injection, benchmarking and cloud deployment.

## Roadmap

The current work is **Phase 1 — Job Persistence + API Correctness**: tighten input
semantics, expand database/API contract coverage, and decide pagination and
transaction boundaries. See the [Phase 0–10 roadmap](docs/development-roadmap.md).
This repository does not claim exactly-once execution. Future delivery is planned
as at-least-once; business side effects will need their own idempotency safeguards.

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
- `automation/prompts/`: next-phase prompts written by external Automation

GitHub repository state is the shared automation source of truth. Automation
reads `status`, `report`, `last_processed_phase`, and `next_prompt` from state,
then reads the exact matching report. This README does not substitute for a
phase report. Phase 1 is `in_progress`; no Phase 1 completion report or next
prompt is claimed. The separate infrastructure report does not complete Phase 1.

Validate with `go run ./scripts/validate-phase-state`; run focused tests with
`go test -count=1 ./scripts/validate-phase-state` or the matching Make targets.

## License

MIT — see [LICENSE](LICENSE).

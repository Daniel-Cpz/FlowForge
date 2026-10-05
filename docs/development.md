# Local development and migration guide

[Portfolio overview](../README.md) | [API contract](api.md) |
[Production runbook](deployment.md)

Run the commands below from the repository root unless a command changes directory.

## Database and configuration preflight

Start with a fresh, compatible database and free host ports. The retained
development fixture remains schema 4 with a legacy duplicate-key group;
migration 000005 rejects that data atomically. Do not start current API/Workers
against it or bypass the duplicate-key check. Follow the
[operator upgrade preflight](worker-operations.md#upgrade-preflight) before
reusing existing data. This documentation change performs no data repair.

## Getting started

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

## Local Go development

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

## Development commands

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
before rolling back schema. Eight `down` calls remove trace context, scheduling, execution control, retry schema, workers, outbox, attempts and jobs.
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
history/restarts normalized work. [Upgrade preflight](worker-operations.md#upgrade-preflight)
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

## Dashboard setup

The React/TypeScript Dashboard reads REST/PostgreSQL snapshots. WebSocket hints
use a separate `<FLOWFORGE_REDIS_STREAM>:ui:v1` Pub/Sub channel. Reconnection and
30-second reconciliation refresh visible resources; hints never deliver tasks
or rebuild authoritative state. Queue depth counts due QUEUED Jobs with budget,
including jobs without a matching capability Worker. Metrics use the separate
Prometheus/Grafana stack; the Dashboard links to its provisioned overview.

Use Node 24: `cd web && npm ci && npm run dev`, with a compatible API on 8080.
Vite proxies REST and WS. On a **fresh compatible DB**,
`docker compose --profile dashboard up --build` starts the local frontend too.
The retained development DB remains schema 4 with one legacy duplicate-key group;
do not run that upgrade command against it. `./scripts/phase8-smoke.ps1` creates,
tests and removes its own compatible DB/stream/channel and containers instead.
It uses real WS clients; browser E2E is NOT RUN.

This control plane has no authentication and is intended for loopback/local
demonstration. Optional public deployment uses Phase 10 gateway HTTPS/Basic Auth
and exact WS origins; application users/sessions/RBAC are still unimplemented.
See [Dashboard contract](dashboard.md), [frontend setup](../web/README.md) and
[ADR 0009](decisions/0009-dashboard-realtime-resync.md).

## Observability and measurement setup

On a fresh compatible DB, `docker compose --profile observability up --build --scale worker=2`
starts Prometheus at localhost:9090 and Grafana at localhost:3000/d/flowforge-overview.
API `/metrics` and internal Worker `:9091/metrics` use finite labels. Global PG
gauges must use max across API replicas; process counters use sum/rate. Keep these
unauthenticated endpoints internal/loopback. OTel export defaults disabled;
set `FLOWFORGE_OTEL_ENABLED=true` for internal OTLP/HTTP Collector debug output.
Trace context is internal schema-8 metadata, excluded from submission identity,
ordinary API JSON and Redis messages. Telemetry failure cannot grant execution
authority or change a committed outcome.

Use `./scripts/phase9-failure.ps1` and `./scripts/phase9-benchmark.ps1` for isolated
resources with finally cleanup and retained-data audit. The retained schema-4 DB
and its duplicate-key blocker are preserved. See [observability](observability.md),
[recorded baseline](benchmarks/phase-9-baseline.md) and [ADR 0010](decisions/0010-observability-cardinality-trace-isolation.md).
The SLEEP baseline applies only to its recorded machine, concurrency, code and
workload; it is not a production latency, scalability or reliability SLA.

## Production artifacts

Production Compose and deployment safeguards are documented in the
[deployment runbook](deployment.md) and [artifact bundle](../deploy/README.md).
They use separate resources from development. Actual SSH/cloud deployment
requires a subsequently authorized target; it has not been performed.

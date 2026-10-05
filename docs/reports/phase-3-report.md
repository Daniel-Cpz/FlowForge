# FlowForge Phase 3 Completion Report

## Phase

Phase 3

## Status

COMPLETED

Technical acceptance, targeted tests and independent-process smoke PASS.
Implementation/tag publication and final metadata evidence are recorded below;
a checkpoint cannot contain its own SHA. The claim state remained in_progress
until checkpoint/tag publication gates
passed; the follow-up metadata commit records the verified completed state.

## Summary

Multiple independent worker processes share the existing PostgreSQL truth and
Redis stream/group. Each process has C fixed slots, one UUID owner and one
dispatcher. Receive/Claim only occurs in free slots; execution and held delivery
are bounded by C. Existing claim/attempt and terminal-before-ACK transactions
are preserved without migrations. A process supervisor drains all slots on a
fatal error, panic or signal, joins them and its dispatcher before closing clients.

## Prompt Source

Prompt Source: automation
Prompt Path: automation/prompts/phase-3.md
Execution ID: phase-3-20261005T032320Z (log/report only, no new schema field).
Started at: 2026-10-05T03:23:20Z (Sydney 14:23:20).
Handoff: 0bc0e8becba3b33eaa3876b092da80c9a05b70a5.
Ownership claim: 8620d1b05190a80d7b308ed2a219631a07b0d408, pushed and reread
on the shared branch before development. External automation had set
last_processed_phase=2. User retry changed no scope. Day1 remains enabled;
Codex neither advances external review accounting nor generates Phase 4.

## Implemented

- FLOWFORGE_WORKER_CONCURRENCY: default 1, unsigned decimal 1..32, strict startup
  rejection of invalid/empty/signed/fraction/exponent/overflow/out-of-range input.
- C fixed consumer/executor loops, no unbounded prefetch or per-job goroutines.
  Each slot holds one delivery through bounded retry/execution/finalization/ACK.
- One per-process UUID and <worker_id>:<slot> Redis consumer identity; shared group.
- Shared concurrency-safe pgxpool/go-redis clients, stateless SLEEP and slog;
  atomic active_jobs, isolated per-slot Job objects and race-safe test doubles.
- Worker PostgreSQL connection max C+2; Redis PoolSize/MaxActiveConns both C+4.
  SLEEP never holds a transaction/connection. API keeps explicit limits of ten.
- One supervised dispatcher per process; duplicates from concurrent publication
  are safe under existing database claim and owner/attempt finalization predicates.
- Fatal Finalize/ACK or unexpected panic stops the whole pool with safe diagnostics.
  Business FAILED outcomes continue. Panic value/stack/payload never logged.
- Concurrent signal cleanup with one shared five-second cancellation window,
  bounded operation contexts and joined slots/dispatcher before resource closure.
- Structured process, slot, claim, finish, draining, stopped and pool_failed logs.
- Repeatable controlled two-container smoke, survivor continuation and SIGTERM.
- README, operations, architecture, lifecycle, roadmap, ADR and report navigation.

## Not Implemented

No required Phase 3 capability is intentionally omitted. Graceful shutdown cannot
fabricate terminal state when storage is unavailable. Remaining post-claim crash
and ambiguous-commit limits are explicitly retained, not recovered in this phase.

## Experimental

None. This is correctness validation of the SLEEP demo; no performance experiment
or production throughput/availability guarantee is claimed.

## Planned

Phase 4 heartbeat/liveness, lease policy, fencing and RUNNING crash recovery.
Later retry/idempotency, scheduling, priorities, general timeout/user cancellation,
DLQ, metrics/UI, admission control, retention, benchmarks and production deployment.

## Tests

### Environment and commands

Windows host has no Go installation. Existing Docker tools image uses
`go version go1.26.8 linux/amd64`; PostgreSQL 18 and Redis 8.2 Compose services
were available. `FLOWFORGE_INTEGRATION=1` was supplied by tools. Tests create a
random PostgreSQL schema with current_schema checks on every connection and
random Redis keys; no FLUSHDB/FLUSHALL or application-data deletion.

Go subcommands below ran with `docker compose --profile tools run --rm tools
sh -c '<commands>'` (some combined in one invocation). Outcomes:

| Command | Result |
|---|---|
| `go test -count=1 ./internal/config ./internal/service/execution ./internal/service/dispatch` | PASS |
| `go test -race -count=1 ./internal/config ./internal/service/execution ./internal/service/dispatch` | PASS; final 1.012s / 6.091s / 1.070s |
| `go test -race -count=1 ./tests/integration -run "Test(MultipleWorkerPoolsAndDispatchers\|TwoWorkersConcurrentClaimDuplicateACKOwnership\|AtomicClaimAndOwnerFinalize\|RedisOutageAndDispatcherRestart\|PublishMarkerFailureDuplicateAndRedisLoss\|ClaimAndFinalizeDatabaseFailures\|RunningPoisonUnsupportedAndInvalidSleep\|WorkerGracefulIdleAndSleepShutdown\|PostgresUnavailableDoesNotPublishOrAck)$"` | PASS, 3.156s, real services |
| `go test -race -count=1 ./tests/integration -run "Test(MultiplePoolUnavailableStoreAfterClaim\|MultipleWorkerPoolsAndDispatchers\|TwoWorkersConcurrentClaimDuplicateACKOwnership)$"` | PASS, 1.867s, real services |
| `go test -count=1 ./internal/config ./internal/app ./internal/infrastructure/postgres ./internal/infrastructure/redis` | Config PASS; app/postgres/redis have no unit test files, behavior covered through integration/smoke |
| `go vet ./internal/config ./internal/service/execution ./internal/service/dispatch ./internal/app ./internal/infrastructure/postgres ./internal/infrastructure/redis ./tests/integration` | PASS |
| `go build -o /tmp/flowforge-phase3-worker ./cmd/worker` | PASS; container temporary artifact |
| `go test -race -count=1 ./internal/service/execution -run "TestPoolCancellationInFlight(Claim\|Receive)$"` | PASS |
| `go test -count=1 ./internal/config` | PASS after environment-isolation adjustment |
| `go run ./scripts/validate-phase-state` | PASS for claim and prospective completed state with real checkpoint/tag; final state validated before metadata publication |
| `gofmt -l` on affected Go files | PASS (empty); files formatted with gofmt |
| `./scripts/phase3-smoke.ps1 -Concurrency 2` | PASS twice after harness corrections; final controlled-process evidence below |
| PowerShell parser on smoke script; `git diff --check` | PASS |
| Full `go test ./...`, full release regression, benchmark | NOT RUN; selected affected tests suffice, no release or SQL/migration changes |
| Fresh Phase 3 remote GitHub Actions result | NOT VERIFIED at report preparation; no inferred CI success |

No selected integration test was skipped. The initially run worker build without
`-o` produced a local binary which was removed; no binary is committed.

### Independent-process smoke

Final run started its isolated resources at 2026-10-05T03:43:03Z:
`ff_phase3_smoke_20261005034303_fa9b0c75` and
`flowforge:test:phase3:20261005034303_fa9b0c75`.
Actual command inside the script:
`docker compose -f docker-compose.yml -f <generated override> up --build -d --scale worker=2 migrate api worker`.
Both containers independently executed /app/worker with C=2 and the same storage,
stream and group. SQL observed four RUNNING Jobs across two owners; logs verified:

| Process worker_id | Claims | Observed max active_jobs | SIGTERM exit |
|---|---:|---:|---:|
| 9f908954-c62b-400b-8e4f-ab12272f1194 | 4 | 2 | 0 |
| e0ac1d2b-06d0-4bf2-a270-94f7e6ada080 | 10 | 2 | 0 |

16 Jobs: eight initial SLEEP(2000ms), four survivor SLEEP(100ms), four final
SLEEP(10000ms). Outcomes: 12 SUCCEEDED, two FAILED/execution_cancelled, two
unclaimed QUEUED with attempt_count=0; 14 Attempts, zero owner/status/result/
finish-time/attempt-number inconsistencies. First worker stopped idle. Survivor
then completed four new QUEUED jobs. Survivor stopped saturated with two active
and two queued; both active records persisted cancellation. No claim of crash
recovery or takeover of RUNNING work is made.

Each process logged one draining, two slot_stopped, one dispatcher_stopped and
one worker_stopped; no pool_failed, exit=0. Generated database/key were removed
only after all test processes stopped. Direct exact-name database/key checks
returned 0/0. Normal Compose API healthy and one worker restored; volumes retained.

Two initial harness runs failed their assertions: readiness incorrectly expected
`ok` instead of `ready`; then a missing active_jobs field was incorrectly treated
as negative by PowerShell. Both were corrected, resources cleaned and normal
services restored. These failed harness attempts are not recorded as PASS.
The final cleanup uses nested finally to attempt normal-service restoration even
if test-resource cleanup fails; final script rerun passed.

## Failure / Edge Case Validation

- C=1/2/4 barriers reached genuine simultaneous execution; Receive/Claim counts
  stayed C while saturated, then progressed after a slot was released. Atomic
  counts returned to zero; process/attempt owners consistent.
- Two Worker instances forced from one QUEUED snapshot to concurrent real SQL
  Claim: one executor/Attempt; loser ACKed only its different delivery while the
  winner's original pending message remained until terminal commit.
- Two real-service pools and concurrent dispatchers completed ten distinct Jobs
  despite duplicate notifications: eight success, two deterministic business
  failures, ten executions/Attempts with matching final data.
- Existing real tests revalidated outage publication retention, publish/marker
  failure, Redis stream loss/republication, poison/missing/RUNNING/terminal paths,
  invalid SLEEP/unsupported type, claim/attempt rollback and owner constraints.
- Finalize failure no ACK; post-commit ACK failure preserves terminal state and
  stops the pool without reexecution. Unit fatal tests joined peers/dispatcher.
- Panic uses static process-level failure; no payload/secret/stack output, no
  silent slot replacement. Claimed job remains pending rather than fabricated success.
- Four blocked Finalize cleanups started together and respected shared cancellation;
  bounded joins did not accumulate four serial five-second waits.
- Idle and saturated cancellation joined every slot; in-flight Receive returning
  after cancellation was not claimed/ACKed; successful in-flight Claim observed
  canceled SLEEP and persisted FAILED/execution_cancelled.
- Closing the real PostgreSQL store after two claims caused sanitized pool failure,
  bounded join and two retained RUNNING Attempts/pending messages, no false terminal ACK.
- Actual independent-container SIGTERM verified idle and saturated/active behavior,
  two per-process dispatcher lifecycles, safe shutdown and survivor work.

## Known Limitations

- No RUNNING crash recovery, leases, heartbeat, stale-owner fencing or pending
  reclaim. Ambiguous Claim/Finalize commit responses need later recovery policy.
- Five-second shared finish window is not a universal five-second wall-clock exit
  guarantee: existing rollback has its own bounded five seconds; network calls
  can race with cancellation. Slots run concurrently and Compose grants fifteen.
- Executor/adapters must honor context; only trusted stateless SLEEP is supported.
  There is no arbitrary plugin isolation, HTTP_REQUEST/shell/container execution.
- Local N*C execution bounds do not cap API admission, global backlog, stream/
  consumer/outbox retention or database server total connections. Distribution and
  completion order need not be uniform/FIFO. No throughput or exactly-once promise.
- Smoke script is local, assumes standard flowforge role/database and create/drop
  permission, and temporarily restarts development API/workers. It is not deployment.
- Secret-safe logs omit raw driver error details; no production diagnostics or SLA.

## Git

- Branch: codex/phase1-api-correctness.
- Ownership commit: 8620d1b05190a80d7b308ed2a219631a07b0d408 (pushed/verified).
- Implementation checkpoint: cfe1988f3a6e5c3022464ee33fa18e4e1945aab3 (implementation + report).
- Annotated tag: phase3-multi-worker; tag object 2d69d132b257a3e9c0e27bf842970d9db72e82e0; target matches checkpoint.
- GitHub push result: implementation + tag atomic push PASS, remote refs verified at 2026-10-05T03:49:37Z. Completion metadata is committed/pushed next; its SHA is reported in the Codex completion log, not self-embedded here.
- State commit points to the implementation/report checkpoint, not its metadata commit.
- Remote main verified unchanged at aa96182a037bfc502927125246e07733d0e8dbd3. No force push/history rewrite/merge/deployment.

## Documentation Updated

README.md; docs/worker-operations.md; docs/architecture.md; docs/job-lifecycle.md;
docs/development-roadmap.md; docs/decisions/0004-fixed-worker-pool.md and ADR index;
docs/phase-automation.md current-phase example; docs/reports/index.md; this report;
.env.example and Compose concurrency configuration. Historical reports, prompts
and migrations preserved. Schema_version=1 is unchanged.

## Next Recommended Phase

Phase 4 — Heartbeat + Lease + Crash Recovery, recommendation only.
No Phase 4 prompt generated or Phase 4 execution started. Wait for external review.

## Notes

Correctness > Feature Count; Reliability > UI Complexity. Last_processed_phase
stays 2 and next_prompt stays null at completion. Git metadata/state checks and
publication timestamps are recorded here by the follow-up metadata commit. No host
runtime installation, unrelated repository writes, global Git config changes,
secrets or generated artifacts are part of the implementation commit.

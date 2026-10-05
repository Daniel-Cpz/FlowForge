# FlowForge Phase 4 Completion Report

## Phase

Phase 4

## Status

COMPLETED

Technical acceptance and final independent-process smoke PASS. This report is
prepared in the implementation checkpoint with Git publication fields pending;
automation state stays in_progress until checkpoint/tag publication and state
validation pass. A metadata-only follow-up records verified Git evidence. A
checkpoint cannot contain its own SHA.

## Summary

PostgreSQL now grants DB-time execution leases and persists process liveness.
Renew/Finalize require current owner, Attempt and an unexpired lease. Bounded
concurrent reapers close expired Attempts with lease_expired evidence and restore
Job QUEUED plus dispatch intent in one transaction. A healthy worker takes over
with a higher Attempt number; a late owner cannot overwrite its result. Fixed
slots remain bounded, and heartbeat/reaper/renew lifecycles join on shutdown.

## Prompt Source

Prompt Source: automation
Prompt Path: automation/prompts/phase-4.md
Execution ID: phase-4-20261005T040022Z (log/report only; schema_version remains 1).
Started at: 2026-10-05T04:00:22Z, Sydney 15:00:22.
Previous checkpoint: cfe1988f3a6e5c3022464ee33fa18e4e1945aab3.
External handoff: ec53a1652b1dba1f180192cc2ee8e390d5d642e5.
Ownership claim: 7eff93338ef9646a24355136b36896d546c0ff9b; pushed and reread on
the shared branch before implementation. External automation had processed Phase
3 and supplied exactly Phase 4. No manual scope changes or Phase 5 prompt.

## Implemented

- Validated whole-second lease/renew/heartbeat/offline/recovery config. Defaults
  15/5/2/10/1; bounds and >2x update margins documented in worker operations.
- Append-only migration 000004: worker registry, bounded-count/status constraints,
  partial heartbeat/expiry indexes and expiry backfill for legacy RUNNING Jobs.
  Historical migrations/Reports/Prompts preserved.
- Fresh process UUID registration; DB-time heartbeat IDLE/BUSY samples, DRAINING
  and OFFLINE with graceful_shutdown/fatal_error/heartbeat_expired evidence.
  Stale or OFFLINE identity cannot reactivate; restart gets a new identity.
- Claim atomically establishes owner, increasing Attempt and DB lease; fresh
  registry required. Worker-row share locks serialize Claim/Renew with offline
  detection. Finalize/renew guard RUNNING + owner + Attempt + DB-valid lease.
- Recovery locks at most 100 expired rows using FOR UPDATE SKIP LOCKED and exact
  selected owner/Attempt/expiry conditions. One transaction closes old Attempt
  FAILED/error/result=lease_expired, clears current Job execution metadata, sets
  QUEUED and inserts/resets outbox publication. Intent failure/corruption rolls back.
- Separate expiry-guarded domain operation preserves the ordinary transition
  graph. Crash attempts may exceed stored max_attempts; integer sequence overflow
  guarded. Terminal business FAILED is not retried.
- Per-process one heartbeat, one recovery loop and one dispatcher with C fixed
  slots and <=C renewers. PG cap C+4, Redis cap C+4; API remains ten each.
- Renewal failure cancels local execution without Finalize/ACK; heartbeat failure
  drains the pool. Recovery/offline scans log failure and retry bounded cycles.
  Cancellation happens before DRAINING DB writes. Panic-safe renew join and
  stale-finalize rejection logging; no raw driver errors/payload/stack/secrets.
- Structured heartbeat, lease_acquired, lease_renewed, lease_expired,
  recovery_requeued, stale_finalize_rejected, worker_offline and loop-stop events.
- Independent-process smoke for SIGKILL, paused old owner, genuine network
  disconnect, and active graceful SIGTERM. README/ADR/lifecycle/operations updated.

## Not Implemented

No required Phase 4 capability intentionally omitted. DB fencing does not
prevent arbitrary external effects. Standalone diagnostic Handle registers and
renews but does not own process heartbeat/reaper loops; production uses Run.
No pending-entry reclaim or automatic history cleanup is added.

## Experimental

None. This is correctness validation of a context-aware SLEEP demonstration,
not a production recovery SLA or performance experiment.

## Planned

Phase 5 business retry/backoff/jitter, unified max_attempts accounting and
submission idempotency. Later scheduling/capabilities, priority, general timeout
or user cancellation, DLQ, dashboard/metrics/tracing, admission control, retention,
benchmarking, production deployment. No planned functionality is claimed implemented.

## Tests

### Environment and executed commands

Existing Docker Go tools: Go 1.26.8 linux/amd64; real PostgreSQL 18 and Redis 8.2.
Host has no Go runtime; no runtime was installed. Tools supplies
FLOWFORGE_INTEGRATION=1. Integration uses random isolated schemas with
current_schema verification on every connection, and random Redis keys. No test
was skipped, no FLUSHDB/FLUSHALL or normal application-data deletion.

Go commands ran in existing tools via docker compose --profile tools run --rm
tools sh -c '<commands>'; validator additionally used process-local Git safe.directory.

| Command | Result |
|---|---|
| go test -count=1 ./internal/domain/... ./internal/config ./internal/service/execution ./tests/integration | Domain/config and real integration PASS; initial lifecycle harness failed, fixed as described below |
| go test -race -count=1 ./internal/service/... ./internal/infrastructure/... ./tests/integration | PASS before final review and again after final lifecycle changes |
| go test -count=1 ./... | Final PASS; execution 5.340s, integration 8.722s, state-validator tests 0.201s |
| Final affected race command above | PASS; dispatch 1.072s, execution 6.359s, job service 1.009s, real integration 10.096s; adapters covered through integration |
| go vet ./...; go build ./... | PASS, final combined invocation |
| go run ./scripts/validate-phase-state | PASS for claimed in_progress state; prospective and published completion checked during Git publication |
| gofmt -l cmd internal migrations tests scripts/validate-phase-state | PASS, empty after formatting |
| ./scripts/phase4-smoke.ps1 | Initial SIGKILL/pause/graceful PASS; expanded network run cleanup failed; final corrected four-case smoke and cleanup PASS, exit 0 |
| PowerShell parser on smoke script; git diff --check | PASS |
| Benchmark, external side-effect exactly-once test, release/deployment test | NOT RUN; out of scope |
| Fresh Phase 4 GitHub Actions result | NOT VERIFIED at report preparation; local PASS does not imply CI completion |

Two early test-development issues were corrected before final PASS: the existing
Claim barrier wrapper embedded a narrow Store interface and needed its repository
methods forwarded for lease registration; a lifecycle test awaited joined Run
using its already canceled execution context and now uses a fresh bounded join
context. These failed runs are not treated as passing. Final review additionally
moved DRAINING persistence after immediate cancellation and rejects no-op registry
updates instead of logging false success; final tests/smoke use that code.

The first network-disconnect smoke passed execution assertions but DROP DATABASE
failed because three server-side test sockets remained after the process exited.
Normal services were restored by nested finally; cleanup was then completed for
that exact generated database/key. The script now terminates sessions only for
its validated disposable database after all test containers stop, before DROP.
That incomplete run is not treated as full smoke PASS; corrected final evidence
and resource cleanup are required before publication.

### Final independent-process smoke

Run started with generated resources at 2026-10-05T04:36:20Z:
ff_phase4_smoke_20261005043620_919182f0 and
flowforge:test:phase4:20261005043620_919182f0.
Actual command uses the generated override with up --build -d --scale worker=2
migrate api worker. Two independently running /app/worker containers share DB,
stream and group, each C=1. Lease/renew/heartbeat/offline/reaper=3/1/1/4/1 seconds.

| Case / process owner | Observed result |
|---|---|
| SIGKILL: fabfba72-255f-4834-a1ac-7b5a528855e5 | Exit 137, no draining/job_finished; old Attempt FAILED/lease_expired; another owner completes 10s SLEEP on Attempt 2 |
| Paused owner: 025b432a-f51f-48ca-bfa8-e09d5e5939df | Paused through expiry/takeover, resumed after Attempt 2 success; exit 1, old history/new result preserved |
| Real network loss: bc189d4b-0b8c-45be-bcf7-811f5ff9de9a | Disconnected from its sole Compose network; heartbeat/renew error, no job_finished, exit 1; another owner completes Attempt 2; exact network restored |
| Graceful survivor: 02d79971-c7eb-4bff-9194-f17bf053b553 | Active SIGTERM exit 0, FAILED/execution_cancelled on Attempt 1; OFFLINE/graceful_shutdown and every loop joined |

Final totals: four Jobs, seven Attempts, three SUCCEEDED Jobs, one
FAILED/execution_cancelled Job, three historical FAILED/lease_expired Attempts,
zero RUNNING Jobs. Successful takeover executions last 10s, proving renewal
beyond their original 3s leases. Restart generates new owner identities. Every
old expired Attempt retains its reason/finish/result; current Job/Attempt owner,
status, result and finished_at match. Expired process registry stays OFFLINE with
heartbeat_expired even if that old process returns.

All smoke services stopped before deleting only its generated DB/key; network
restored, no paused container retained. Exact-name database/key checks for both
the failed network run and final corrected run returned 0/0. Normal API ready,
PostgreSQL/Redis healthy and one worker restored; volumes/data preserved.
Final source prints its PASS summary after cleanup/restoration, with PowerShell
syntax verified; this output-order refinement does not alter tested recovery.

## Failure / Edge Case Validation

- Claim-after-crash-before-execution: real repository Claim without invoking an
  executor, then forced DB expiry recovers; separate real process SIGKILL during
  SLEEP validates abrupt termination. No claim of proving the precise tiny
  before-Execute process-kill timing window.
- Invalid owner/Attempt, expired lease even before recovery, and old owner after
  new Claim cannot renew or finalize. New owner success preserves old Attempt.
- Eight concurrent real reapers forced on one expired Job produce exactly one
  effective transition. Unexpired leases produce zero recoveries.
- Dispatch intent constraint failure rolls back Job/Attempt recovery; missing
  old Attempt returns corruption and preserves RUNNING. Scan limits 0/101 rejected.
- Stale heartbeat cannot reactivate even before detection; OFFLINE registration,
  heartbeat, Claim/Renew rejected, while a new UUID can register. Active count
  exceeds capacity rejected. Existing crash reason cannot be overwritten by stop.
- Real recovered-intent publish failure leaves marker null; post-publish marker
  failure remains reconstructible. Deleting only that test Redis key and aging
  the marker causes republication and successful Attempt 2 execution.
- Closing real PG pool makes heartbeat/renew/reaper/offline operations fail,
  without fabricated success. Actual isolated worker network disconnect demonstrates
  process-level failure/takeover with the real adapters and unreachable services.
- Lifecycle fault tests: renew/heartbeat failure, retrying recovery failure,
  executor/renew panic; loops and slots join, no continued operations after Run,
  no secret leakage. Uncertain renewal has zero terminal writes and ACKs.
- 4.2s real SLEEP completes with one Attempt under a 3s lease; graceful stop records
  distinct registry reason. Expired Finalize/recovery race rejects graceful
  cancellation write and leaves Job QUEUED with lease-expiry evidence.
- App readiness includes worker registry schema. Migration up/down preserves
  Job/Attempt data and backfills/reclaims consistent pre-lease RUNNING records.
- Existing API, duplicate Claim/ACK, outbox, poison delivery, business failure,
  concurrent pools, cancellation and SQL failure tests pass in full regression.

## Known Limitations

- At-least-once delivery, no exactly-once external effects. A paused/partitioned
  owner may have performed a side effect; fencing protects DB results only.
- Progress requires a healthy reaper/dispatcher and reachable dependencies. No
  healthy worker means waiting for one to return; no timer-value SLA is claimed.
- Ambiguous Claim/renew/Finalize/recovery commit responses are not guessed as
  success. PostgreSQL truth may be RUNNING, terminal or recovered; use database
  records/history. A restored intent handles delivery, not effect deduplication.
- Crash attempts can exceed max_attempts; business FAILED is not retried. Phase
  5 must define accounting. Exhausting PostgreSQL integer sequence requires repair.
- Corrupt history rolls back a bounded batch and may delay other expired work;
  operator repair is needed. Registry/Attempt/Redis/outbox history has no cleanup.
- Finalized leases remain audit metadata. Registry active count is a liveness
  sample, not scheduling authority. DB clock changes/latency/pauses affect leases.
- Shared finalization cancellation is 5s, rollback up to 5s separately, registry
  stop up to 3s; no universal five-second wall-clock exit promise. Trusted SLEEP
  and adapters must honor context. Mixed pre-lease/new workers unsupported.
- Local smoke assumes standard flowforge role/database and create/drop permission,
  temporarily restarts development services, and manipulates only its test workers.
  No production authentication, orchestration, retention or throughput claim.

## Git

- Branch: codex/phase1-api-correctness.
- Ownership commit: 7eff93338ef9646a24355136b36896d546c0ff9b, pushed/verified.
- Implementation checkpoint: PENDING_CHECKPOINT (filled by follow-up metadata).
- Annotated tag: phase4-lease-recovery, publication/target verification pending.
- GitHub push: pending checkpoint/tag and completion metadata publication.
- State commit references the implementation/report checkpoint, not metadata commit.
- main must remain aa96182a037bfc502927125246e07733d0e8dbd3; no merge/force/history rewrite.

## Documentation Updated

README; architecture; job-lifecycle; worker-operations; development-roadmap;
ADR 0005 and index; report index and this independent report; phase-automation
current-phase example; .env.example and Compose policy settings. Historical SQL,
reports and prompts preserved; schema_version=1 unchanged.

## Next Recommended Phase

Phase 5 — Retry + Backoff/Jitter + Idempotency, recommendation only. Wait for
external review and its prepared prompt. Codex did not generate or start Phase 5.

## Notes

Correctness > Feature Count; Reliability > UI Complexity. Completion keeps
last_processed_phase=3 and next_prompt=null for external automation. No host
runtime installation, unrelated writes, credentials, generated binaries or
smoke artifacts are included. Git/state publication evidence is filled by the
metadata-only follow-up and its SHA appears in the Codex completion log.

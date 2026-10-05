# FlowForge Phase 7 Completion Report

## Phase

Phase 7

## Status

COMPLETED

## Summary

Delayed Jobs, canonical Worker/Job capabilities and fixed-interval recurring
templates compose with PostgreSQL-authoritative priority, retry, lease, timeout,
cancellation and DLQ. Ordinary Jobs/Attempts/dispatch remain the sole execution
pipeline. All implementation, regression, race and isolated process acceptance
gates passed. Completion publication follows the checkpoint/tag metadata protocol.

## Prompt Source

Prompt Source: automation
Prompt Path: automation/prompts/phase-7.md
Execution ID: phase-7-20261005T064310Z
Started At: 2026-10-05T06:43:10Z
Previous checkpoint: 3673d56dbf466241225ebe383d47c10d8b60d246
Ownership commit: 791caf62dc09b4911a5bea7468bba4f3d0a443fc, pushed/read back before edits.
No manual scope changes or future-phase prompt generation.

## Implemented

- Optional scheduled_at, UTC microsecond normalization, PG-time dispatch/Claim
  eligibility and zero Attempt/budget before due. Past/null/missing semantics;
  queue wait excluded from execution timeout.
- Shared bounded ASCII capability parser: trim/lowercase, deduplicate/sort,
  1–32-character tokens, at most 16 input items; reject malformed/control tokens.
  Empty requirements are unrestricted.
- FLOWFORGE_WORKER_CAPABILITIES config/Compose integration, immutable registry
  capability set per UUID, canonical DB checks, preserved heartbeat and logging.
- Claim checks live Worker capability superset and ranks only its due/compatible
  backlog. Incompatible high priority cannot block eligible lower work. Rejected
  notification creates no Attempt; durable intent reconstructs capable delivery.
- Canonical keyed identity includes scheduled_at and requirements; equivalent
  offset/set replay and different time/set conflict. Existing lifecycle/redrive
  continues to compare original submission budget.
- POST /api/v1/schedules, GET /api/v1/schedules/{id}, POST .../{id}/cancel.
  ACTIVE/CANCELLED fixed interval 1..604800, flat validated Job template, optional
  first-run time defaulted by PG, no submission key reuse.
- Bounded <=100/3s materializer in each existing maintenance loop. SKIP LOCKED
  row coordination and one transaction for ordinary occurrence Job, intent and
  cursor advancement. Unique schedule_id/scheduled_for and foreign key fence
  duplicates across schedulers/restarts.
- Coalesced missed runs: one oldest due occurrence per schedule/pass, skip middle
  intervals and advance on the original grid to the first future DB-time boundary.
- Idempotent schedule cancellation serialized with materialization; existing Jobs
  unaffected. Job cancel leaves parent ACTIVE; retry/redrive retains occurrence,
  time, priority and capability requirements.
- Append-only 000007 migration, canonical storage/time/status/interval integrity,
  historical migration regression and isolated schema-7 real-service acceptance.

## Not Implemented

No required Phase 7 acceptance item remains unmet. Cron, schedule listing/editing/
pause/resume, routing topology, independent scheduler microservice and UI are
outside scope. Retained legacy development data was not repaired or upgraded.

## Experimental

None. SLEEP remains the bounded demonstration executor; no production SLA claimed.

## Planned

Existing roadmap Phase 8 Dashboard/WebSocket, then telemetry/cloud work, requires
external review and a prepared prompt. Capability routing/fairness, cron/editable
templates, priority aging, persistent logs and retention policies remain future work.

## Tests

### Tests Executed

Environment: existing Docker Go 1.26.8 Linux tool image; real PostgreSQL/Redis
integration uses verified random schemas/stream keys, with FLOWFORGE_INTEGRATION=1.
Normal application data and services remain untouched.

- Initial affected unit check: docker compose --profile tools run --rm tools
  sh -c 'gofmt -w internal migrations && go test ./internal/...'. PASS.
- New parser/HTTP + all integration: go test -count=1
  ./internal/domain/capability ./internal/transport/http/handler ./tests/integration.
  PASS, integration 18.455s.
- Full gate: docker compose --profile tools run --rm -e GIT_CONFIG_COUNT=1
  -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0=/src tools
  sh -c 'gofmt -w internal tests/integration && sh scripts/check.sh'.
  PASS: format, state validator, go vet ./..., go test -count=1 ./...,
  go build ./...; integration 19.008s.
- Race: docker compose --profile tools run --rm tools go test -race -count=1
  ./internal/domain/capability ./internal/service/... ./internal/infrastructure/...
  ./tests/integration. PASS, integration 21.183s, no race reports.
- Added config malformed-input regression: gofmt -w internal/config &&
  go test -count=1 ./internal/config. PASS.
- Explicit due-high-GPU/low-CPU interaction: go test -race -count=1
  ./tests/integration -run TestDueHighGPUStillAllowsLowerCPU. PASS, 1.130s.
- PowerShell ./scripts/phase7-smoke.ps1. PASS, real independent API + CPU/GPU
  Worker processes on generated schema-7 DB/stream/loopback port.
- git diff --check and staged content/historical-source integrity inspection;
  prospective/final completed-state validator are final publication gates.

### Test Results

PASS. No required test skipped. Packages marked [no test files] are covered by
the real repository/integration and process tests, not counted as standalone tests.
Smoke evidence: schema_version=7, jobs=5, attempts=5, succeeded=5,
occurrences=2, workers=2. Generated DB/key/containers removed. Retained audit
unchanged: legacy_duplicate_groups=1, schema_version=4.

## Failure / Edge Case Validation

- Future dispatch/Claim reject with zero Attempts; PG due boundary and past
  eligibility; cancellation before due. Equivalent timestamp offsets/canonical
  capability sets replay; different time/requirement conflict.
- CPU vs GPU, combined capabilities, incompatible priority does not block CPU;
  canonical registration, immutable set, malformed config/Job requirements,
  fresh constructor UUID, stale/OFFLINE fence and overlapping Worker claim race.
- No capable Worker retains QUEUED/budget 0 and ACKs only notification; later
  GPU Worker succeeds through retained intent. Integration accelerates only its
  isolated publication marker; real smoke uses actual reconciliation.
- Eight concurrent materializer calls generate one oldest due occurrence;
  restart via a fresh repository sees no repeat. Direct duplicate occurrence
  INSERT is rejected by PostgreSQL. 100-row batch bound, SKIP LOCKED progress,
  future template exclusion and missing-interval skip behavior validated.
- Intent trigger fault after occurrence insert rolls back Job and cursor. A
  random-schema trigger marks its own PG backend; termination during pre-commit
  intent insert proves real connection-crash rollback and successful retry.
  Fresh repository after committed materialization proves durable no-replay.
- Injected unavailable publisher after PG commit preserves unpublished intent;
  subsequent Redis duplicate deliveries yield one ordinary execution Attempt.
  Existing real Redis outage/restart regressions also pass.
- Ten cancellation/materialization races produce zero or one prior occurrence,
  then no future occurrence. Repeated cancel is idempotent; existing Job states
  are preserved. Job cancel does not cancel template.
- Capability-constrained recurring Job TIMED_OUT -> retry -> DEAD_LETTER ->
  redrive retains requirement and occurrence identity; CPU cannot claim any
  retry/redrive. Schedule cancellation does not rewrite that Job or template.
- Migration up/down preserves historical Jobs/Attempts; legacy empty capability
  registration remains compatible. Array order/duplicates/null/invalid tokens,
  infinity time, unpaired occurrence and interval bounds rejected by DB checks.
- Existing priority, timeout, cancel, lease, retry, idempotency, DLQ and failure
  regressions pass. Historical migration SQL and earlier reports/prompts unchanged.
- Isolated process smoke: 5s scheduled wait with timeout=1 executes successfully
  after due, CPU/GPU matching with distinct processes, two unique occurrences,
  repeated schedule cancellation, no new occurrence across two 4s intervals,
  existing materialized Job states unchanged. Scoped cleanup/retained audit PASS.

## Known Limitations

- Global stream mismatch can add 30s reconciliation and repeated mismatch delays;
  no latency/fairness guarantee or capability router. Priority may starve eligible
  lows; global Claim arbitration favors correctness over maximum throughput.
- Recurring is fixed interval, with coalesced missed intervals. Healthy Worker
  maintenance and PostgreSQL availability are required. Template create has no
  submission idempotency guarantee; clients must track returned schedule IDs.
- Schedule cancellation affects only unmaterialized occurrences; current Job
  execution remains independently cancellable. Cancellation of execution remains
  cooperative and lease-fenced. External effects are not exactly-once.
- Retained development DB stays schema 4 with one legacy duplicate-key group.
  Explicit separately authorized operator resolution is required before 000005+;
  isolated schema-7 acceptance does not deploy or silently repair historical data.
- Down 000007 loses templates/eligibility/capability/attribution metadata while
  retaining Jobs/Attempts. Stop processes before up/down; no mixed-binary promise.
- Local demonstration only: no auth/tenant isolation, job log store, retention,
  benchmarks, telemetry, dashboard or production orchestrator.

## Git

### Git Branch

codex/phase1-api-correctness

### Git Commit

Pending implementation/report checkpoint commit. The completion metadata follow-up
records its real SHA; a commit cannot contain its own hash. State remains
in_progress until validated checkpoint/tag publication.

### Git Tag

Pending annotated phase7-scheduling-capabilities at the implementation/report checkpoint.

### GitHub Push Result

Pending checkpoint/tag and completion metadata push/readback. No force/reset,
automatic main merge or historical rewriting. main remains
aa96182a037bfc502927125246e07733d0e8dbd3 pending final remote verification.

## Documentation Updated

README.md; docs/architecture.md; docs/job-lifecycle.md; docs/worker-operations.md;
docs/development-roadmap.md; docs/scheduling.md; docs/reports/index.md; this report;
docs/decisions/0008-time-capabilities-recurring-schedules.md and ADR index;
internal/scheduler/README.md; .env.example/Compose configuration.
Historical SQL, reports, prompts and ADRs retained unchanged.

## Next Recommended Phase

Existing roadmap Phase 8 Dashboard/WebSocket, recommendation only. Wait for
GPT/external review and its next prepared prompt; none generated by Codex.

## Notes

Execution ID is recorded in the conversation/report; schema 1 forbids extra state
fields. Completion keeps last_processed_phase=6 and next_prompt=null. Existing
Day1 heartbeat continues this chat; no separate future task/automation created.

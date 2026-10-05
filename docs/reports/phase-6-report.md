# FlowForge Phase 6 Completion Report

## Phase

Phase 6

## Status

COMPLETED

Technical acceptance, isolated smoke and implementation/tag publication PASS. Claim remained in_progress through checkpoint publication. This metadata follow-up records the real checkpoint and publishes completed state only after its prospective validation.

## Summary

PostgreSQL now arbitrates non-preemptive priority Claims, while Worker contexts
enforce attempt deadlines. Durable user cancellation coordinates API, renewal,
fenced completion and lease recovery. Minimal DLQ APIs list/inspect/redrive with
explicit extra budget and preserved history. Original keyed submission identity
survives administrative budget changes. No Phase 7 implementation or prompt.

## Prompt Source

Prompt Source: automation
Prompt Path: automation/prompts/phase-6.md
Execution ID: phase-6-20261005T060517Z (report/log only; schema_version remains 1).
Started: 2026-10-05T06:05:17Z (Sydney 17:05:17).
Previous Phase: 5, checkpoint 984014f7980c4205f60789974b39702dfc1770e6.
External reviewed handoff: 4076f581ea58c59557c193fa03f55765d6b6a862.
Ownership claim: 3b39b2516561b1b0a5a1bb4fa69f7ec4c064e48f,
pushed and reread before development. No manual scope changes.

## Implemented

- Priority pending order and conditional Claim rank: priority DESC / created_at
  ASC / UUID ASC, eligible QUEUED with remaining budget. Short transaction advisory
  lock serializes Claim decisions, not execution; SQL rechecks precedence. Lower
  ranked deliveries create no Attempt/change/budget consumption. Three 250ms peer
  waits then notification-only ACK use existing 30s durable reconciliation.
- Non-preemption, deterministic ties, concurrent unique ownership, retry promotion
  and explicit redrive participating in normal priority eligibility.
- Per-attempt context timeout from executor invocation. SLEEP cooperatively stops;
  renewer stops/joins. TIMED_OUT Attempt has execution_timeout/error/result/end.
  Job graph RUNNING -> TIMED_OUT -> RETRYING/DEAD_LETTER follows total budget and
  jitter policy; TIMED_OUT is no longer an ordinary terminal Job graph state.
  Existing owner/attempt/unexpired-lease fences and failure/no-ACK behavior remain.
- POST /api/v1/jobs/{id}/cancel: QUEUED/RETRYING atomically CANCELLED, clears
  schedule and removes intent; RUNNING persists cancel_requested_at without falsely
  claiming execution stopped. Renew observes request, cancels executor, joins and
  settles fenced Job/Attempt CANCELLED/user_cancelled. Row lock makes committed
  request win late local success/timeout. Recovery honors crash-after-request,
  no retry or new Attempt. CANCELLED repeat 200; other terminal conflict 409;
  missing 404; malformed ID/body/query rejected with stable envelope.
- GET /api/v1/dead-letter: only DEAD_LETTER, existing bounded creation-time cursor
  order, same-timestamp UUID ties and exclusive continuation, no snapshot claim.
- GET /api/v1/jobs/{id}/attempts: complete history ordered by attempt_number,
  capped by total budget 100; empty [] and missing 404. Worker/status/start/end/
  stable error/result available, raw legacy error text redacted.
- POST /api/v1/jobs/{id}/retry: explicit DEAD_LETTER-only transaction grants
  max_attempts+1 up to 100, preserves count/Attempt history, clears terminal
  fields and resets intent. Next Claim alone creates Attempt. Concurrent second
  redrive conflicts; cancel/redrive linearize safely; failed intent reset rolls
  back state/budget. Ordinary DEAD_LETTER graph remains terminal.
- Append-only 000006: cancel_requested_at, original submission_max_attempts,
  cancellation state constraint, original-budget bounds and partial priority/DLQ
  indexes. Canonical replay compares immutable original budget, including after
  redrive. Historical 000001–000005 SQL unchanged; up/down tests retain evidence.
- Whole-project README, architecture/lifecycle/operations/roadmap, report/ADR
  indexes, ADR 0007, scheduler navigation and repeatable isolated smoke.

## Not Implemented

No authorized Phase 6 acceptance feature is omitted. Retained development DB
upgrade is not performed: it still has one legacy duplicate-key group and schema
version 4. No historical Job deletion/merge/key rewrite or migration bypass.
Acceptance used isolated compatible schemas/disposable smoke DB. Deployment is
not claimed. Historical TIMED_OUT Job rows are preserved without automatic retry.

## Experimental

None. Controlled timeout/transient executors are test-only; production remains SLEEP.

## Planned

Priority aging/fairness, scheduled/capability-aware scheduling (existing Phase 7
roadmap), persistent per-job logs, DLQ UI, dashboard, observability, benchmarks and
deployment remain planned. No future Phase prompt generated or processed-counter
advance performed by Codex.

## Tests

### Tests Executed

Windows PowerShell; existing Go 1.26.8 Linux Docker tools, PostgreSQL/Redis real
services. FLOWFORGE_INTEGRATION=1 enabled. Each test creates a verified random
schema and random Redis namespace; no public application data or FLUSHDB.

- `docker compose --profile tools run --rm -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0=/src tools sh -c 'gofmt -w internal tests/integration && sh scripts/check.sh && go test -race -count=1 ./internal/domain/job ./internal/service/... ./internal/infrastructure/... ./tests/integration'`
  - gofmt check: PASS.
  - phase-state validator (in_progress): PASS.
  - go vet ./...: PASS.
  - go test -count=1 ./...: PASS; real integration 17.626s.
  - go build ./...: PASS.
  - affected race detector: PASS; execution 6.355s, integration 18.265s;
    domain/service/dispatch and infrastructure paths included, no race reported.
- `./scripts/phase6-smoke.ps1`: PASS; PowerShell syntax parse PASS.
- `git diff --check`: PASS; historical migrations/reports/prompts diff empty.
- Readonly retained DB/services/generated-resource audit: PASS (details below).
- Prospective completed-state validator: PASS after normalizing the Phase field to exact Phase 6; actual checkpoint/tag/report evidence validated before atomic replacement. The validator also reads checkpoint report contents; corrected report checkpoint 3673d56 and annotated v2 tag now PASS. Final state retains last_processed_phase=5, next_prompt=null.

## Test Results

Final required technical checks PASS. Initial test run was interrupted after a
legacy fake Job's omitted timeout=0 caused immediate cancellation and its barrier
test to hang. Updated only those test fixtures with valid timeout=300, retaining
their original supervision/shutdown assertions. An initial new test did not compile
because its claim-wait helper was absent; added a bounded helper.

Full/race tests then exposed a too-strong immediate cancellation/reaper assertion:
SKIP LOCKED can skip a Job while Cancel holds its row. Test now verifies subsequent
bounded recovery converges after request commit, rather than assuming one scan.
Final full/race rerun above passes. This is a documented maintenance latency, not
lost intent or extra retry. Migration down tests now remove 000006 before testing
historical 000005/000004/000003 behavior. No required test skip counted as PASS. Prospective validator initially rejected the extended Phase identity line and missing Tests/Git container headings; normalized it to exact Phase 6 and grouped Tests Executed and Git evidence under their required containers in this metadata-only follow-up. No invalid completed state was published.

NOT RUN: performance/throughput/starvation-aging benchmark, production deployment,
external business-effect exactly-once and uncooperative-executor termination tests;
these are outside current implementation scope. GitHub CI is not inferred from
local tests; external review can inspect published workflow evidence independently.

## Failure / Edge Case Validation

| Case | Evidence/result |
|---|---|
| Low Redis notification precedes high enqueue | Claim defers, notification ACK bounded; low has zero Attempts/count |
| High backlog starvation | Repeated low Claim stays deferred; no incorrect execution; aging unimplemented |
| Same-priority same timestamp | UUID tie order in dispatch/Claim; later tie cannot cross earlier queued candidate |
| Two workers/concurrent duplicate claims | High/low arbitration and exactly one winner for same Job |
| Low already RUNNING, high appears | Low remains RUNNING; no preemption |
| Promoted/redriven high queued | Low rejected until high normal Claim; old Attempt owner fenced after redrive |
| Deadline + renewal at same interval | TIMED_OUT Attempt; retry schedule or exact budget exhaustion; no N+1 |
| Test timeout then success | Attempt 1 TIMED_OUT, durable retry, Attempt 2 SUCCEEDED; history retained |
| Timeout finalize fault | Whole Job/Attempt rollback; RUNNING/pending retained, no ACK; later recovery fences stale finalize |
| QUEUED old notification after cancel | No execution/Attempt; intent invalidated; repeated cancel 200 |
| RETRYING cancel before due | Schedule cleared, no subsequent promotion/new Attempt |
| RUNNING cooperative cancel | Durable RUNNING request; owner stops; one CANCELLED/user_cancelled Attempt |
| Request then crash/late success | Recovery settles CANCELLED; stale finalize rejected; valid late local success honors committed intent |
| Cancel/reaper/expired finalize race | Durable intent converges across scan; one original Attempt, no retry |
| Cancel persistence fault | QUEUED and original intent preserved, no fabricated cancellation |
| SUCCEEDED/DEAD_LETTER/missing cancel | Stable 409/404; invalid ID/body rejected |
| DLQ pagination same timestamp | Only DEAD_LETTER, two pages, no duplicate UUID; malformed cursor/limit rejected |
| Attempts missing/empty/multiple | 404 / [] / stable full sequence with preserved worker/error/outcomes |
| Concurrent redrive | One 200 + one 409, single budget grant, no new Attempt until Claim |
| Budget cap 100 | Stable conflict, no bypass |
| Redrive/cancel race | Serialized state; at most one +1 budget, QUEUED or CANCELLED outcome |
| Redrive outbox fault | DEAD_LETTER/count/budget rollback; old history retained |
| Keyed POST after redrive | 200 original Job using original submission budget, no logical duplicate |
| Migration up/down | New columns removed/recreated; cancellation-state constraint enforced; historical migration regressions pass |
| Existing retry/idempotency/lease regression | Complete real-service integration and race suite PASS |

## Real-service Smoke

Generated DB/namespace: ff_phase6_smoke_20261005062033_4209e832.
Separate flowforge-phase6-smoke image/API loopback port and C=1 worker, without
replacing standard images/services. Long low-priority SLEEP supplies a claim
barrier; high/low queued count remains zero, running low is not preempted. User
cancel persists RUNNING request; renewal settles one CANCELLED/user_cancelled
Attempt. On barrier release high starts before queued low.

SLEEP(2000ms), timeout=1s, budget=2 records TIMED_OUT/RETRYING then second TIMED_OUT/
DEAD_LETTER. Permanent unsupported Job enters DLQ; list/Attempts inspect succeeds;
redrive grants +1 and preserves Attempt 1; Attempt 2 runs and keyed replay stays
the original Job. Final evidence: Jobs=5, Attempts=7, SUCCEEDED=2, CANCELLED=1,
DEAD_LETTER=2, TIMED_OUT Attempts=2. PASS printed after generated cleanup.

Readonly follow-up: generated DBs/containers/Redis namespace count zero; normal
API healthy, PG/Redis healthy, worker running; legacy duplicate groups=1 and
retained schema version=4 unchanged. Smoke build/cache image remains outside Git.

## Known Limitations

- Non-preemptive priority can starve lows; aging/fairness unimplemented. Short
  global DB Claim lock may limit peak scheduling throughput; no benchmark/SLA.
  Deferred Redis notification may add 30s reconciliation plus dependency delay.
- Cancellation is cooperative/renew-cadence based, not immediate execution stop;
  SKIP LOCKED may require another recovery scan. Dependencies/healthy workers are
  required. SLEEP honors deadline; uncooperative/external effectful executors are
  unsupported, and completed side effects cannot be undone.
- Original key identity stays global with no expiry/tenant scope. It is not
  exactly-once execution/business effects. DB lease fence cannot undo external work.
- No auth, persistent per-job application logs, DLQ UI, telemetry or production
  orchestrator. Attempts/results provide evidence; retention is unbounded.
- DLQ creation-time cursor is not a snapshot; concurrent redrive/removal/insertion
  can affect visible membership. History remains bounded by budget cap 100.
- Retained duplicate-key development DB is still Phase 4; explicit operator
  resolution is required before 000005+. Isolated acceptance does not deploy.
- 000006 down loses cancel intent and original submission-budget metadata; stop
  processes and evaluate data before rollback, no mixed-version recovery claim.

## Git

Real checkpoint/tag and publication evidence follow.

### Git Branch

codex/phase1-api-correctness

### Git Commit

Implementation revision: 414da6b9bcb3d7cf8c1a9dd83d6cbeead533fd49. Validated implementation/report checkpoint: 3673d56dbf466241225ebe383d47c10d8b60d246 (report-only repair commit; tested production sources unchanged).

### Git Tag

Final annotated phase6-control-dlq-v2 targets 3673d56dbf466241225ebe383d47c10d8b60d246, verified locally and remotely. Earlier phase6-control-dlq remains a published in_progress artifact at implementation revision 414da6b; its report format did not satisfy completed-state validation and it is not the completion tag. No tag/history rewriting.

### GitHub Push Result

PASS: validated checkpoint/annotated v2 tag pushed atomically and remote branch/peeled tag verified; main unchanged. This report/state metadata is published in the follow-up commit; prospective completed-state validation is required before replacement.
main remains aa96182a037bfc502927125246e07733d0e8dbd3; no automatic merge/history rewrite.

## Documentation Updated

README.md; docs/architecture.md; docs/job-lifecycle.md; docs/worker-operations.md;
docs/development-roadmap.md; docs/reports/index.md; this independent report;
docs/decisions/0007-priority-timeout-cancellation-dlq.md and ADR README;
internal/scheduler/README.md. Historical reports/prompts/ADRs/SQL unchanged.

## Next Recommended Phase

Existing roadmap Phase 7 is a recommendation only. Wait for GPT/external review
and its prepared next Prompt; Codex does not generate it or change processed to 6.

## Notes

Day1 continues its existing periodic checks in this chat. Completion state keeps
last_processed_phase=5 and next_prompt=null. No separate future task created.

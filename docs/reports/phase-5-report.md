# FlowForge Phase 5 Completion Report

## Phase

Phase 5

## Status

COMPLETED

Technical acceptance and final isolated real-service smoke PASS. This implementation
checkpoint prepares Git fields without claiming its own SHA; claim state remains
in_progress until implementation/tag publication and prospective completed-state
validation pass. A metadata-only follow-up records the actual checkpoint/push.

## Summary

Phase 5 delivers a total execution Attempt budget, explicit failure classification,
durable DB-time RETRYING schedules, bounded equal-jitter exponential backoff,
concurrent due promotion, DEAD_LETTER exhaustion and global exact-key submission
idempotency. PostgreSQL arbitrates scheduling and concurrent POST requests;
Redis remains reconstructible notification transport. Runtime failure transitions
follow the domain graph in order and commit with the exact Attempt. No Phase 6
priority/timeout/user-cancel/DLQ management is implemented.

## Prompt Source

Prompt Source: automation
Prompt Path: automation/prompts/phase-5.md
Execution ID: phase-5-20261005T051923Z (log/report only; schema_version remains 1).
Started at: 2026-10-05T05:19:23Z, Sydney 16:19:23.
Previous checkpoint: 40f1250ac64f12bcb51e799a8cbdce8cc45d6f00.
External handoff: ace1934a1583e7e98c5ae3aa1ed1aa160d8536b8.
Ownership claim: 5328d1bb1a5898e0ddd06a7529bab74301109133, pushed/reread before
business implementation. External Automation reviewed Phase 4, advanced processed
to 4 and published exactly Phase 5. No user scope changes or generated Phase 6.

## Implemented

- Claim condition count<max_attempts; only successful Claim increments/inserts
  Attempt. Expired lease, transient failure and graceful operational interruption
  consume the same budget; no N+1 execution. History/sequence remain intact.
- Explicit permanent/retryable Outcome classification with bounded stable codes.
  Unsupported type/invalid SLEEP are permanent; execution_cancelled/lease_expired
  are retryable subject to budget. No raw error-string policy or production test type.
- Domain RUNNING -> FAILED -> RETRYING/DEAD_LETTER applied sequentially in one
  fenced repository transaction with failed Attempt/result/error/end time.
  RETRYING clears owner/lease and sets DB-time retry_at; exhausted/permanent
  DEAD_LETTER clears schedule. Success clears lease/schedule and retains audit owner.
- Equal jitter: cap=min(max,base*2^(attempt-1)), delay uniform [cap/2,cap], default
  base/max 1/30 seconds. Saturating arithmetic, deterministic injection and
  concurrency-safe production source; validated whole-second configuration.
- Bounded <=100 concurrent promotion with SKIP LOCKED. RETRYING -> QUEUED and
  dispatch intent reset/insert commit together, without creating Attempt or doing
  Redis I/O. Existing maintenance loop handles recovery/promotion/offline scans;
  independent heartbeat and joined renewers remain intact.
- Lease recovery closes exact expired Attempt through identical retry policy;
  current owner/attempt/DB-time fences remain authoritative. Panic/uncertain
  renewal still requires expiry recovery without fabricated Finalize/ACK.
- Global exact non-null key uniqueness and INSERT arbitration. Canonical request
  comparison uses PostgreSQL JSONB semantics, trimmed type, priority, max_attempts
  and timeout. Explicit CreateDisposition carries created/replayed.
- HTTP first create/no key 201, identical keyed replay 200 with original body/
  Location, conflict 409 IDEMPOTENCY_CONFLICT. Replay at RUNNING/RETRYING/SUCCEEDED/
  DEAD_LETTER creates no Job/intent/Attempt and leaves publication unchanged.
- Append-only 000005 retry_at, budget/schedule constraints, due partial index and
  global key partial unique index. Duplicate historical keys atomically reject
  migration without selecting/deleting/merging a Job. History>100 requires review;
  over-budget <=100 freezes max at recorded count, and exhausted QUEUED rows
  normalize administratively to DEAD_LETTER. Old RETRYING gets a DB-time 1s
  schedule; historical FAILED stays unchanged. Down removes schema additions
  while preserving history/counters/normalized outcomes. 000001–000004 unchanged.
- Real isolated API/process smoke and migration/concurrent HTTP/repository/
  lifecycle/failure tests; whole-project docs and ADR 0006 synchronized.

## Not Implemented

None of the authorized Phase 5 acceptance scope is omitted. Upgrade of the existing
local development DB was not performed: its duplicate historical keys require
explicit operator resolution. Acceptance used isolated schemas/generated resources;
existing Phase 4 services/data remain running unchanged. Deployment/data cleanup
is not claimed as completed functionality.

## Experimental

None. Fault executors are test-only; SLEEP is the sole production executor.

## Planned

Existing roadmap Phase 6 priority/generic timeout/user cancellation/DLQ management,
and later scheduling/UI/observability/benchmarks/deployment remain planned. No next
prompt was generated; external review determines the next authorized phase.

## Tests

### Commands and environment

Windows PowerShell host; no host Go installation. Existing Docker tools image
uses Go 1.26.8 linux/amd64 with configured PostgreSQL 18/Redis 8.2. Integration
enabled via FLOWFORGE_INTEGRATION=1. Tests create random verified schemas and
stream keys, never public application data or FLUSHDB. Tools use existing module/
build caches; CLI Git safe.directory is passed only for the mounted repository.

- `docker compose --profile tools run --rm -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0=/src tools sh -c 'gofmt -w tests/integration && sh scripts/check.sh'`
  - gofmt check, phase-state validator: PASS.
  - go vet ./...: PASS.
  - go test -count=1 ./...: PASS, including real integration (9.434s).
  - go build ./...: PASS; no generated binary staged.
- `docker compose --profile tools run --rm tools go test -race -count=1 ./internal/retry ./internal/service/... ./internal/infrastructure/... ./tests/integration`
  - PASS; execution service 6.348s, integration 11.524s, no race reported.
  - Later vet correction only named the same Failure fields in test literals;
    runtime behavior/production sources unchanged, and full tests passed afterward.
- `./scripts/phase5-smoke.ps1`: final PASS, including cleanup after assertions.
  PowerShell parser also PASS. Builds separate flowforge-phase5-smoke image;
  existing development image tags/services are not replaced.
- `git diff --check`: PASS. Completion metadata/state validation and remote
  checkpoint/tag/branch checks are recorded under Git after publication.

### Results and resolved development failures

Initial tests correctly exposed old Phase 4 expectations (FAILED/immediate requeue/
over-budget crash/double creation) that Phase 5 intentionally supersedes. Updated
tests retain prior fencing/rollback/notification/shutdown assertions under new
contracts. A test-only multi-statement parameterized SQL fixture was split into
separate statements. Vet rejected unkeyed cross-package Failure literals; named
fields corrected this. Final complete checks pass; no unresolved failure/skipped
required integration test is reported as successful.

First smoke acceptance passed but Redis CLI emitted an empty-password AUTH warning;
cleanup was hardened to set authentication only when configured and verify EXISTS=0.
Final smoke passed cleanly after this change. No normal application key/DB was deleted.

## Failure / Edge Case Validation

| Scenario | Verified result |
|---|---|
| Controlled transient Attempt 1 | RETRYING with retry_at/no owner/lease; original delivery ACK only after commit; no early dispatch/promotion |
| Due retry after repository/process restart | QUEUED + intent then Attempt 2 SUCCEEDED; old Attempt remains FAILED with stable code |
| Budget 1 and 3; transient and lease-expired paths | Exactly N Attempts; final DEAD_LETTER; Claim N+1 rejected; promoter creates none |
| Permanent unsupported/invalid SLEEP | One FAILED Attempt; Job DEAD_LETTER; peers continue |
| SIGTERM execution_cancelled | Real process exit 0; Attempt 1 FAILED; durable RETRYING; fresh process completes Attempt 2 |
| Expiry vs late Finalize/Renew | DB fence rejects old owner/sequence/expired lease; concurrent reapers settle once |
| Retry scheduling constraint failure | RUNNING Job/Attempt retained atomically; pending delivery count 1/no ACK; later expiry recoverable |
| Closed DB during scheduling/promotion | Error returned; no fabricated success/state/ACK |
| Eight concurrent promoters | One effective due transition, no new Attempt |
| Intent reset rejected | Promotion rolls back; RETRYING schedule remains; retry works after fault removal |
| Redis/dispatch unavailable after due QUEUED | Durable intent retained; marker failure/queue deletion reconstructed and Attempt 2 succeeds |
| Jitter endpoints/large integer Attempt | Exact inclusive [cap/2,cap], positive lower bound, saturation without overflow; concurrent default samples within bounds |
| Sixteen concurrent keyed HTTP requests | One 201, fifteen 200; one logical Job/outbox; same ID/Location |
| Semantically equal differently formatted JSON | 200 replay, including reordered objects and numeric 1 vs 1.0; no float64 comparison |
| Different type/payload/priority/max/timeout | Stable 409 IDEMPOTENCY_CONFLICT; existing rows unchanged |
| Exact whitespace/case keys; null/no key | Distinct exact strings; eight concurrent no-key requests create distinct Jobs |
| Replay RUNNING/RETRYING/SUCCEEDED/DEAD_LETTER | Same Job/status; one Attempt/outbox, publication unchanged; HTTP 200/Location original |
| Job insert then keyed outbox failure | Whole first-create rollback; key not consumed; later create succeeds |
| DB unavailable for replay/conflict | Generic safe 500, no pgx/connection/driver detail |
| Historical duplicate-key migration | Diagnostic error; schema version remains 4; both Jobs retained; retry_at addition rolls back |
| Historical over-budget normalization/down | Count retained and budget frozen; exhausted QUEUED terminal; >100 history rejects upgrade; new schema up/down/re-up passes |

### Final real-service smoke

Generated resources: ff_phase5_smoke_20261005054103_cacac9db and its corresponding
flowforge:test:phase5 stream; API uses an available loopback port. Concurrent keyed
requests: 1x201 + 7x200, one ID/Location/Job/outbox; mismatch 409; two no-key IDs
distinct. Controlled active-owner SIGTERM yields execution_cancelled and a future
RETRYING schedule. Base=max=8s equal jitter provides [4,8]s observation window;
fresh worker starts before due while count/history remain 1, then promotes and
completes 5s SLEEP as Attempt 2 on a different owner.

Final evidence: Jobs=4, Attempts=5, SUCCEEDED=4; retry history =
Attempt 1 FAILED/execution_cancelled, Attempt 2 SUCCEEDED/error null. PASS prints
after generated containers/DB/key cleanup. Follow-up readonly audit finds zero
Phase 5 smoke databases, zero matching Redis keys and zero generated containers;
normal API healthy/worker running and the original one duplicate-key group retained.

## Known Limitations

- Existing development DB has one legacy duplicate non-null key group, detected
  readonly before development and retained afterward. 000005 deliberately blocks
  that DB upgrade until explicit operator resolution; normal services still run
  Phase 4. New/compatible DBs and isolated acceptance are Phase 5 validated. No
  silent production-like rollout or historical data deletion is claimed.
- Administrative legacy normalization preserves Attempts/counts but may change
  old max_attempts metadata; future replay compares normalized canonical fields.
  >100 counts or incompatible/corrupt historical state need explicit review.
- Submission idempotency is global with no key expiry/tenant namespace. It does
  not guarantee exactly-once execution or external business effects. Leases fence
  DB writes only; future effectful handlers need business-specific idempotency.
- Healthy dependencies and workers are required for recovery/promotion; DB clock
  shifts and scan intervals affect delay. No time/throughput/availability SLA.
- Old pending Streams entries, consumers, dispatch/Attempt/key history have no
  automatic retention. Corrupt history can roll back a bounded maintenance batch.
- Local demo has no auth/admission control/Redis TLS/production orchestrator.
  Priority/timeout/user cancel/DLQ management are not implemented.
- Historical Phase 3/4 smoke scripts remain for their tagged contracts; use the
  Phase 5 smoke for this version. Local smoke image/cache lives outside Git.

## Git

- Branch: codex/phase1-api-correctness.
- Commit: pending implementation/report checkpoint; this file cannot embed its own SHA.
- Tag: pending annotated phase5-retry-idempotency targeting that checkpoint.
- GitHub Push Result: pending publication; claim already pushed/reread successfully.
- main remains the Phase 0 revision aa96182a037bfc502927125246e07733d0e8dbd3;
  no merge/force/history rewrite. No .env/credentials/binary/temp artifacts staged.
- Completion state will preserve last_processed_phase=4 and next_prompt=null;
  report/path source remain automation/Phase 5. No false external consumption.

## Documentation Updated

- README.md: whole-project capability/status/API/retry/upgrade documentation.
- docs/architecture.md, docs/job-lifecycle.md, docs/worker-operations.md.
- docs/development-roadmap.md, docs/reports/index.md.
- docs/decisions/0006-budgeted-retries-and-submission-idempotency.md and ADR index.
- internal/retry/README.md and executor/recovery navigation placeholders.
- .env.example and docker-compose.yml retry configuration.
- docs/reports/phase-5-report.md: this independent evidence; historical reports,
  prompts and migration SQL remain unchanged.

## Next Recommended Phase

Phase 6 is the existing roadmap recommendation only. Wait for external GPT review
and its explicitly prepared next prompt; no Phase 6 file/scope is generated here.

## Notes

Day1 heartbeat remains ACTIVE on the same chat at the existing 30-minute interval.
After completed-state push, wait for external review; do not advance processed to
5 or synthesize next_prompt. Operator duplicate-key resolution is a separate
explicit action before upgrading the retained development database.

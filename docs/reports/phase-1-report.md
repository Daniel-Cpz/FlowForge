# FlowForge Phase 1 Completion Report

## Phase

Phase 1

## Status

COMPLETED

## Summary

Phase 1 establishes a strict, tested Job HTTP and PostgreSQL persistence contract
before queue dispatch or worker execution. It adds exact Create envelope parsing,
explicit null/default semantics, deterministic keyset pagination, canonical
readback, safe error mapping and expanded real-database coverage. The existing
automation protocol now tracks both manual and automated prompt sources.

This report records completed implementation and validation. The real Git
checkpoint is recorded in a follow-up metadata commit; authoritative state stays
in_progress until that checkpoint/tag and report evidence are verified.

## Prompt Source

Prompt Source: manual
Prompt Path: null

The user supplied the original Phase 1 prompt directly, then explicitly added the
Phase Automation Protocol and Manual / Automation Prompt Source Extension while
work was in progress. These additions extended the current scope; they did not
authorize Phase 2. No automated Phase 1 prompt or superseded prompt file existed.
Existing independent infrastructure commits are preserved without rewriting.

## Implemented

- Exact lowercase Create envelope fields, unknown/server-owned field rejection,
  duplicate top-level key rejection, one-document parsing and bounded UTF-8 input.
- Required type and payload; JSON null payload accepted; numeric omission/default
  versus explicit null distinguished; type/key length and numeric boundaries.
- Unpaired metadata-surrogate rejection and JSONB Unicode/numeric rejection
  mapped to generic public 400 errors without raw PostgreSQL detail.
- Single INSERT ... RETURNING canonical create response, UUID/UTC/null execution
  invariants and shared domain value checks at service/persistence boundaries.
- Typed keyset boundary, deterministic descending timestamp/UUID order, limit+1
  lookahead and a versioned URL-safe cursor with bounded strict decoding.
- Request context propagation, bounded database operations, corrupted-row
  detection and generic 500 handling for unexpected constraints/dependencies.
- HTTP, service/domain, cursor and isolated PostgreSQL integration tests.
- v1 prompt_source/prompt_path extension, existing strict schema/Git/path checks
  retained, and manual/automation prompt-source regression coverage.
- Project README, architecture/roadmap, ADR, protocol/template and independent
  phase completion report synchronized with actual capabilities.

## Not Implemented

No Phase 1 acceptance item remains unmet at the implementation/test checkpoint.
Queue and execution features are deliberately not implemented; see Out of Scope.
An external Automation runner is not implemented by the state protocol.

## Experimental

None.

## Planned

Phase 2 Redis queue and a single worker demonstration, followed by bounded
concurrency, heartbeat/lease/recovery, retry/backoff/jitter, scoped idempotency,
priority/timeout/cancellation/DLQ, scheduling, dashboard, observability,
benchmarking and cloud deployment. None of these is claimed implemented here.

## API Contract Changes

POST /api/v1/jobs accepts only type, payload, priority, max_attempts, timeout and
idempotency_key with exact names and unique envelope keys. Type is trimmed only
at its ends, then limited to 1–128 Unicode code points. Payload is required and
can be any JSON value, including null. Optional numeric null is rejected; missing
priority/max_attempts/timeout default to 0/3/300. Their ranges remain 0–100,
1–100 and 1–86400 seconds. Metadata key omission/null means no key; a supplied
nonblank string is preserved and limited to 255 code points. Stored idempotency
key != idempotent submission: same-key requests still create independent jobs.

Invalid JSON/UTF-8, wrong-case, unknown, injected execution fields, duplicate
envelope fields and trailing documents return 400. Body >1 MiB returns 413 even
without Content-Length. Nested payload duplicates retain JSONB last-value
semantics. JSONB normalizes stored values; POST and GET expose the same persisted
representation. RFC3339 timestamps are UTC with microsecond precision.

GET /api/v1/jobs/{id} returns 200, malformed UUID 400 or absent Job 404. Existing
error-envelope style remains. INVALID_CURSOR distinguishes malformed cursors;
unexpected DB failures/corrupted data return generic INTERNAL_ERROR 500.

## Persistence Changes

Create returns every stored column from INSERT ... RETURNING instead of echoing
pre-normalized JSON. Get/List normalize timestamps to UTC and reject invalid
domain values rather than turning serious corruption into a valid response.
PostgreSQL JSONB input errors, including Unicode and numeric-range failures,
map to INVALID_INPUT; unrelated constraint errors stay internal. All queries
retain request context and five-second operation bounds.

## Pagination Design

Offset was replaced directly; no external API consumer or compatibility promise
was found. List accepts limit (default 20, range 1–100) and optional cursor only,
and returns jobs plus nullable next_cursor. Offset is now an unknown parameter.

Rows sort by (created_at DESC, id DESC); a subsequent page applies the exclusive
tuple comparison (created_at, id) < (cursor_time, cursor_id). Service requests
limit+1 rows, returns at most limit, and derives the cursor from the last returned
row, never the hidden lookahead row. Final/empty pages have null next_cursor.

Transport owns bounded (512-character), versioned, canonical base64url/JSON
encoding; domain and repository use a time/UUID structure. Newer inter-page
inserts do not shift traversal of older jobs. It is not a snapshot, and valid
constructed unsigned boundaries are accepted. No performance benchmark is claimed.

## Transaction Boundary

Create is one INSERT ... RETURNING; Get and List each use one SELECT. No explicit
transaction is added to single-statement CRUD. Migrations retain their explicit
transaction, version tracking and advisory lock. Future multi-statement job,
attempt and dispatch coordination needs its own atomicity decision (ADR 0002).

## Database / Migration Changes

No schema migration was needed and historical migrations were not modified.
jobs, job_attempts and schema_migrations remain. Existing UUID/JSONB/TIMESTAMPTZ,
status/numeric/time constraints and attempt uniqueness remain in force.
jobs_created_at_id_idx already matches the ordering and tuple boundary; no
duplicate or speculative index was added. No UNIQUE(idempotency_key) was added.

## Tests Added

- Create envelope payload types, Unicode/rune boundaries, defaults/nulls,
  duplicate/escaped keys, unknown/wrong-case names, execution-field injection,
  malformed/trailing input and exact/over-limit request bodies.
- Cursor round trip, invalid base64/JSON/structure, missing fields, invalid
  timestamp/UUID, precision/version errors and excessive length.
- Service creation invariants/input ownership, lookahead and last-returned-row
  cursor generation; domain corruption/value validation.
- Real PostgreSQL full-field readback, canonical HTTP-to-DB responses, same-key
  independent submissions, JSONB Unicode/numeric failures and absent jobs.
- Equal-created_at UUID tie breaking, first/next/final/empty pages, exclusive
  boundaries, page limits and newer inserts between page requests.
- Raw SQL invariant protection, cancelled/expired contexts, unexpected DB
  constraint errors, closed-pool failures and corrupted stored status/time.
- Prompt-source manual/null and automation/current-file rules, source/path
  schema rejection, missing/wrong-phase files, current/next independence and
  repository/symlink confinement; all original validator tests retained.

## Tests

### Tests Executed

Host Go is unavailable. Commands ran in the Linux Docker tools container;
FLOWFORGE_INTEGRATION=1 uses PostgreSQL with a fresh random schema per test.
Every test-pool connection verifies current_schema() before queries, including
reconnections. Cleanup drops only its generated schema; no application data is
removed. Git validator fixtures use disposable temporary repositories.

### Test Results

| Command/check | Result |
|---|---|
| go test -count=1 ./internal/transport/http/... | PASS |
| go test -count=1 ./internal/domain/job ./internal/service/job | PASS |
| go test -count=1 -v ./tests/integration | PASS, real PostgreSQL, no skips |
| go test -count=1 -v ./scripts/validate-phase-state | PASS, original/new tests, no skips |
| go vet ./scripts/validate-phase-state | PASS |
| go run ./scripts/validate-phase-state | PASS for manual/in_progress state |
| go test -race -count=1 ./internal/service/job ./internal/transport/http/... ./tests/integration | PASS |
| gofmt -l cmd internal migrations tests scripts/validate-phase-state | PASS, no unformatted files |
| go vet ./... | PASS |
| go test -count=1 ./... | PASS, includes real integration and validator tests, no skips |
| go build ./... | PASS |
| docker compose config --quiet | PASS |
| docker compose up --build -d | PASS; API, worker and migration binaries built |
| live GET /health and /ready | PASS, both 200 |
| live POST/GET and first/next cursor pages with an inter-page insert | PASS |
| git diff --check | PASS before checkpoint preparation |
| Remote GitHub Actions status | NOT EXECUTED / not inspected |

The complete checks ran through docker compose --profile tools run --rm tools
(scripts/check.sh). Development initially found an unused test import; it was
fixed and targeted tests rerun. Final acceptance has no failing or skipped test.

## Failure / Edge Case Validation

Malformed/client-owned state never persists. Unknown Job maps to 404; malformed
UUID/cursor to 400; oversize body to 413. Nullable execution fields remain present
and null. Same-timestamp jobs have deterministic order. Integration and live
smoke tests show a newer insert does not duplicate or skip the older traversal.
PostgreSQL Unicode NUL/unpaired surrogate/numeric overflow produces sanitized
400; unrelated DB constraints, unavailable pool and corrupt rows produce generic
500. Cancellation/deadlines preserve their cause and prevent ordinary queries
from discarding request context.

One automatic approval call failed with a 401 review-service error and did not
execute. A later review rejected destructive test SQL because isolation had not
been demonstrated. Added per-connection schema assertions and row-scoped
corruption updates, inspected the evidence, and the same approval route then
accepted the tests. No bypass or application-data mutation was used.

Docker PostgreSQL/Redis/API are healthy, migrations complete, and the worker
remains a non-executing process. Five phase1-smoke QUEUED jobs were intentionally
retained in the development database after the required live HTTP smoke test.

## Known Limitations

- Jobs still do not execute. Redis queue is still not implemented.
- Worker remains non-executing. No delivery/execution guarantee is provided.
- Idempotency key does not suppress duplicates. Priority and timeout are metadata only.
- Nested payload duplicate keys follow JSONB semantics. Cursor traversal is not
  a snapshot; backdated inserts may appear after a boundary. Cursors are unsigned.
- A commit can persist while its HTTP response is lost; resubmission can duplicate jobs.
- No authentication, production deployment, metrics/tracing or benchmark evidence.
- Validator checks protocol shape, file paths and real Git evidence; it cannot
  prove tests/scope or enforce external-consumer authorization/concurrency.
- Prompt-source fields finalize unpublished v1; no formal external consumer was
  found. A real deployed consumer would need a compatibility review.

## Out of Scope

No Redis dispatch, worker execution, scheduling, heartbeat, lease, crash recovery,
retry/backoff/jitter, duplicate suppression, DLQ, timeout/cancellation execution,
priority scheduling, dashboard, tracing/metrics or cloud infrastructure was added.

## Git

- Branch: codex/phase1-api-correctness
- Commit: Pending implementation/report checkpoint; fill the actual SHA in the metadata follow-up.
- Tag: Pending annotated phase1-api-correctness at that checkpoint.
- GitHub Push Result: Pending final publication; no successful Phase 1 push is claimed yet.

Git Commit identifies the implementation/report checkpoint, not a file's own SHA.
Git Tag points to that checkpoint; a later metadata commit records completed state.
The previous infrastructure history and main are preserved; no force push or
automatic main merge is authorized by this release workflow.

## Documentation Updated

- Project README: README.md, whole-project status, strict API and pagination examples.
- Phase report: docs/reports/phase-1-report.md, independent Phase 1 completion evidence.
- docs/architecture.md and docs/development-roadmap.md.
- docs/decisions/0002-phase-1-api-contract.md and decision index.
- docs/phase-automation.md and docs/phase-report-template.md.
- docs/reports/phase-automation-infrastructure-report.md, historical results retained
  with a prompt-source follow-up; docs/reports/index.md navigates phase evidence.

## Next Recommended Phase

Phase 2 — Redis Queue + Single Worker Execution. Ready to begin only with an
authorized future prompt and an explicit database/queue dual-write design.
Phase 2 has not started. next_prompt stays null and last_processed_phase stays 0;
no external Automation consumption or generated next prompt is fabricated.

## Notes

Manual and automated prompts follow identical implementation/test/report/README/
Git/state gates. Explicit scope additions are recorded above. Atomic state
replacement publishes completion only after the real checkpoint/tag exist and
prospective state validation passes. No new third-party dependency, framework,
migration engine or large refactor was introduced.

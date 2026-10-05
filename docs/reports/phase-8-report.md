# FlowForge Phase 8 Completion Report

## Phase

Phase 8

## Status

COMPLETED

## Summary

React/TypeScript Dashboard and bounded multi-process WebSocket invalidation
hints expose PostgreSQL facts without changing execution authority. Visible
REST snapshots repair missed/duplicate/out-of-order events on reconnect and
every 30s. Controls preserve real REST success/conflict/error semantics.
Implementation and validation gates passed; Git publication is recorded below.

## Prompt Source

Prompt Source: automation
Prompt Path: automation/prompts/phase-8.md
Execution ID: phase-8-20261005T075343Z
Started At: 2026-10-05T07:53:43Z
Previous Phase: 7
Previous checkpoint: 54daf19da8009c2e35a6f9bff1ca5caea4be20cf
Ownership commit: 115a8796295f1658970a27c09a5dc6b56ca28d28, pushed/read back before edits.
No manual scope changes or future-phase prompt generation.

## Implemented

- Summary read model/service/API: one PG statement snapshot; explicit zero counts
  for all Job/Worker/Schedule statuses; documented due QUEUED/budget queue depth;
  active registry count excludes OFFLINE. No fabricated performance metrics.
- Stable bounded Workers (immutable UUID DESC) and Schedules (created_at/UUID
  DESC) pagination with 20 default / 100 max and <=101 lookahead; validation and
  generic DB failure envelopes. Reuse Jobs/DLQ/details/Attempts.
- After-commit nonblocking observation for create/Claim/Finalize/retry promotion/
  recovery/cancel/redrive, schedule creation/cancellation/materialization and
  Worker register/status/count/drain/offline. Unchanged heartbeat emits no hint.
- Separate namespace-derived Redis Pub/Sub UI channel, 128-slot process queue,
  one publisher, 250ms deadline and independent two-connection UI Redis pool.
  Loss/failure is sanitized and cannot roll back committed business state.
- Version-1 resource/status-only hints with 1 KiB cap, independent API subscribers,
  subscription reconnect LIVE/DEGRADED hints and snapshot invalidation.
- Gorilla WebSocket v1.5.3; 100 connections/API, 16 messages/client, overflow closes
  slow peers, 1 KiB reads, 5s handshake / 2s writes, 15s ping / 45s pong; restrictive
  same-host/exact-origin allowlist, REST-only commands, joined client/subscriber
  lifecycle and graceful shutdown.
- React 19/TypeScript/Vite frontend: Overview, Jobs, detail/full fields/Attempts,
  Workers, Schedules, DLQ; hash navigation, bounded pagination, unified badges,
  local timestamps/full tooltip, escaped collapsed JSON capped at 16 KiB/field.
- Native fetch/WS: reconnect resync, 30s visible repair, 250ms burst coalescing,
  1s..15s retry backoff, timer disposal and stale request abort/discard. Loading,
  empty, errors/stale data and degraded/reconnecting state are explicit.
- Confirmed REST cancellation/redrive/schedule cancel with real results/conflicts,
  disabled submitting state and refetch; no optimistic authoritative FSM changes.
- Vite REST/WS proxy, local Compose dashboard profile, lockfile, frontend and Go
  CI gates. Separate isolated two-API/two-worker/Vite process smoke with exact cleanup.

## Not Implemented

Browser E2E automation (NOT RUN), persistent logs, throughput/failure-rate/latency
metrics, replay/durable UI event delivery, auth/TLS/public serving, cloud deployment,
cron/edit/pause/resume, capability routing or priority aging. No new SQL migration
and no retained legacy-data repair/upgrade.

## Experimental

None. The Dashboard is a tested local/demo control plane, with documented
production limitations; it is not presented as a production deployment.

## Planned

Phase 9 observability/metrics/tracing, systematic failure injection and benchmarks
remain roadmap context for external review. Phase 10 deployment must reassess
authentication, TLS and origins. No next-phase prompt was generated here.

## Tests

### Tests Executed

- Docker tools Go 1.26.8: `sh scripts/check.sh` with real isolated-schema PostgreSQL
  and random-key Redis integration: gofmt, state validator, `go vet ./...`,
  `go test -count=1 ./...`, `go build ./...` — PASS; integration 20.901s.
- `go test -race -count=1 ./internal/realtime ./internal/service/...
  ./internal/infrastructure/... ./internal/transport/... ./tests/integration`
  — PASS; integration 24.177s. Existing scheduling/capability/retry/cancel/lease
  regressions ran with real PG/Redis; no integration NOT RUN in these tools runs.
- Final separate UI connection-pool wiring: `go build ./...` and targeted
  realtime/WS/real fanout/publish-failure race regressions — PASS (integration 2.811s).
- Final overflow warning is emitted by the publisher rather than the business
  producer; affected outage/publish-failure/fanout race tests and build — PASS
  (integration 2.841s). Producer does not wait on Redis or the warning log sink.
- Node 24.21.0/npm 11.19.0: lockfile `npm ci --no-audit --no-fund`, typecheck,
  `npm test -- --run` (16 tests / 3 files), `npm run build` — PASS.
  Final formatting followed by typecheck/all 16 frontend tests — PASS.
- `scripts/phase8-smoke.ps1`: real API x2, Worker x2 (C=1), PostgreSQL, Redis,
  Vite REST/WS proxy and actual ClientWebSocket clients — PASS. Generated schema 7:
  4 Jobs, 6 Attempts, 3 SUCCEEDED, 1 DEAD_LETTER, 2 Worker identities.
- Retained-data audit before/after: schema 4, one legacy duplicate non-null key
  group, unchanged. Generated containers/database/stream removed; UI subscribers
  zero. Normal development service images/data are preserved.
- Phase state validation and `git diff --check` — PASS before publication.

### Test Results

PASS for implementation, frontend, real integrations, race, full regressions,
builds and isolated process acceptance. Browser E2E NOT RUN; no visual browser
rendering or browser automation result is claimed. Real WS clients, components
and process proxy acceptance are the supplied evidence, not a performance benchmark.

## Failure / Edge Case Validation

- Empty/all-zero counts; many Workers with tied heartbeat and updates between
  pages; tied Schedule timestamps; 1..100 limits, duplicate/unknown/invalid query,
  generic DB unavailable response. Due queue includes unmatched capability Jobs.
- Observation callbacks read PG independently to prove changes are committed
  before hints; transactional dispatch failure emits no successful-change hint.
  Repeated unchanged heartbeats create no event storm.
- Redis UI publish outage produces sanitized warning/drop while Create/Cancel
  stay durably successful. A 1,000-hint producer burst does not wait on Redis.
- Two separate Redis clients/API hubs receive cross-process hints. Killing only
  a uniquely named test Pub/Sub socket signals DEGRADED, resubscribes, receives
  the next hint and joins on cancellation.
- Hub race tests: 1,600 concurrent broadcasts, deterministic 16-message bound,
  slow peer disconnect, connection admission/origin rejection, reconnect/unregister,
  application-command rejection, malformed unmasked frames, oversized frames,
  idle non-reading peer and shutdown with connected clients.
- Frontend: API 400/409/500/invalid response, 200-event coalescing, duplicate
  disconnects, capped retries, periodic lost-event repair, cleanup with zero timers,
  stale detail response after early event, Jobs refreshed status, real cancel
  result/refetch, redrive failures, confirmed schedule cancel and escaped bounded JSON.
- Process smoke: both API hubs observe QUEUED -> RUNNING -> SUCCEEDED; killed
  owner yields lease_expired Attempt and survivor Attempt 2 success, OFFLINE hint;
  completion while disconnected is recovered through REST snapshot; unsupported
  executor DLQ/redrive preserves two Attempts; Schedule cancel and Worker fields.
- Initial integration test compilation used an unavailable Redis helper name;
  corrected to a named independent Redis client, all affected tests reran PASS.
  Windows sandbox initially denied esbuild ancestor reads; authorized local
  build retry and clean-install final build passed. First smoke exposed a
  PowerShell async void-result issue before business validation; corrected, exact
  generated resources cleaned, retained audit unchanged, full smoke reran PASS.

## Known Limitations

- No authentication or public production serving. Vite profile is local/demo;
  external exposure needs Phase 10 security/deployment work.
- UI hints are lossy/transient, with no replay/order guarantee. Refresh converges
  only while REST is available, normally within 30s if notifications are missed;
  no latency/availability SLA or exactly-once execution/side-effect claim.
- Summary scans current tables; pagination is live, not a frozen snapshot. Job
  and Attempt requests are separate; Worker active count is heartbeat-derived.
  Stable heartbeat/lease details update via periodic/manual refresh.
- JSON display is capped; REST contains full data. Persistent application logs
  and Phase 9 metrics are not implemented. Browser E2E/visual QA NOT RUN.
- Retained development DB remains schema 4 with its legacy duplicate-key group;
  no migration bypass or data repair. New Dashboard acceptance uses isolated
  compatible schema 7; no Phase 8 schema changes.

## Git

Git Branch: codex/phase1-api-correctness
Git Commit: c349113f0f2423890fe199a81308e40cedd695d7 (implementation checkpoint).
Git Tag: phase8-dashboard-websocket (annotated; target is that checkpoint).
Tag object: 5bba1bae67a4b93da8b530274c017a19aeaf907c.
Git Push: checkpoint/tag atomic push PASS; remote branch and peeled tag both
read back as c349113f0f2423890fe199a81308e40cedd695d7. Completed state/report
metadata is published in the subsequent ordinary branch commit; its final
readback is required before the chat completion log.
Remote main remains aa96182a037bfc502927125246e07733d0e8dbd3.

The checkpoint includes a valid report with pending Git fields and all
implementation/docs with state still IN_PROGRESS. This completion metadata
records that real checkpoint SHA; a report cannot embed its own future commit.
Prospective completed state/checkpoint-report/tag validation passed before push.
No force push, history rewrite,
retagging, main merge or changes to historical migrations/reports/prompts.

## Documentation Updated

README, architecture, Dashboard contract, Worker operations, roadmap, report
index, ADR index/0009, web README and this independent report. Compose/env/CI
document the local frontend integration and actual validation contract.

## Next Recommended Phase

Wait for external GPT review and a prepared Phase 9 observability prompt, following
the existing roadmap. This is a recommendation, not authorization or a generated
next prompt. Completion leaves last_processed_phase=7 and next_prompt=null.

## Notes

Execution ID remains in logs/report because strict schema_version=1 has no
execution_id field. Ownership was pushed/read back before business edits.
Correctness authority remains PostgreSQL; UI hints are presentation transport.

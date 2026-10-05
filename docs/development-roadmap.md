# Development roadmap

Phases 0–4 are implemented: foundation, strict API/persistence, durable dispatch,
SLEEP execution, multiple workers with bounded concurrency, and DB-time lease recovery.
Phase 5 onward remains Planned. Authoritative completion status is in
[`automation/state.json`](../automation/state.json). The whole-project
[`README.md`](../README.md) and each independent report are required on completion.
See the [protocol](phase-automation.md) and [report template](phase-report-template.md).

| Phase | Scope |
|---|---|
| 0 | Engineering foundation: processes, domain, persistence baseline, tests and tooling |
| 1 | Job model + PostgreSQL + API correctness |
| 2 | Redis queue + single worker + SLEEP demonstration; solve DB/queue dual-write failure |
| 3 | Multiple workers + bounded concurrency |
| 4 | Heartbeat + lease + crash recovery, stale-owner fencing |
| 5 | Retry + exponential backoff + jitter + idempotency |
| 6 | Priority + timeout + cancellation + dead letter management |
| 7 | Scheduled jobs + capability-aware scheduling |
| 8 | React / TypeScript dashboard + WebSocket |
| 9 | Prometheus + Grafana + tracing + failure injection + benchmarks |
| 10 | Cloud deployment + CI/CD |

## Next recommendation: Phase 5 — Retry + Idempotency

Phase 3 preserves PostgreSQL conditional claim/attempt and owner/attempt finalization,
adds C fixed slots per process, shared stream/group process identities, multiple
bounded dispatchers, fail-fast supervision and concurrent shutdown. Barrier/race
and isolated PostgreSQL/Redis tests plus two real processes validate correctness;
no scaling throughput claim is made.

Phase 4 adds persisted heartbeat/offline state, valid-lease Claim/Renew/Finalize,
bounded concurrent reapers and atomic recovery/dispatch intent. SIGKILL and paused
stale-owner smoke demonstrates takeover with independent Attempt history.

Phase 5 should define business retryability, bounded backoff/jitter, unified
max_attempts accounting and submission idempotency. Crash attempts currently may
exceed max_attempts to avoid stranding recoverable RUNNING work; business FAILED
jobs are not retried. This is a recommendation only; Codex has not generated or
started Phase 5. External review and prompt publication are required for handoff.

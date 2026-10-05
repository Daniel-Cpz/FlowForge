# Development roadmap

Phases 0–7 are implemented: foundation, strict API/persistence, durable dispatch,
SLEEP execution, multiple workers with bounded concurrency, DB-time lease recovery, budgeted retries/idempotency and priority/timeout/cancellation/DLQ controls.
Phase 7 adds DB-time delayed Jobs, capability-aware Claim and fixed-interval recurring schedules. Phase 8 onward remains Planned. Authoritative completion status is in
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

## Current implementation and next handoff

Phase 7 implements delayed time eligibility, immutable canonical Worker
capabilities, per-worker eligible priority, and fixed-interval templates. Bounded
PG transactions create a unique ordinary Job/intent and advance next_run_at;
missed middle intervals are skipped. Schedule cancel leaves existing Jobs alone.
Cron/edit/pause/resume, capability routing, priority aging and dashboard remain
unimplemented. Phase 8 React/WebSocket remains Planned.

External Automation must consume the Phase 7 report and publish a legitimate
next prompt before another phase starts. Codex has not generated that prompt or
advanced last_processed_phase to 7. The next recommendation is roadmap context.
Retained duplicate-key data remains schema 4 pending separately authorized
operator resolution; isolated schema-7 acceptance is not deployment or data repair.
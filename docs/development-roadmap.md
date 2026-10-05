# Development roadmap

Phases 0–6 are implemented: foundation, strict API/persistence, durable dispatch,
SLEEP execution, multiple workers with bounded concurrency, DB-time lease recovery, budgeted retries/idempotency and priority/timeout/cancellation/DLQ controls.
Phase 7 onward remains Planned. Authoritative completion status is in
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

## Current completion and next handoff

Phase 6 adds PostgreSQL-authoritative non-preemptive priority, attempt execution
timeouts with budgeted TIMED_OUT outcomes, durable user cancellation and minimal
DLQ pagination/Attempts inspect/manual redrive. Historical Attempts are preserved;
redrive explicitly grants one extra budget and original submission identity stays
unchanged. Priority aging, persistent logs and DLQ UI are unimplemented.

Phase 7 scheduled/capability-aware scheduling remains Planned. External Automation
must review the Phase 6 report and publish a legitimate next prompt before Codex
starts another phase. Codex has not generated that prompt or advanced processed
phase to 6. The retained legacy duplicate-key DB still requires explicit operator
resolution before upgrading from Phase 4; isolated acceptance is not deployment.
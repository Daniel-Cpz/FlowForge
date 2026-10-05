# Development roadmap

Phases 0–5 are implemented: foundation, strict API/persistence, durable dispatch,
SLEEP execution, multiple workers with bounded concurrency, DB-time lease recovery, budgeted retry/backoff/jitter and submission idempotency.
Phase 6 onward remains Planned. Authoritative completion status is in
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

Phase 5 unifies execution attempt budgets, durable RETRYING schedules, concurrent
promotion, bounded equal jitter, failure classification and global submission
idempotency. Permanent/exhausted failures end DEAD_LETTER. It does not implement
priority, generic timeout enforcement, user cancellation or DLQ management.

External Automation must review the Phase 5 report and publish a legitimate next
prompt. Phase 6 is the existing roadmap recommendation only; Codex has not
created a Phase 6 prompt, advanced its processed counter or started that scope.

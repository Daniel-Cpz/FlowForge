# Development roadmap

Phases 0–9 are implemented: foundation, strict API/persistence, durable dispatch,
SLEEP execution, multiple workers with bounded concurrency, DB-time lease recovery, budgeted retries/idempotency and priority/timeout/cancellation/DLQ controls.
Phase 7 adds DB-time delayed Jobs, capability-aware Claim and fixed-interval recurring schedules. Phase 8 adds the React/TypeScript Dashboard, transient WebSocket fanout and REST resync. Phase 9 adds bounded Prometheus/OTLP diagnostics, Grafana provisioning, isolated failure injection and a repeated local benchmark. Phase 10 remains Planned. Authoritative completion status is in
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
Phase 8 exposes Overview, Jobs/detail/Attempts, Workers, Schedules and DLQ through
REST, with transient WS hints and reconnect/30s repair. Cron/edit/pause/resume,
capability routing and priority aging remain planned. Phase 9 supplies metrics,
durable async trace correlation and repeatable failure/measurement harnesses;
production alerting, durable trace storage and deployment remain planned.

External Automation consumed the Phase 8 report and prepared Phase 9. After its
completion, external review and a legitimate next prompt are required before
Phase 10. Codex does not advance last_processed_phase to 9 or generate Phase 10.
Retained duplicate-key data remains schema 4 pending separately authorized
operator resolution; isolated schema-8 acceptance is not deployment or data repair.

# Development roadmap

Phase 0 supplies the foundation; Phase 1 adds strict API and persistence contracts.
All execution phases remain Planned. Authoritative completion status is in
[`automation/state.json`](../automation/state.json). The whole-project
[`README.md`](../README.md) and each independent `docs/reports/phase-N-report.md`
are both required on phase completion. See the
[protocol](phase-automation.md) and [report template](phase-report-template.md).

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

## Next: Phase 2 — Redis Queue + Single Worker Execution

Phase 1 delivers exact Create envelope names, duplicate rejection, null/default
semantics, JSONB regressions, canonical persistence readback, deterministic cursor
pagination and HTTP/database failure coverage. Transaction boundaries are recorded
in ADR 0002. Prompt-source tracking supports manual and automated phase inputs.

Before dispatch, Phase 2 must decide how a persisted job becomes a reconstructible
queue notification despite database/Redis dual-write failures. Add only the
authorized single-worker demonstration in that future phase. Queue and execution
are not implemented here. Measure behavior before making performance claims.

# Development roadmap

Phase 0 is the committed foundation. Phase 1 is in progress; later phases are
Planned. Machine-readable status is in
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

## Next: Phase 1 — Job Persistence + API Correctness

The create/get/list baseline already exists. Remaining work is to define stricter
JSON semantics (including duplicate keys), settle null/default distinctions,
exercise full HTTP-to-database contracts and failure cases, choose stable
pagination semantics, and document transaction boundaries for future transitions.
Do not start execution, scheduling or retry here. Measure behavior before making
performance claims. Each phase must distinguish working code from future design.

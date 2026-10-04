# Development roadmap

Phase 0 supplies the foundation; Phase 1 adds strict API and persistence contracts.
Phase 2 adds durable dispatch and single-worker SLEEP execution; Phase 3 onward
remains Planned. Authoritative completion status is in
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

## Next recommendation: Phase 3 — Multiple Workers + Bounded Concurrency

Phase 1 delivers exact Create envelope names, duplicate rejection, null/default
semantics, JSONB regressions, canonical persistence readback, deterministic cursor
pagination and HTTP/database failure coverage. Transaction boundaries are recorded
in ADR 0002. Prompt-source tracking supports manual and automated phase inputs.

Phase 2 uses a transactional PostgreSQL outbox, bounded dispatcher, Redis Streams
and one serial worker. CAS claim and owner/attempt finalization provide a basis
for Phase 3, but no worker pool or multi-worker operation is implemented here.
Phase 4 must explicitly solve post-claim RUNNING crashes and stale ownership;
QUEUED notification republication does not provide that recovery.

Phase 3 is a recommendation only. Codex has not generated its prompt or started
it; external review and state publication are required before automatic handoff.
Measure behavior before making performance claims.

# Phase reports

The [project README](../../README.md) describes FlowForge as a whole. Every
completed phase gets an independent `phase-N-report.md`. Read
[`automation/state.json`](../../automation/state.json) for the current report;
never infer the phase from report ordering.

- [Report template](../phase-report-template.md)
- [Automation protocol](../phase-automation.md)
- [Automation infrastructure report](phase-automation-infrastructure-report.md)

- [Phase 1 — API and persistence correctness](phase-1-report.md)
- [Phase 2 — Durable dispatch and single-worker execution](phase-2-report.md)
- [Phase 3 — Multiple workers and bounded concurrency](phase-3-report.md)
- [Phase 4 — Heartbeat, leases and crash recovery](phase-4-report.md)

Each numbered report contains that Phase's own evidence. Read state for its published
completion/Git checkpoint; the infrastructure report is independent and cannot
substitute for phase completion.

- [Phase 5 — Budgeted retries and submission idempotency](phase-5-report.md)
- [Phase 6 — Priority, timeout, user cancellation and DLQ](phase-6-report.md)
- [Phase 7 — Delayed Jobs, capability-aware Claim and recurring schedules](phase-7-report.md)
- [Phase 8 — Dashboard, WebSocket fanout and REST resync](phase-8-report.md)
- [Phase 9 — Bounded observability, failure injection and measured local baseline](phase-9-report.md)

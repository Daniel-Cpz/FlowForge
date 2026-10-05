# Architecture decisions

Use sequential ADR files. Include Status, Context, Decision, Consequences and
Alternatives. Accepted decisions may be superseded by a new ADR; retain history.

- [0001 — Phase 0 boundaries](0001-phase-0-boundaries.md)
- [0002 — Phase 1 API contract and persistence boundaries](0002-phase-1-api-contract.md)
- [0003 — Durable dispatch and single worker](0003-durable-dispatch-and-single-worker.md)
- [0004 — Fixed worker slots and process supervision](0004-fixed-worker-pool.md)
- [0005 — DB-time leases, fencing and atomic recovery](0005-db-time-leases-and-recovery.md)

Template:

```markdown
# NNNN — Title
Status: Proposed | Accepted | Superseded by NNNN
## Context
What problem and constraints motivate this decision?
## Decision
What are we choosing and why?
## Consequences
What costs, failure modes and follow-up work result?
## Alternatives
Which simpler or different approaches were considered?
```

- [0006 — Budgeted retries and submission idempotency](0006-budgeted-retries-and-submission-idempotency.md)
- [0007 — Priority, attempt deadlines, cancellation and DLQ](0007-priority-timeout-cancellation-dlq.md)
- [0008 — DB-time eligibility, immutable capabilities and recurring occurrences](0008-time-capabilities-recurring-schedules.md)
- [0009 — Dashboard transient hints and authoritative REST resync](0009-dashboard-realtime-resync.md)
- [0010 — Bounded telemetry and durable async trace context](0010-observability-cardinality-trace-isolation.md)

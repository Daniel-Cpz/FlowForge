# Architecture decisions

Use sequential ADR files. Include Status, Context, Decision, Consequences and
Alternatives. Accepted decisions may be superseded by a new ADR; retain history.

- [0001 — Phase 0 boundaries](0001-phase-0-boundaries.md)
- [0002 — Phase 1 API contract and persistence boundaries](0002-phase-1-api-contract.md)
- [0003 — Durable dispatch and single worker](0003-durable-dispatch-and-single-worker.md)
- [0004 — Fixed worker slots and process supervision](0004-fixed-worker-pool.md)

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

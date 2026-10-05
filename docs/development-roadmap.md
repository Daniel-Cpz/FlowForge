# Development roadmap

Phases 0–9 are implemented: foundation, strict API/persistence, durable dispatch,
SLEEP execution, multiple workers with bounded concurrency, DB-time lease recovery, budgeted retries/idempotency and priority/timeout/cancellation/DLQ controls.
Phase 7 adds DB-time delayed Jobs, capability-aware Claim and fixed-interval recurring schedules. Phase 8 adds the React/TypeScript Dashboard, transient WebSocket fanout and REST resync. Phase 9 adds bounded Prometheus/OTLP diagnostics, Grafana provisioning, isolated failure injection and a repeated local benchmark. Phase 10 — Production Hardening + CI + Local Release Acceptance — is completed under explicit owner scope adjustment (ADR 0012). v1.0.0 Released; real cloud/SSH/GHCR deployment is optional. Authoritative completion status is in
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
| 10 | Production Hardening + CI + Local Release Acceptance |

## Current implementation and next handoff

Phase 7 implements delayed time eligibility, immutable canonical Worker
capabilities, per-worker eligible priority, and fixed-interval templates. Bounded
PG transactions create a unique ordinary Job/intent and advance next_run_at;
missed middle intervals are skipped. Schedule cancel leaves existing Jobs alone.
Phase 8 exposes Overview, Jobs/detail/Attempts, Workers, Schedules and DLQ through
REST, with transient WS hints and reconnect/30s repair. Cron/edit/pause/resume,
capability routing and priority aging remain planned. Phase 9 supplies metrics,
durable async trace correlation and repeatable failure/measurement harnesses;
production alerting and durable trace storage remain planned. Deployment tooling
is implemented/config validated, not cloud-deployed or publication-validated.

External Automation consumed Phase 9 and prepared the original cloud-required
Phase 10. Its unchanged progress report records the accurate historical BLOCKED
state. The owner subsequently removed cloud/SSH/GHCR/publication/deployment gates
from v1.0.0 via [ADR 0012](decisions/0012-v1-local-production-acceptance.md).
The separate [final completion evidence](reports/phase-10-completion.md) covers
the new manual scope and successful final regression. Roadmap 0–10 is complete;
no Phase 11 is generated. The owner explicitly authorized final release on
2026-10-06; [independent review](reports/v1.0.0-release-review.md) PASS.
Main CI, annotated tag and published GitHub Release are verified; see the
[publication receipt](reports/v1.0.0-release.md). Lifecycle is maintenance/portfolio.
Phase state retains last_processed_phase=9 for the separate GPT Automation;
release review is recorded independently. After publication, lifecycle is
maintenance/portfolio; optional work requires a later owner decision.
Retained duplicate-key data remains schema 4 pending separately authorized
operator resolution; isolated schema-8 acceptance is not deployment or data repair.

## Optional future work (no Phase 11)

Real authorized VPS/AWS deployment, GHCR publication, protected SSH/Environment
deployment, public DNS/ACME, off-host backups, HA/autoscaling and infrastructure
orchestration only if justified by a future need. Not deployed to a real VPS/EC2
by project scope decision. No optional item blocks v1.0.0 readiness.

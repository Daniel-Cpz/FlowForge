# 0012 — v1 local production acceptance and release readiness

Status: Accepted by explicit project owner decision, 2026-10-05.
Supersedes ADR 0011's cloud/publication/deployment completion gates; its
implementation and operational safety boundaries remain in force.

## Context

FlowForge is a personal backend/distributed systems engineering project.
Phase 10's original automated scope required a real authorized VPS/EC2 and a
protected deployment. Local production infrastructure and CI passed, but no
authorized target existed; the historical progress report accurately records
BLOCKED. That report and the original prompt remain unchanged.

Lease authority, recovery, retry, submission idempotency, scheduling and Job FSM
correctness can be exercised with real PostgreSQL/Redis, isolated production
Compose, integration tests, failure injection and GitHub-hosted CI. A real cloud
host adds operational evidence, but is not necessary for v1's correctness scope.

## Decision

The owner explicitly changes Phase 10 to **Production Hardening + CI + Local
Release Acceptance**. v1.0.0 does not require real cloud deployment or publication.

Required: production-like containerized runtime; real PostgreSQL and Redis;
production TLS/auth/config safety; failure and lease recovery; graceful shutdown;
retry/DLQ; persistence; disposable populated backup/restore; REST/WS recovery;
full standard CI, relevant race/frontend/workflow checks; accurate docs, independent
completion report, safe Git evidence and consistent schema-1 phase state.

Optional: VPS/EC2, real SSH/protected Environment deployment, GHCR publication,
public DNS/ACME, cloud firewall/host reboot acceptance, off-host backups, HA,
autoscaling, Kubernetes and Terraform. Retain existing deployment tooling as
implemented and locally/config validated; it is not production-deployed or
publication-validated. No host guess, credentials placeholder or weaker safety gate.

Preserve the original BLOCKED progress report; create `phase-10-completion.md`
for the new manual scope and evidence. The narrow Phase 10 manual report-path
exception preserves report identity, confinement, committed SHA and tag checks.
Schema fields do not change. `last_processed_phase` remains external review only.
No Phase 11, cloud-named completion tag or automatic main/version release.

## Consequences

Release criteria are reproducible without a long-running server and focus on
distributed correctness. Local Docker/GitHub-hosted acceptance proves the tested
reference topology, not a production SLA or general workload scaling.

Do not claim AWS deployment, cloud production validation, public production,
HA, Kubernetes production, real cloud disaster recovery, zero downtime or
exactly-once external effects. v1 delivery remains at-least-once; submission
idempotency deduplicates requests and lease fencing protects DB authority.
Future effectful executors need their own deduplication/fencing.

Default-branch workflow dispatch, actual publication/deployment, host security,
certificate rotation and off-host disaster recovery require a later operator
decision. v1.0.0 READY is readiness, not a published version tag; external review
and the repository release policy still govern main and final version release.

## Alternatives

Keeping a mandatory cloud gate would retain an unrelated external dependency.
Deleting deployment code loses useful engineering work. Rewriting the old report
as PASS would falsify evidence; a separate completion report preserves the chain.

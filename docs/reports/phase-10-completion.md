# FlowForge Phase 10 Completion Report

## Phase

Phase 10

Production Hardening + CI + Local Release Acceptance

## Status

IN_PROGRESS

Manual owner finalization; final release regression and Git publication pending.
This report is not referenced by state.report until all revised gates pass.

## Scope Adjustment

The original automated Phase 10 required an authorized VPS/EC2, GHCR publication
and protected SSH/Environment deployment. Infrastructure/local CI succeeded, but
the absence of an authorized target accurately caused BLOCKED. The owner explicitly
changed v1.0.0 scope on 2026-10-05: real cloud/publication/deployment is optional.
[ADR 0012](../decisions/0012-v1-local-production-acceptance.md) records the decision
and new required gates. This is a scope change, not a retroactive cloud PASS.

The original `automation/prompts/phase-10.md`, ADR 0011 and
`docs/reports/phase-10-report.md` are retained unchanged. Automated prompt existed
but was superseded by explicit user instructions for this same phase's finalization.
State uses manual/null; the external-review counter stays 9. No Phase 11.

## Summary

Formal owner scope adjustment, documentation/claim audit, narrow report-path
protocol validation and final standard regression. Existing deployment code and
all TLS/auth/SSH/backup/migration/health safety mechanisms remain intact.
Core runtime, scheduling/performance paths and historical evidence are unchanged.

## Implemented

- Durable PG Job/outbox/Attempt FSM, budgeted retry/equal jitter, exact-key
  submission idempotency, priority, timeout/cancellation and DLQ redrive.
- Multiple bounded Workers, DB-time leases/heartbeat/fencing/crash recovery;
  delayed/capability-aware/fixed-interval recurring scheduling.
- React/REST/transient WS/reconnect repair, bounded Prometheus/OTLP diagnostics
  and existing failure/benchmark harnesses.
- Independent production Compose, real PG TLS, strong Redis auth, private/public
  overlays, static Caddy REST/WS gateway, image identity and persistent volumes.
- Backup/restore, lock/drain/migrate/health/atomic metadata/safe abort scripts;
  optional GHCR/protected SSH workflows and strict host verification.
- ADR 0012, current docs and schema-1 protocol update. Separate manual Phase 10
  report preserves BLOCKED progress; all identity/Git/confinement checks retained.

## Validated

Prior clean checkpoint37aab0c CI run37304523689 passed all Go/race/frontend/
workflow/config and real disposable production-stack checks, recorded in the
historical progress report. Final regression for this manual scope is pending;
the final result will be recorded here, independently of that earlier evidence.

## Not Implemented

Not deployed to a real VPS/EC2 by project scope decision. Real SSH/protected
Environment deployment, published GHCR digests, public DNS/ACME, cloud firewall/
host reboot acceptance and real cloud disaster recovery are NOT EXECUTED.
No application users/RBAC, HA, autoscaling, multi-region, Kubernetes or Terraform.

## Experimental

Single-host production-like reference topology tested with Docker and GitHub-hosted
CI, not a deployed production SLA. Public-mode HTTPS uses a trusted disposable CA,
not public ACME. SLEEP remains the only executable Job type.

## Planned

Optional authorized cloud/GHCR/protected deployment, public DNS/ACME, off-host
backups and additional operational validation require later decisions. No next
numbered phase. Final main/version release follows external review and authorization.

## Tests

Final release regression: PENDING. Uses the existing `.github/workflows/ci.yml`
without reducing checks: `sh scripts/check.sh`; the existing broad race suite;
frontend npm ci/typecheck/tests/build; telemetry/harness validation; actionlint,
shell/deploy fixtures, production config and `production-stack.py` acceptance.

Focused validator tests, docs/claim audit, `git diff --check` and retained-data
readonly audit are recorded as they execute. No new release pipeline invented.
Performance path unchanged; Phase 9 benchmark not rerun.

## Failure / Edge Case Validation

Final regression must preserve Worker hard-kill/lease expiry/new Attempt/retry
success, graceful cancellation/registry shutdown, DLQ/redrive, schedule/capability,
Redis NOAUTH/strong-password checks, real PG TLS, trusted-CA HTTPS/WS, reconnect
REST repair, populated archive restore and named-volume restart persistence.
Only final executed PASS evidence will certify these gates.

Validator rejects automated/other-phase use of the new manual report path,
historical progress as completion, unfinished completion identity and report
missing from its recorded commit. Existing schema/path/symlink/tag checks remain.

## Known Limitations

- At-least-once delivery/execution can repeat; submission idempotency deduplicates
  requests, not external effects. Lease fencing protects DB writes only.
- Single-host failure domain, maintenance downtime, no HA/SLA/scaling promise.
  Phase 9's SLEEP benchmark is machine/workload-specific, not linear scalability.
- PG sslmode=require encrypts without certificate identity verification; certificate
  rotation is operator work. Redis lacks TLS on the single-host private network.
  Gateway upstream default user remains; backend uses non-root flowforge.
- Basic Auth is demo access, not app RBAC. Browser E2E/public ACME/real cloud
  deployment not run. Optional diagnostics have no claimed cloud UP/dashboard evidence.
- Local logical backups do not prove off-host disaster recovery. Safe abort requires
  operator schema compatibility/restore decisions, never automatic migrate down.
- Retained development DB readonly audit: schema4, one duplicate group/four rows,
  no non-null-key unique index. Migration000005 intentionally blocks its upgrade;
  current schema8 isolated tests validate uniqueness/concurrent replay. This is
  preserved pre-Phase5 development data, not evidence of a current-runtime bug.
  No keys/payloads are printed and no data is deleted, merged or changed.

## Release Readiness

NOT READY — final regression and published completion evidence pending.
Real cloud/publication is not a release gate. See [release policy](../release-readiness.md).

## Git

Branch: codex/phase1-api-correctness.
Previous progress metadata checkpoint: b9aced4865cc4d6e005602001de8ca42982f82fb.
Final implementation/report checkpoint: pending after tests.
Push: pending final checkpoint. Tag: none; no cloud-named tag.
Main remains aa96182a037bfc502927125246e07733d0e8dbd3; review required before
main/version release. Later metadata records the real checkpoint SHA; no self-reference.

## Documentation Updated

README; architecture/deployment/Dashboard/observability/worker operations;
development roadmap; reports/ADR index; phase automation protocol; ADR 0012;
release readiness and this new report. Historical reports/prompts/migrations/
benchmarks/tags preserved unchanged.

## Next Recommended Phase

No Phase 11. External review of completed Phase 10, then explicit final main/
version release decision. Codex does not manufacture review or advance its counter.

## Notes

2026-10-05 owner scope decision is the source of the new completion criteria.
Cloud removal never weakens TLS/auth/SSH verification or correctness gates.
The schema remains version1 and no new state fields are added. Completion and
v1.0.0 READY are published only after final tests and a real report checkpoint.

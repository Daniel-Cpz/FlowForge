# v1.0.0 release readiness

Current status: **v1.0.0 READY**. Owner Phase 10 finalization and final standard
[regression run37310265921](https://github.com/Daniel-Cpz/FlowForge/actions/runs/37310265921)
PASS under ADR 0012; completed state pins the real report checkpoint.
Read [state](../automation/state.json) and the exact
[completion report](reports/phase-10-completion.md) for published evidence.

## Required gates

[ADR 0012](decisions/0012-v1-local-production-acceptance.md) requires real PG/Redis
production-like Docker runtime, TLS/auth/config safety, lifecycle/lease/retry/DLQ/
scheduling/WS correctness, failure recovery, graceful shutdown, persistence and
populated disposable backup/restore. Use standard full CI (Go/vet/integration/
race, frontend typecheck/tests/build, workflow/shell/config and production-stack
acceptance), accurate docs, preserved history, no secrets/generated data, a real
Git checkpoint/normal push and consistent completed phase state.

## Optional release enhancements

Real VPS/EC2, public DNS/ACME, cloud firewall/reboot acceptance, GHCR publication,
real protected SSH/Environment deployment and off-host backup/HA/autoscaling are
not v1.0.0 gates. Existing tooling is implemented and locally/config validated;
publication and actual production deployment are NOT EXECUTED. No invented digests,
cloud claims, placeholder keys or relaxed host verification.

## Publication policy

v1.0.0 READY means the revised engineering gates passed; it does not mean a
GitHub Release, version tag, default-main merge or cloud deployment happened.
The shared authoritative branch remains `codex/phase1-api-correctness`; its name
does not define the phase. Repository protocol requires external review followed
by an explicit final main/version release decision. Keep main and tags unchanged
during this finalization; no `phase10-cloud-cicd` tag.

After review, fetch and check main ancestry/remote state before any authorized
fast-forward or ordinary merge. Never force push, reset or rewrite history.
If authorized to publish v1.0.0, use the existing annotated tag convention and
verify its checkpoint and remote target. No formal Phase 11 is generated.

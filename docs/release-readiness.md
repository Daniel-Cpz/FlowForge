# v1.0.0 release readiness

Current status: **v1.0.0 Release Ready; RELEASE REVIEW: PASS**. Owner Phase 10 finalization and final standard
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

The owner explicitly authorized independent review, main merge, annotated
v1.0.0 and GitHub Release on 2026-10-06. The
[review](reports/v1.0.0-release-review.md) independently checks the completed
Phase 10 state/checkpoint, actual candidate CI, history, claims, tracked secrets/
artifacts, docs and version policy. Its PASS permits the remaining publication
gates; it does not mean GitHub publication or cloud deployment already happened.

Refresh refs/tags and clean-tree checks; prefer a normal fast-forward when main
is an ancestor. Push/fetch/read back main and await actual main-push CI SUCCESS
before tagging. No force push, reset, history rewrite or tag replacement. The
annotated v1.0.0 uses message "FlowForge v1.0.0" and pins the final release
commit; verify its remote object/target before publishing the non-draft GitHub
Release titled "FlowForge v1.0.0" with the committed [notes](releases/v1.0.0.md).
Re-read the published Release independently. Until that succeeds README stays
Release Ready. A later publication receipt records the actual event/IDs and
updates Released status only; it does not add missing release functionality or
move the immutable tag. Main becomes the shared source after verified publication.

The project version is the annotated tag/Release. The private frontend's 0.8.0
package/lock version is component metadata; v1 API/Redis/WS/cursor fields are wire
formats, not project version constants. Docker identity uses full source SHA and
digests. No arbitrary version rewrites are needed. Phase state remains completed
at its original report checkpoint, with null phase tag and next prompt; separate
release evidence records the version tag. No Phase 11 or schema expansion.

After publication, lifecycle is maintenance/portfolio and optional future work.
Real VPS / AWS deployment is intentionally outside the required v1.0.0 scope and was not performed.

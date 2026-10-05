# Production release bundle

See [deployment runbook](../docs/deployment.md). Phase10 infrastructure is locally
implemented and validated locally and in CI. ADR 0012 makes actual cloud/SSH/
protected Environment deployment and GHCR publication optional, not completion
gates. Tooling is implemented/config validated, not production-deployed or
publication-validated; see the [completion report](../docs/reports/phase-10-completion.md).

Independent compose.prod.yml has no builds/published ports, real PG TLS/Redis
passwords, persistence/init/restart/health/log limits. Select exactly one private
or public port overlay. gateway/ builds lockfile React static assets and contains
matching Caddy configs. scripts/ implements bootstrap/literal env parsing/flock/
backup/drain/migrate/health/atomic metadata and pinned-SSH digest deployment.
Safe abort is default rollback; no automatic migrate-down/unknown compatibility.

From repository root, `python deploy/tests/production-stack.py` builds/tests only
generated ffp10 resources and ignored fixture secrets; retained data is never
mounted. Tests real TLS/auth/WS/lease recovery/disposable restore/persistence;
test-only public HTTPS trusts a disposable local CA, not public ACME.
Bash tests: `bash deploy/tests/state-machine.sh`, `bash deploy/tests/remote-safety.sh`.
config-safety.py requires test-only PyYAML==6.0.2. Production stack uses stdlib.
Compose>=2.24.4 required for test !override. Ownership guards precede cleanup.

Publication copies Phase9 provisioning into the versioned deployment artifact;
use that artifact on the server, not an incomplete source-directory copy. Server
scripts require full SHA/exact GHCR digest; local test tags are fixture-only.
No main merge/final tag/Phase11/retained-data repair is authorized by this bundle.

# Single-host release and recovery

Phase 10 production infrastructure is implemented and validated locally and in
GitHub CI. Under [ADR 0012](decisions/0012-v1-local-production-acceptance.md), real
cloud deployment, GHCR publication and protected Actions deployment are optional,
not v1.0.0 completion gates. No real host is authorized; these paths are implemented/
config validated, not production-deployed or publication-validated. Private mode
is selected. See the unchanged historical [BLOCKED progress report](reports/phase-10-report.md)
and new [completion evidence](reports/phase-10-completion.md).

Not deployed to a real VPS/EC2 by project scope decision. The host/SSH commands
below are an optional operator runbook, not actions performed during finalization.
Never weaken host verification, TLS/auth or migration/backup/health checks.

## Target and access boundaries

Ubuntu 24.04-compatible amd64, Docker Engine/Compose >=2.24.4, Bash, curl,
OpenSSL, coreutils and util-linux/flock. Reserve at least 2 GiB free disk for
preflight, more for real backups. Use a non-root deploy user; Docker group is
root-equivalent. Operator configures key-only SSH, restricted source ranges for
port22 where practical, UFW/cloud security groups, automatic security updates,
UTC and disk/log monitoring. Bootstrap does not modify firewall or SSH settings.

Use a fresh compatible DB. Retained development DB remains schema4 with one
legacy duplicate-key group; never seed from it, bypass migration000005 or repair
its data automatically. No Phase10 migration is added; current schema is8.

Production Compose is independent of development: no builds/host sources/socket,
backend non-root, init/restart/healthchecks, 30s stop grace and 10MiB x3 log rotation.
One bridge network and named volumes. Default 2 Workers/C1, configurable1..4;
Phase9's variable scaling does not justify16 Workers or a production SLA.

### Private mode (chosen)

Only SSH is externally open. Gateway binds127.0.0.1:8180; API/PG/Redis/Worker
metrics/OTLP have no host ports. Optional Prometheus/Grafana bind127.0.0.1:9090/3000.
Access through SSH, using the exact origin and tunnel port configured for WS:

```sh
ssh -o StrictHostKeyChecking=yes -L 8180:127.0.0.1:8180 DEPLOY_USER@AUTHORIZED_HOST
# Browser: http://127.0.0.1:8180
# Optional diagnostics: add -L 9090:127.0.0.1:9090 -L 3000:127.0.0.1:3000
```

localhost is a different origin. HTTP is inside the SSH tunnel/private network;
this optional remote mode is a private demo, not public HTTPS. SSH/host access is the boundary,
not application users/sessions/permissions. Public metrics are always blocked
by the gateway, including through the private tunnel.

### Public mode (explicit operator choice)

Set FLOWFORGE_MODE=public, FLOWFORGE_PUBLIC_HOST to a DNS hostname already pointing
to the host, FLOWFORGE_BASIC_USER, FLOWFORGE_BASIC_HASH and FLOWFORGE_WS_ORIGINS=
https://hostname in server env. Generate bcrypt interactively using Caddy's
hash-password; store the literal hash in dotenv single quotes so `$` is preserved.
Do not place plaintext passwords in shell history or commit password/hash.
Publish only gateway80/443 and SSH; Caddy issues HTTPS and redirects HTTP.
Basic Auth runs before static/REST/WS. `/metrics*` is404 even authenticated.
The private8080 gateway health listener has no public mapping. No public IP/plain
HTTP/no-auth acceptance is permitted. Actual domain/ACME/authenticated routing
must be verified on the host; local disposable-CA HTTPS tests prove no public
certificate issuance. Basic Auth is a demo boundary, not per-user authorization.

## Host-local bootstrap and TLS

Operator installs Docker and creates `/opt/flowforge` owned by the deploy user;
transfer reviewed bootstrap script and run once:

```sh
bash bootstrap.sh /opt/flowforge
```

It refuses existing env/key, generates independent256-bit hex PG/Redis passwords
into `.env.prod` mode0600 and a3072-bit self-signed PG certificate (365days). Key
mode0600/UID70 matches postgres:18-alpine; only PostgreSQL mounts the TLS directory.
Application `FLOWFORGE_ENV=production` uses sslmode=require: encryption on the
single host, not CA/hostname identity verification. Production TLS protection is
preserved. Rotate certificate before expiry in a maintenance window. Redis has
a required strong password/private port and no TLS on this single host; reassess
transport encryption before moving to multiple hosts.

Server layout: `.env.prod`, `secrets/postgres/`, `backups/`, `releases/<full SHA>/`,
`deploy.lock`, `current.env`, `previous.env`. Restrict parent directories. No Git clone/server
build, private key/hash/token/archive or volume data in Git/Actions logs.

## Validated CI and optional GHCR / protected deployment

CI retains full Go/race/frontend/telemetry/harness checks and adds inert deploy/
SSH failure tests, config validation and real disposable production-stack smoke.
Release and deploy workflows are workflow_dispatch only; never push/PR deploy.
CI is verified by actual successful run evidence. The release-images GHCR and
protected deployment workflows are implemented/config validated, but have not
been executed successfully as publication/deployment; no GHCR digest is claimed.

release-images.yml reuses CI then builds/tests the exact image IDs before pushing
`ghcr.io/daniel-cpz/flowforge-{backend,gateway}:<full SHA>`. Existing SHA tags are
refused. Deployment uses `@sha256:<digest>`, not latest, and verifies OCI source/
revision matches the bundle SHA. Artifact includes images.env, bundle tar/checksum
and versioned Compose/Caddy/scripts plus Phase9 observability configs; retention
30days. Partial push produces no successful deploy artifact; use a new reviewed
revision after resolving the failure.

Create Environment **flowforge-cloud**, restrict deployment branch and configure
required reviewers where supported. Set secrets in GitHub, not chat/repo:

| Secret | Purpose |
|---|---|
| FLOWFORGE_DEPLOY_HOST | Explicitly authorized host |
| FLOWFORGE_DEPLOY_USER | Dedicated SSH deploy user |
| FLOWFORGE_DEPLOY_SSH_KEY | Deployment private key |
| FLOWFORGE_DEPLOY_KNOWN_HOSTS | Host-key line verified out of band |

PG/Redis/auth credentials remain server-local. Actions uses its short-lived token
for GHCR via stdin and a temporary remote DOCKER_CONFIG, logout/removal on exit.
Private packages must grant repository workflow read access. Runner SSH key0600
is deleted on exit. Missing secret/host-key mismatch/preflight fails before upload;
StrictHostKeyChecking=yes, never disabled. Neither SSH keys nor registry token
are echoed or persistently stored on the remote host.

1. Dispatch Release immutable images on the exact shared branch revision.
2. Wait for success; record run URL, SHA and both digests.
3. Dispatch Controlled cloud deploy on the same revision, input release_run_id.
4. Approve Environment if configured; record successful deployment URL/evidence.

GitHub requires the workflow_dispatch definition on the repository's default
branch for dispatch eligibility. At Phase 10 finalization, new workflows lived
only on the shared branch and main remained early foundation. The separate
owner-authorized final release merges the reviewed workflows to main; publication
does not dispatch image or cloud deployment workflows. Any later deployment
requires its own authorized target and protected configuration. See [GitHub manual workflow
docs](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow).

## Deploy sequence and rollback policy

`deploy.sh ENV SHA BACKEND_DIGEST GATEWAY_DIGEST` runs from the installed SHA
bundle. Server flock serializes deploy/backup. Validate literal env/paths/files/
permissions/disk/Docker; pull exact images and verify revision; validate Caddy;
start dependencies; pg_dump-Fc and verify archive; stop gateway/API/Workers;
migrate up once; start API/configured Workers/gateway; bounded health/readiness;
check private route/static page or public HTTPS auth denial; atomically rename
current.env. Current metadata changes only after all gates. Health waits90s per
service; migration uses existing30s deadline. No mixed-version/zero-downtime claim.

Before-drain failure retains the old app. After-drain failure stops app containers,
retains dependencies/volumes/backups and old metadata. **Safe abort is default
rollback**: no automatic migrate down, app downgrade with guessed compatibility,
data repair or volume deletion. Operator compares current schema to the previous
app's verified forward compatibility before selecting an old bundle/digests and
its health gates. Unknown/incompatible means remain stopped and decide a restore
into a new DB from the verified archive; production DB replacement/data-loss
window requires an explicit operator decision.

Services persist through process/host restart via named volumes/unless-stopped;
intentional compose stop needs up-d to resume. Restart never runs migrations.

## Backup and disposable restore

Run `backup.sh /opt/flowforge/.env.prod DEPLOYED_SHA` from the current bundle.
Same lock; pg_dump-Fc, pg_restore--list, atomic rename/mode0600, UTC+SHA+PID name;
keep newest7 complete dumps. Pre-migration archives use the actual previous
deployed SHA; first-install archives explicitly use bootstrap. Backup failure aborts before migration and removes
only partial archive. These are logical local backups, not off-host disaster
recovery; separately arrange protected off-host copies/retention.

In maintenance, stop gateway/API/Workers, then from `/opt/flowforge`:

```sh
bash releases/SHA/scripts/restore-test.sh /opt/flowforge/.env.prod BACKEND_DIGEST GATEWAY_DIGEST
```

Script refuses running writers, restores only a generated disposable DB, compares
schema/sorted Job/Attempt/Schedule IDs, drops only that DB, and never replaces
authoritative data. Resume using current bundle Compose/explicit worker count.
Budget disk for archive plus restore DB. Local tests verify populated data and
post-restart Job readability. Actual cloud backup/restore is optional future
operational evidence, not a v1.0.0 gate or off-host disaster-recovery proof.

## Optional future real cloud acceptance

This checklist applies only after a future authorized cloud deployment decision.
It is NOT EXECUTED and is not required for Phase 10/v1.0.0 under ADR 0012.

Record provider/OS/CPU/RAM/Docker/Compose, private mode, worker count/C1, revision/
digests and profile state; no secrets/optional private identifiers. Require fresh
schema8, static assets, gateway REST/WS/reconnect/REST repair, SLEEP success,
long-SLEEP Worker kill/recovery/new Attempt, capability schedule, DLQ/redrive,
internal metrics/no public listeners (host ss and external/security-group check),
named-volume restart persistence, populated disposable backup restore and successful
protected Actions deployment. Reboot only if authorized; service restart suffices.

Optional FLOWFORGE_OBSERVABILITY=true: internal DNS Worker scrape,7day/1GiB Prom
retention, private Grafana anonymous Viewer and internal Collector. If enabled,
verify UP targets and dashboard. Disabled stack is not cloud Grafana deployment.
Use matching3000 SSH tunnel for the Dashboard's existing local Grafana link.

## Primary references

[Caddy Basic Auth](https://caddyserver.com/docs/caddyfile/directives/basic_auth),
[Caddy proxy/WS](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy),
[Docker production Compose](https://docs.docker.com/compose/how-tos/production/),
[GitHub publication](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images).

# 0011 — Single-host immutable release and maintenance gates

Status: Accepted implementation; actual cloud acceptance pending.

## Context

Phase9 proves local diagnostics/correctness and variable scaling. Phase10 requires
a protected reproducible single-host release with recoverable migrations, without
new orchestration or automatic legacy-data repair.

## Decision

Independent production Compose, default2 C1 Workers, lockfile static React build
and Caddy. Chosen private mode is loopback+SSH; optional public is HTTPS/Basic Auth
for static/REST/WS. PG server-local TLS require, strong-password internal Redis,
private optional diagnostics. Test image IDs before GHCR push; pin digests/bundle
SHA and OCI revision. Dispatch-only protected Environment, pinned SSH, ephemeral
registry credential and server flock. Archive before migration, drain, one-shot
up, bounded health, atomic record. Default rollback is safe abort, never automatic
schema down/image downgrade with unverified forward compatibility.

## Consequences

Downtime/single-host failure domain, no HA/SLA/scaling claim. TLS require encrypts
without CA identity verification; single-host boundary only. Docker group is
root-equivalent; Basic Auth is not RBAC. Backups need off-host storage and TLS
expiry monitoring. Host/GHCR/Actions/cloud acceptance are completion gates;
local tests cannot substitute. Default-branch dispatch eligibility needs an
operator decision, no automatic main merge or final version release.

## Alternatives

Nginx/Certbot needs more coordination. Kubernetes/Terraform/multi-host are outside
scope. Mutable tags/server rebuilds weaken artifact identity. Automatic migrate
down or guessing schema compatibility risks authoritative state.

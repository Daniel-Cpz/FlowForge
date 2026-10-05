#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "$0")/common.sh"
export FLOWFORGE_ENV_FILE=${1:?server env file required}
load_env "$FLOWFORGE_ENV_FILE"
export FLOWFORGE_RELEASE_SHA=${2:?release SHA required}
export FLOWFORGE_BACKEND_IMAGE=${3:?backend digest required}
export FLOWFORGE_GATEWAY_IMAGE=${4:?gateway digest required}
validate_release
[[ $BUNDLE == "$FLOWFORGE_HOME/releases/$FLOWFORGE_RELEASE_SHA" ]] || fail 'bundle must be installed in its SHA release directory'
acquire_lock
step=preflight; drained=false; migrated=false
cleanup() {
  local rc=$?
  trap - EXIT
  if (( rc != 0 )); then
    printf '%s\n' "Deployment aborted at $step; previous release metadata preserved" >&2
    if [[ $drained == true ]]; then
      dc stop gateway api worker >/dev/null 2>&1 || true
      # Safe abort is the default. Never down volumes, migrate down, or guess
      # previous-image forward compatibility. Operator follows rollback runbook.
      printf '%s\n' 'Application stopped; dependencies/data retained. Operator compatibility/restore decision required.' >&2
    fi
  fi
  rm -f -- "$FLOWFORGE_HOME/current.env.partial"
  rm -f -- "$FLOWFORGE_HOME/previous.env.partial"
  flock -u 9 || true
  exit "$rc"
}
trap cleanup EXIT
command -v docker >/dev/null; command -v curl >/dev/null
docker info >/dev/null; docker compose version >/dev/null
[[ $(df -Pk "$FLOWFORGE_HOME" | awk 'NR==2 {print $4}') -ge 2097152 ]] || fail 'at least 2 GiB free disk required'
[[ -s $FLOWFORGE_HOME/secrets/postgres/server.crt && -s $FLOWFORGE_HOME/secrets/postgres/server.key ]] || fail 'bootstrap TLS files missing'
[[ $(stat -c %a "$FLOWFORGE_HOME/secrets/postgres/server.key") == 600 ]] || fail 'TLS key must be mode 0600'
dc config --quiet
if [[ -e $FLOWFORGE_HOME/current.env ]]; then
  [[ -f $FLOWFORGE_HOME/current.env && ! -L $FLOWFORGE_HOME/current.env && $(stat -c %a "$FLOWFORGE_HOME/current.env") == 600 ]] || fail 'invalid previous release record'
  # Keep previous image refs even after a successful current.env replacement.
  cp -- "$FLOWFORGE_HOME/current.env" "$FLOWFORGE_HOME/previous.env.partial"
  chmod 600 "$FLOWFORGE_HOME/previous.env.partial"
  mv -- "$FLOWFORGE_HOME/previous.env.partial" "$FLOWFORGE_HOME/previous.env"
fi
step=pull
dc pull
for image in "$FLOWFORGE_BACKEND_IMAGE" "$FLOWFORGE_GATEWAY_IMAGE"; do
  [[ $(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image") == "$FLOWFORGE_RELEASE_SHA" ]] || fail 'image revision does not match bundle SHA'
done
step=gateway-config
dc run --rm --no-deps gateway caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null 2>&1 || fail 'gateway config rejected'
step=dependencies
dc up -d postgres redis
wait_health postgres 1; wait_health redis 1
step=backup
backup_database
step=drain
drained=true
dc stop gateway api worker
step=migration
dc run --rm --no-deps migrate
migrated=true
[[ $(schema_version) == 8 ]] || fail 'unexpected schema; this release requires schema 8'
step=application
dc up -d api worker --scale "worker=$FLOWFORGE_WORKER_COUNT"
wait_health api 1; wait_health worker "$FLOWFORGE_WORKER_COUNT"
step=gateway
dc up -d gateway
if [[ $FLOWFORGE_OBSERVABILITY == true ]]; then dc up -d prometheus grafana otel-collector; fi
wait_health gateway 1
dc exec -T api wget -q -O /dev/null http://127.0.0.1:8080/ready
step=gateway-route
if [[ $FLOWFORGE_MODE == private ]]; then
  curl --fail --silent --show-error --max-time 10 "http://127.0.0.1:$FLOWFORGE_PRIVATE_PORT/ready" >/dev/null
  curl --fail --silent --show-error --max-time 10 "http://127.0.0.1:$FLOWFORGE_PRIVATE_PORT/" | grep -q '<div id="root">'
else
  # Real certificate validation, not --insecure; no Basic Auth secret is printed.
  [[ $(curl --silent --show-error --max-time 15 -o /dev/null -w '%{http_code}' "https://$FLOWFORGE_PUBLIC_HOST/api/v1/jobs") == 401 ]] || fail 'public access control/HTTPS failed'
fi
step=record
cat > "$FLOWFORGE_HOME/current.env.partial" <<EOF
FLOWFORGE_RELEASE_SHA=$FLOWFORGE_RELEASE_SHA
FLOWFORGE_BACKEND_IMAGE=$FLOWFORGE_BACKEND_IMAGE
FLOWFORGE_GATEWAY_IMAGE=$FLOWFORGE_GATEWAY_IMAGE
SCHEMA_VERSION=8
MODE=$FLOWFORGE_MODE
DEPLOYED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
chmod 600 "$FLOWFORGE_HOME/current.env.partial"
# Rename on the same filesystem only after all deployment gates passed.
mv -- "$FLOWFORGE_HOME/current.env.partial" "$FLOWFORGE_HOME/current.env"
printf '%s\n' "Deployment health gates PASS; revision $FLOWFORGE_RELEASE_SHA"

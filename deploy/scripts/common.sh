#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
fail() { printf '%s\n' "FAIL: $*" >&2; exit 1; }
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
BUNDLE=$(cd -- "$SCRIPT_DIR/.." && pwd -P)
load_env() {
  local file=$1 line key value
  [[ -f $file && ! -L $file ]] || fail 'missing regular server env file'
  [[ $(stat -c %a "$file") == 600 ]] || fail 'server env must be mode 0600'
  while IFS= read -r line || [[ -n $line ]]; do
    [[ -z $line || $line == \#* ]] && continue
    [[ $line != *$'\r'* && $line == *=* ]] || fail 'invalid env format'
    key=${line%%=*}; value=${line#*=}
    case "$key" in
      FLOWFORGE_HOME|FLOWFORGE_PROJECT|FLOWFORGE_MODE|FLOWFORGE_PRIVATE_PORT|FLOWFORGE_PUBLIC_HOST|FLOWFORGE_BASIC_USER|FLOWFORGE_BASIC_HASH|FLOWFORGE_POSTGRES_PASSWORD|FLOWFORGE_REDIS_PASSWORD|FLOWFORGE_WS_ORIGINS|FLOWFORGE_WORKER_COUNT|FLOWFORGE_WORKER_CONCURRENCY|FLOWFORGE_OBSERVABILITY|FLOWFORGE_OTEL_ENABLED) ;;
      *) fail 'unexpected server env key';;
    esac
    # Literal dotenv single quotes; never source/eval a credentials file.
    if [[ $value == \'*\' ]]; then value=${value:1:${#value}-2}; fi
    [[ $value != *$'\n'* ]] || fail 'invalid env value'
    export "$key=$value"
  done < "$file"
  export FLOWFORGE_HOME=${FLOWFORGE_HOME:?required}
  [[ $FLOWFORGE_HOME =~ ^/[a-zA-Z0-9_/-]+$ && $FLOWFORGE_HOME != / && $FLOWFORGE_HOME != *..* ]] || fail 'invalid install path'
  [[ -d $FLOWFORGE_HOME && ! -L $FLOWFORGE_HOME ]] || fail 'invalid install directory'
  [[ $(realpath "$FLOWFORGE_HOME") == "$FLOWFORGE_HOME" ]] || fail 'install path must be canonical'
  export FLOWFORGE_PROJECT=${FLOWFORGE_PROJECT:-flowforge-cloud}
  [[ $FLOWFORGE_PROJECT =~ ^flowforge-[a-z0-9-]+$ ]] || fail 'invalid project'
  export FLOWFORGE_MODE=${FLOWFORGE_MODE:-private}
  [[ $FLOWFORGE_MODE == private || $FLOWFORGE_MODE == public ]] || fail 'invalid mode'
  [[ ${FLOWFORGE_POSTGRES_PASSWORD:-} =~ ^[a-f0-9]{64}$ && ${FLOWFORGE_REDIS_PASSWORD:-} =~ ^[a-f0-9]{64}$ ]] || fail 'use bootstrap-generated 256-bit passwords'
  export FLOWFORGE_WORKER_COUNT=${FLOWFORGE_WORKER_COUNT:-2}
  [[ $FLOWFORGE_WORKER_COUNT =~ ^[1-4]$ ]] || fail 'worker count must be 1..4'
  export FLOWFORGE_WORKER_CONCURRENCY=${FLOWFORGE_WORKER_CONCURRENCY:-1}
  [[ $FLOWFORGE_WORKER_CONCURRENCY == 1 ]] || fail 'release defaults require concurrency 1'
  export FLOWFORGE_PRIVATE_PORT=${FLOWFORGE_PRIVATE_PORT:-8180}
  [[ $FLOWFORGE_PRIVATE_PORT =~ ^[0-9]{4,5}$ && $FLOWFORGE_PRIVATE_PORT -ge 1024 && $FLOWFORGE_PRIVATE_PORT -le 65535 ]] || fail 'invalid private port'
  if [[ $FLOWFORGE_MODE == private ]]; then
    [[ ${FLOWFORGE_WS_ORIGINS:-} == "http://127.0.0.1:$FLOWFORGE_PRIVATE_PORT" ]] || fail 'private WS origin must match tunnel port'
  else
    [[ ${FLOWFORGE_PUBLIC_HOST:-} =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ && $FLOWFORGE_PUBLIC_HOST == *.* ]] || fail 'public DNS hostname required'
    [[ ${FLOWFORGE_BASIC_USER:-} =~ ^[a-zA-Z0-9_-]{1,32}$ && ${FLOWFORGE_BASIC_HASH:-} =~ ^\$2[aby]\$[0-9]{2}\$[./a-zA-Z0-9]{53}$ ]] || fail 'public username/bcrypt hash required'
    [[ ${FLOWFORGE_WS_ORIGINS:-} == "https://$FLOWFORGE_PUBLIC_HOST" ]] || fail 'public origin must match HTTPS hostname'
  fi
  export FLOWFORGE_OBSERVABILITY=${FLOWFORGE_OBSERVABILITY:-false}
  [[ $FLOWFORGE_OBSERVABILITY == true || $FLOWFORGE_OBSERVABILITY == false ]] || fail 'invalid observability flag'
  export FLOWFORGE_OTEL_ENABLED=${FLOWFORGE_OTEL_ENABLED:-false}
  [[ $FLOWFORGE_OTEL_ENABLED == false || ( $FLOWFORGE_OTEL_ENABLED == true && $FLOWFORGE_OBSERVABILITY == true ) ]] || fail 'tracing requires internal collector'
}
validate_release() {
  [[ ${FLOWFORGE_RELEASE_SHA:-} =~ ^[a-f0-9]{40}$ ]] || fail 'full release SHA required'
  local component value
  for component in BACKEND GATEWAY; do
    local key=FLOWFORGE_${component}_IMAGE
    value=${!key:-}
    [[ $value =~ ^ghcr.io/daniel-cpz/flowforge-(backend|gateway)@sha256:[a-f0-9]{64}$ ]] || fail 'exact GHCR digest required'
    [[ $value == "ghcr.io/daniel-cpz/flowforge-${component,,}@"* ]] || fail 'image component mismatch'
  done
}
dc() {
  local args=(compose --project-name "$FLOWFORGE_PROJECT" --env-file "$FLOWFORGE_ENV_FILE" -f "$BUNDLE/compose.prod.yml" -f "$BUNDLE/compose.$FLOWFORGE_MODE.yml")
  [[ $FLOWFORGE_OBSERVABILITY == false ]] || args+=(--profile observability)
  docker "${args[@]}" "$@"
}
acquire_lock() {
  mkdir -p "$FLOWFORGE_HOME/backups" "$FLOWFORGE_HOME/releases"
  [[ ! -L $FLOWFORGE_HOME/deploy.lock ]] || fail 'symlink deploy lock refused'
  exec 9>"$FLOWFORGE_HOME/deploy.lock"
  flock -n 9 || fail 'another deploy/backup is running'
}
schema_version() { dc exec -T postgres psql -U flowforge -d flowforge -Atc 'SELECT max(version) FROM schema_migrations'; }
backup_database() {
  local directory="$FLOWFORGE_HOME/backups" sha=${FLOWFORGE_RELEASE_SHA:-bootstrap} target
  [[ ! -L $directory ]] || fail 'backup directory is a symlink'
  target="$directory/$(date -u +%Y%m%dT%H%M%SZ)-$sha-$$.dump"
  if ! dc exec -T postgres pg_dump -U flowforge -d flowforge -Fc > "$target.partial"; then
    rm -f -- "$target.partial"; return 1
  fi
  [[ -s $target.partial ]] || { rm -f -- "$target.partial"; return 1; }
  # Validate archive before publishing it; never print its contents.
  if ! dc exec -T postgres pg_restore --list < "$target.partial" >/dev/null; then
    rm -f -- "$target.partial"; return 1
  fi
  chmod 600 "$target.partial"; mv -- "$target.partial" "$target"
  # Only complete dumps with our naming convention; preserve the newest seven.
  mapfile -t old < <(find "$directory" -maxdepth 1 -type f -name '????????T??????Z-*.dump' -printf '%f\n' | sort -r | tail -n +8)
  local name
  for name in "${old[@]}"; do rm -f -- "$directory/$name"; done
  printf '%s\n' "Backup archive verified (mode 0600)"
}
wait_health() {
  local service=$1 expected=$2 elapsed=0 ids id count ok
  while (( elapsed < 90 )); do
    ids=$(dc ps -q "$service") || return 1
    count=0; ok=true
    for id in $ids; do
      ((count+=1))
      [[ $(docker inspect --format '{{.State.Health.Status}}' "$id") == healthy ]] || ok=false
    done
    [[ $ok == false || $count -ne $expected ]] || return 0
    sleep 2; ((elapsed+=2))
  done
  return 1
}

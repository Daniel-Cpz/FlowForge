#!/usr/bin/env bash
# Restores only into a generated disposable DB on the running PostgreSQL.
set -Eeuo pipefail
source "$(dirname -- "$0")/common.sh"
export FLOWFORGE_ENV_FILE=${1:?server env required}
load_env "$FLOWFORGE_ENV_FILE"
export FLOWFORGE_BACKEND_IMAGE=${2:?backend image required}
export FLOWFORGE_GATEWAY_IMAGE=${3:?gateway image required}
acquire_lock
[[ -z $(dc ps -q api worker) ]] || fail 'restore identity test requires stopped application writers (maintenance window)'
db="flowforge_restore_$(date -u +%Y%m%d%H%M%S)_$$"
archive="$FLOWFORGE_HOME/backups/$db.dump"
[[ $db =~ ^flowforge_restore_[0-9]{14}_[0-9]+$ ]] || fail 'invalid restore DB'
created=false
cleanup_restore() {
  local rc=$?; trap - EXIT
  if [[ $created == true ]]; then dc exec -T postgres dropdb -U flowforge "$db"; fi
  rm -f -- "$archive" "$archive.before" "$archive.after"
  exit "$rc"
}
trap cleanup_restore EXIT
query="SELECT json_build_object('schema',(SELECT max(version) FROM schema_migrations),'jobs',(SELECT json_agg(id ORDER BY id) FROM jobs),'attempts',(SELECT json_agg(id ORDER BY id) FROM job_attempts),'schedules',(SELECT json_agg(id ORDER BY id) FROM job_schedules))"
dc exec -T postgres psql -U flowforge -d flowforge -Atc "$query" > "$archive.before"
dc exec -T postgres pg_dump -U flowforge -d flowforge -Fc > "$archive"
dc exec -T postgres createdb -U flowforge "$db"; created=true
dc exec -T postgres pg_restore -U flowforge -d "$db" --exit-on-error --no-owner < "$archive"
dc exec -T postgres psql -U flowforge -d "$db" -Atc "$query" > "$archive.after"
cmp "$archive.before" "$archive.after"
[[ $(dc exec -T postgres psql -U flowforge -d "$db" -Atc 'SELECT max(version) FROM schema_migrations') == 8 ]] || fail 'restore schema mismatch'
printf '%s\n' 'Disposable restore schema/Job/Attempt/Schedule identity comparison PASS'

#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "$0")/common.sh"
export FLOWFORGE_ENV_FILE=${1:?server env file required}
load_env "$FLOWFORGE_ENV_FILE"
export FLOWFORGE_RELEASE_SHA=${2:?deployed SHA required}
[[ $FLOWFORGE_RELEASE_SHA =~ ^[a-f0-9]{40}$ ]] || fail 'invalid SHA'
# Compose needs app image variables even for exec. Load real current release refs.
[[ -f $FLOWFORGE_HOME/current.env && ! -L $FLOWFORGE_HOME/current.env ]] || fail 'no current release'
while IFS='=' read -r key value; do
  case $key in FLOWFORGE_BACKEND_IMAGE|FLOWFORGE_GATEWAY_IMAGE) export "$key=$value";; esac
done < "$FLOWFORGE_HOME/current.env"
validate_release
acquire_lock
backup_database

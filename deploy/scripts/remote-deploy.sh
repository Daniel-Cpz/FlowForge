#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
release=${1:?release artifact directory required}
[[ ${DEPLOY_HOST:-} =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ && ${DEPLOY_USER:-} =~ ^[a-z_][a-z0-9_-]*$ ]] || { echo 'Missing/invalid deployment target' >&2; exit 1; }
[[ -n ${SSH_KEY:-} && -n ${KNOWN_HOSTS:-} && -n ${GH_TOKEN:-} && ${REGISTRY_USER:-} =~ ^[a-zA-Z0-9-]+$ ]] || { echo 'Required SSH/registry secrets missing' >&2; exit 1; }
python3 "$(dirname "$0")/../tests/validate-artifact.py" "$release/images.env" "$GITHUB_SHA"
temp=$(mktemp -d)
incoming=''
cleanup_runner() {
  local rc=$?
  trap - EXIT
  # Only the validated SHA/run-ID incoming directory; never a release/data dir.
  if [[ -n $incoming ]]; then
    ssh "${ssh_args[@]}" "$target" "rm -rf -- '$incoming'" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$temp"
  exit "$rc"
}
trap cleanup_runner EXIT
printf '%s\n' "$SSH_KEY" > "$temp/key"; chmod 600 "$temp/key"
printf '%s\n' "$KNOWN_HOSTS" > "$temp/known_hosts"
unset SSH_KEY KNOWN_HOSTS
ssh_args=(-i "$temp/key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$temp/known_hosts" -o ConnectTimeout=10)
target="$DEPLOY_USER@$DEPLOY_HOST"
sha=$GITHUB_SHA
# Ensure pinned key/remote preflight succeeds before any upload or Docker login.
ssh "${ssh_args[@]}" "$target" 'test -d /opt/flowforge && test -f /opt/flowforge/.env.prod && command -v docker >/dev/null && command -v flock >/dev/null'
[[ ${GITHUB_RUN_ID:-} =~ ^[0-9]+$ ]] || exit 1
incoming="/opt/flowforge/releases/incoming-$sha-$GITHUB_RUN_ID"
ssh "${ssh_args[@]}" "$target" "umask 077; mkdir '$incoming'"
scp "${ssh_args[@]}" "$release/bundle.tar.gz" "$release/images.env" "$release/bundle.sha256" "$target:$incoming/"
backend=$(sed -n 's/^FLOWFORGE_BACKEND_IMAGE=//p' "$release/images.env")
gateway=$(sed -n 's/^FLOWFORGE_GATEWAY_IMAGE=//p' "$release/images.env")
# Validated artifact contains no shell metacharacters; token travels only on stdin.
# Per-run DOCKER_CONFIG is removed on every remote exit; no persistent registry login.
remote="set -eu; umask 077;
export DOCKER_CONFIG=\$(mktemp -d);
trap 'docker logout ghcr.io >/dev/null 2>&1 || true; rm -rf -- \"\$DOCKER_CONFIG\" \"$incoming\"' EXIT;
docker login ghcr.io -u '$REGISTRY_USER' --password-stdin >/dev/null;
cd '$incoming'; sha256sum --check bundle.sha256 >/dev/null;
if test -d '/opt/flowforge/releases/$sha'; then
  cmp bundle.sha256 '/opt/flowforge/releases/$sha/bundle.sha256';
else
  mkdir '/opt/flowforge/releases/$sha';
  tar -xzf bundle.tar.gz --strip-components=1 -C '/opt/flowforge/releases/$sha';
  cp bundle.sha256 '/opt/flowforge/releases/$sha/bundle.sha256';
fi;
bash '/opt/flowforge/releases/$sha/scripts/deploy.sh' '/opt/flowforge/.env.prod' '$sha' '$backend' '$gateway'"
printf '%s' "$GH_TOKEN" | ssh "${ssh_args[@]}" "$target" "$remote"

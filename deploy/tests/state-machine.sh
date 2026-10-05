#!/usr/bin/env bash
# All external commands resolve to inert fixtures; never contacts a real host.
set -Eeuo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
fixture=$(mktemp -d /tmp/flowforge-deploy-test-XXXXXXXX)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/bin" "$fixture/home/secrets/postgres" "$fixture/home/releases"
sha=$(printf a%.0s {1..40}); digest=$(printf b%.0s {1..64})
release="$fixture/home/releases/$sha"
cp -R "$root" "$release"
printf 'fixture\n' > "$fixture/home/secrets/postgres/server.key"
printf 'fixture\n' > "$fixture/home/secrets/postgres/server.crt"
chmod 600 "$fixture/home/secrets/postgres/server.key"
cat > "$fixture/env" <<EOF
FLOWFORGE_HOME=$fixture/home
FLOWFORGE_PROJECT=flowforge-test
FLOWFORGE_MODE=private
FLOWFORGE_WS_ORIGINS=http://127.0.0.1:8180
FLOWFORGE_POSTGRES_PASSWORD=$(printf c%.0s {1..64})
FLOWFORGE_REDIS_PASSWORD=$(printf d%.0s {1..64})
EOF
chmod 600 "$fixture/env"
cat > "$fixture/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
echo "$*" >> "$FIXTURE_LOG"
[[ $FIXTURE_CASE != preflight || $* != info ]] || exit 1
[[ $FIXTURE_CASE != pull || $* != *' pull' ]] || exit 1
[[ $FIXTURE_CASE != backup || $* != *pg_dump* ]] || exit 1
[[ $FIXTURE_CASE != migration || $* != *'--no-deps migrate'* ]] || exit 1
[[ $FIXTURE_CASE != gateway-config || $* != *'caddy validate'* ]] || exit 1
if [[ $* == *'image inspect'* ]]; then echo "$FIXTURE_SHA"
elif [[ $* == *'inspect --format'* ]]; then
  if [[ ( $FIXTURE_CASE == readiness && $* == *api-id ) || ( $FIXTURE_CASE == gateway && $* == *gateway-id ) ]]; then echo unhealthy; else echo healthy; fi
elif [[ $* == *'ps -q worker' ]]; then printf 'worker-1\nworker-2\n'
elif [[ $* == *'ps -q postgres' ]]; then echo postgres-id
elif [[ $* == *'ps -q redis' ]]; then echo redis-id
elif [[ $* == *'ps -q api' ]]; then echo api-id
elif [[ $* == *'ps -q gateway' ]]; then echo gateway-id
elif [[ $* == *'pg_dump'* ]]; then echo fixture-archive
elif [[ $* == *'SELECT max(version)'* ]]; then echo 8
fi
EOF
cat > "$fixture/bin/curl" <<'EOF'
#!/usr/bin/env bash
[[ $FIXTURE_CASE != gateway-route ]] || exit 1
echo '<div id="root">'
EOF
printf '#!/usr/bin/env bash\nexit 0\n' > "$fixture/bin/sleep"
chmod +x "$fixture/bin/"*
export PATH="$fixture/bin:$PATH" FIXTURE_LOG="$fixture/log" FIXTURE_SHA="$sha"
run() { bash "$release/scripts/deploy.sh" "$fixture/env" "$sha" "ghcr.io/daniel-cpz/flowforge-backend@sha256:$digest" "ghcr.io/daniel-cpz/flowforge-gateway@sha256:$digest"; }
for case in preflight pull gateway-config backup migration readiness gateway gateway-route; do
  export FIXTURE_CASE=$case
  printf 'previous-release-preserved\n' > "$fixture/home/current.env"
  : > "$FIXTURE_LOG"
  if run > "$fixture/output" 2>&1; then echo "unexpected success: $case"; exit 1; fi
  grep -qx previous-release-preserved "$fixture/home/current.env"
  [[ ! -e $fixture/home/current.env.partial ]]
  ! grep -q 'migrate down\| down\|--volumes' "$FIXTURE_LOG"
  # flock released after both early and post-drain failures.
  flock -n "$fixture/home/deploy.lock" true
  if [[ $case == backup ]]; then ! grep -q -- '--no-deps migrate' "$FIXTURE_LOG"; fi
  printf '%s\n' "PASS abort/$case + metadata/lock/data preservation"
done
export FIXTURE_CASE=success
run > "$fixture/output" 2>&1
grep -qx "FLOWFORGE_RELEASE_SHA=$sha" "$fixture/home/current.env"
[[ $(stat -c %a "$fixture/home/current.env") == 600 ]]
flock -n "$fixture/home/deploy.lock" true
printf '%s\n' 'PASS successful atomic record'
# Contention gate before Docker and malformed credential gate.
: > "$FIXTURE_LOG"
if flock "$fixture/home/deploy.lock" bash -c 'bash "$1" "$2" "$3" "$4" "$5"' _ "$release/scripts/deploy.sh" "$fixture/env" "$sha" "ghcr.io/daniel-cpz/flowforge-backend@sha256:$digest" "ghcr.io/daniel-cpz/flowforge-gateway@sha256:$digest" > "$fixture/output" 2>&1; then exit 1; fi
[[ ! -s $FIXTURE_LOG ]]
chmod 644 "$fixture/env"
if run > "$fixture/output" 2>&1; then exit 1; fi
[[ ! -s $FIXTURE_LOG ]]
printf '%s\n' 'PASS concurrency lock + wrong secret permissions fail before Docker'

#!/usr/bin/env bash
# Run once as the authorized deployment user on the selected Ubuntu host.
set -Eeuo pipefail
umask 077
home=${1:-/opt/flowforge}
[[ $home =~ ^/[a-zA-Z0-9_/-]+$ && $home != / && $home != *..* ]] || exit 1
mkdir -p "$home/secrets/postgres" "$home/backups" "$home/releases"
[[ $(realpath "$home") == "$home" && ! -e $home/.env.prod && ! -e $home/secrets/postgres/server.key ]] || { echo 'Refusing to overwrite an existing installation' >&2; exit 1; }
openssl req -x509 -newkey rsa:3072 -nodes -days 365 -subj /CN=postgres -addext subjectAltName=DNS:postgres -keyout "$home/secrets/postgres/server.key" -out "$home/secrets/postgres/server.crt" 2>/dev/null
chmod 600 "$home/secrets/postgres/server.key"
chmod 755 "$home/secrets/postgres"
# postgres:18-alpine UID/GID 70. Only the newly created TLS directory is mounted.
docker run --rm --user 0 --entrypoint sh -v "$home/secrets/postgres:/tls" postgres:18-alpine -c 'chown 70:70 /tls/server.key /tls/server.crt; chmod 600 /tls/server.key; chmod 644 /tls/server.crt'
cat > "$home/.env.prod" <<EOF
FLOWFORGE_HOME=$home
FLOWFORGE_PROJECT=flowforge-cloud
FLOWFORGE_MODE=private
FLOWFORGE_PRIVATE_PORT=8180
FLOWFORGE_WS_ORIGINS=http://127.0.0.1:8180
FLOWFORGE_POSTGRES_PASSWORD=$(openssl rand -hex 32)
FLOWFORGE_REDIS_PASSWORD=$(openssl rand -hex 32)
FLOWFORGE_WORKER_COUNT=2
FLOWFORGE_WORKER_CONCURRENCY=1
FLOWFORGE_OBSERVABILITY=false
FLOWFORGE_OTEL_ENABLED=false
EOF
chmod 600 "$home/.env.prod"
printf '%s\n' 'Bootstrap complete. Server-local env/TLS secrets created; no firewall or SSH settings changed.'

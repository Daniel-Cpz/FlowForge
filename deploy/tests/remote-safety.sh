#!/usr/bin/env bash
set -Eeuo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
fixture=$(mktemp -d /tmp/flowforge-ssh-test-XXXXXXXX)
trap 'rm -rf -- "$fixture"' EXIT
mkdir "$fixture/bin" "$fixture/release"
sha=$(printf a%.0s {1..40}); digest=$(printf b%.0s {1..64})
cat > "$fixture/release/images.env" <<EOF
FLOWFORGE_BACKEND_IMAGE=ghcr.io/daniel-cpz/flowforge-backend@sha256:$digest
FLOWFORGE_GATEWAY_IMAGE=ghcr.io/daniel-cpz/flowforge-gateway@sha256:$digest
FLOWFORGE_RELEASE_SHA=$sha
EOF
cat > "$fixture/bin/ssh" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" > "$SSH_TEST_LOG"
[[ $* == *StrictHostKeyChecking=yes* && $* == *BatchMode=yes* && $* == *UserKnownHostsFile=* ]]
exit 85 # simulate pinned-host mismatch, with no connection
EOF
cat > "$fixture/bin/scp" <<'EOF'
#!/usr/bin/env bash
touch "$SSH_TEST_SCP"
exit 99
EOF
chmod +x "$fixture/bin/"*
export PATH="$fixture/bin:$PATH" GITHUB_SHA="$sha" GITHUB_RUN_ID=1 REGISTRY_USER=fixture
export DEPLOY_HOST=example.invalid DEPLOY_USER=fixture SSH_TEST_LOG="$fixture/log" SSH_TEST_SCP="$fixture/scp"
if SSH_KEY='' KNOWN_HOSTS='' GH_TOKEN='' bash "$root/scripts/remote-deploy.sh" "$fixture/release" > "$fixture/output" 2>&1; then exit 1; fi
[[ ! -e $SSH_TEST_LOG && ! -e $SSH_TEST_SCP ]]
if SSH_KEY=fixture KNOWN_HOSTS=fixture GH_TOKEN=fixture bash "$root/scripts/remote-deploy.sh" "$fixture/release" > "$fixture/output" 2>&1; then exit 1; fi
[[ -e $SSH_TEST_LOG && ! -e $SSH_TEST_SCP ]]
! grep -q 'fixture.*key' "$fixture/output"
printf '%s\n' 'PASS missing secrets fail before SSH; pinned-host mismatch fails before upload'

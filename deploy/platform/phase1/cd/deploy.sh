#!/usr/bin/env bash
# Server rollout of main, the same steps as scripts/local-environment.mjs `up` with three differences:
# single-lite tier with the intelligence services and the Operations Agent, images loaded beforehand (nothing is built here), and
# the platform bound to loopback behind the host nginx. Idempotent like local:up.
#
# apply-on-server.sh and rollback.sh run this copy from the unpacked platform tree
# (/opt/hvac-platform/deploy/platform/phase1/cd/deploy.sh). It used to live only on the host as
# /opt/hvac-platform-tools/deploy.sh, which stays as the fallback when this file is absent.
#
#   deploy.sh [<release revision>]
#
# The revision (40 hex) names the web build; without one, the platform's REVISION file is used.
# Secrets stay in environments/server.runtime.env on the host; nothing here holds one.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# sudo resets the environment, so the platform root comes from where this script sits.
platform_dir="${HVAC_PLATFORM_DIR:-$(cd "$script_dir/../../../.." && pwd)}"
cd "$platform_dir"

revision="${1:-}"
if [[ -z "$revision" && -s "$platform_dir/REVISION" ]]; then
  revision="$(tr -d '[:space:]' < "$platform_dir/REVISION")"
fi
if [[ ! "$revision" =~ ^[0-9a-f]{12,40}$ ]]; then
  echo "error: no release revision (argument or $platform_dir/REVISION); got '${revision}'" >&2
  exit 1
fi

export PHASE1_RUNTIME_DIR="$platform_dir/deploy/platform/phase1/runtime/server"
export PHASE1_ENV_FILE="$platform_dir/deploy/platform/phase1/environments/server.runtime.env"
export PHASE1_DEPLOYMENT_TIER=single-lite
export HVAC_LOCAL_PROJECT=hvac-platform
# Same 12-character id build-release.sh bakes into the web dist.
export HVAC_WEB_BUILD_ID="${revision:0:12}"
export PLATFORM_PUBLIC_ORIGIN=https://hvac.swayingwindmill.online
R=$PHASE1_RUNTIME_DIR
L="node scripts/phase1-wsl-compose.mjs"
step() { echo "== $* $(date -Is)"; }
service_dir() { mkdir -p "$1"; docker run --rm --user 0 --entrypoint chown -v "$1:/data" postgres:16.4-bookworm 65532:65532 /data; }

step "release ${HVAC_WEB_BUILD_ID}"

step identities
node scripts/issue-workload-identities.mjs

step permissions
chmod 700 "$R"
find "$R" -mindepth 1 -type d -exec chmod 755 {} +
find "$R" -mindepth 1 -type f -exec chmod 644 {} +
mkdir -p "$R/backups/postgres/base" "$R/backups/clickhouse"
service_dir "$R/identity"
service_dir "$R/iam"

step migrate
$L --profile migration run --rm phase1-migrator

step keys
[ -f "$R/identity/signing-key.pem" ] || $L run --rm identity-keygen
[ -f "$R/identity/mfa-encryption.key" ] || $L run --rm identity-mfa-keygen
[ -f "$R/iam/api-credential.pepper" ] || $L run --rm iam-api-credential-keygen
if [ ! -f "$R/gateway-ca/connectivity.crt" ]; then
  service_dir "$R/gateway-ca"
  $L run --rm --no-deps -v "$R/gateway-ca:/run/hvac/provisioning:rw" -e GATEWAY_CA_DIR=/run/hvac/provisioning connectivity --initialize-gateway-ca
fi

step administrator
node scripts/local-environment.mjs administrator --no-build

step foundation
node scripts/phase1-bootstrap-local-foundation.mjs
$L run --rm -T identity-reconciler < "$R/identity-reconcile.json"

step up
$L --integration up -d
# Intelligence services, the Operations Agent included (DEEPSEEK_API_KEY in the runtime env, 2026-10-16).
$L --integration --intelligence up -d --no-deps forecast-service optimization-service fdd-service mv-service report-service operations-agent-service

step songyaxuan
# Simulators stay off on this server (user, 2026-10-14); the real Site is registered from its site file.
PHASE1_POSTGRES_CONTAINER=hvac-platform-postgres-1 node scripts/site-onboarding.mjs deploy/sites/songyaxuan.site.json

step "done"
$L --integration ps --format '{{.Service}} {{.Status}}'

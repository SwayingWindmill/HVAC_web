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
service_dir() { mkdir -p "$1"; docker run --rm --user 0 --entrypoint chown -v "$1:/data" postgres:16.4-bookworm -R "${2:-65532:65532}" /data; }

step "release ${HVAC_WEB_BUILD_ID}"

step identities
node scripts/issue-workload-identities.mjs

step permissions
chmod 700 "$R"
mkdir -p "$R/backups/postgres/base" "$R/backups/postgres/wal" "$R/backups/clickhouse" "$R/alarm-delivery" "$R/data/otel-collector" "$R/mqtt-crl"
chmod 750 "$R/data/otel-collector"
# Non-root containers run under UID 65532 (Go services), supplementary group 65532 (alertmanager/centrifugo/operations-agent), UID 1883 (mosquitto), UID 101 (clickhouse), UID 999 (postgres), or UID 10001 (otel-collector).
docker run --rm --user 0 --entrypoint sh -v "$R:/runtime" postgres:16.4-bookworm -c '
  for d in identity iam gateway-ca mqtt-crl; do [ -d "/runtime/$d" ] && chown -R 65532:65532 "/runtime/$d"; done
  for d in internal-pki alert-receiver alarm-delivery secrets; do [ -d "/runtime/$d" ] && chown -R 0:65532 "/runtime/$d"; done
  [ -d /runtime/tls ] && find /runtime/tls -type d -exec chown 0:65532 {} +
  [ -d /runtime/mqtt-pki ] && chown -R 1883:65532 /runtime/mqtt-pki
  chown -R 101:101 /runtime/backups/clickhouse
  chown -R 999:999 /runtime/backups/postgres
  chown -R 10001:10001 /runtime/data/otel-collector
'
# Postgres requires 0700 on PGDATA; keep data/ untouched and restrict secret/PKI dirs to 0750.
find "$R" -mindepth 1 -type d \
  ! \( -path "$R/data" -o -path "$R/data/*" \
    -o -path "$R/backups" -o -path "$R/backups/*" \
    -o -path "$R/identity" -o -path "$R/identity/*" \
    -o -path "$R/iam" -o -path "$R/iam/*" \
    -o -path "$R/gateway-ca" -o -path "$R/gateway-ca/*" \
    -o -path "$R/internal-pki" -o -path "$R/internal-pki/*" \
    -o -path "$R/db-role-credentials" -o -path "$R/db-role-credentials/*" \
    -o -path "$R/alert-receiver" -o -path "$R/alert-receiver/*" \
    -o -path "$R/alarm-delivery" -o -path "$R/alarm-delivery/*" \
    -o -path "$R/mqtt-pki" -o -path "$R/mqtt-pki/*" \
    -o -path "$R/tls" -o -path "$R/tls/*" \
    -o -path "$R/secrets" -o -path "$R/secrets/*" \) \
  -exec chmod 755 {} +
for sensitive_dir in identity iam gateway-ca internal-pki db-role-credentials alert-receiver alarm-delivery mqtt-pki tls secrets backups; do
  if [ -d "$R/$sensitive_dir" ]; then
    find "$R/$sensitive_dir" -type d -exec chmod 750 {} +
  fi
done
# Only public certificates stay world-readable (0644); CA and workload secrets stay locked down.
find "$R" -mindepth 1 -type f ! -path "$R/data/*" ! -path "$R/backups/*" \
  \( -name '*.crt' -o -name 'trusted-client-ca.pem' -o -name '*.crl.pem' -o -name '*.pub' \) \
  -exec chmod 644 {} +
find "$R" -mindepth 1 -type f ! -path "$R/data/*" ! -path "$R/backups/*" \
  \( -name 'tls.key' -o -name 'signing-key.pem' -o -name 'mfa-encryption.key' \
    -o -name '*-key.pem' -o -name '*.key' -o -name '*secret*' -o -name '*token*' \
    -o -name '*password*' -o -name '*webhook*' -o -name '*credentials*' \
    -o -name 'recipients.json' -o -name '*.dsn' -o -name '*.env' \) \
  -exec chmod 640 {} +
find "$R" -mindepth 1 -type f ! -path "$R/data/*" \
  \( -name 'ca.key' -o -name 'ca.srl' -o -name 'roles.sql' -o -name 'local-admin.credentials' -o -name 'public.key' \) \
  -exec chmod 600 {} +

step roles
node scripts/local-environment.mjs roles

step migrate
$L --profile migration run --rm phase1-migrator
clickhouse_mode="${PHASE1_CLICKHOUSE_MODE:-}"
if [[ -z "$clickhouse_mode" && -r "$PHASE1_ENV_FILE" ]]; then
  clickhouse_mode="$(awk -F= '/^PHASE1_CLICKHOUSE_MODE=/{v=$2; gsub(/["'\''\r[:space:]]/, "", v); val=v} END{print val}' "$PHASE1_ENV_FILE")"
fi
clickhouse_mode="${clickhouse_mode:-local}"
case "$clickhouse_mode" in
  local)
    $L --profile clickhouse-migration run --rm phase1-clickhouse-migrator
    ;;
  external)
    echo "skipping local ClickHouse migrator (PHASE1_CLICKHOUSE_MODE=external)"
    ;;
  *)
    echo "error: unknown PHASE1_CLICKHOUSE_MODE: '${clickhouse_mode}'" >&2
    exit 1
    ;;
esac

step keys
[ -f "$R/identity/signing-key.pem" ] || $L run --rm identity-keygen
[ -f "$R/identity/mfa-encryption.key" ] || $L run --rm identity-mfa-keygen
if [ ! -f "$R/gateway-ca/connectivity.crt" ]; then
  service_dir "$R/gateway-ca"
  chmod 750 "$R/gateway-ca"
  $L run --rm --no-deps -v "$R/gateway-ca:/run/hvac/provisioning:rw" -e GATEWAY_CA_DIR=/run/hvac/provisioning connectivity --initialize-gateway-ca
fi

step administrator
node scripts/local-environment.mjs administrator --no-build

step foundation
node scripts/phase1-bootstrap-local-foundation.mjs
$L run --rm -T identity-reconciler < "$R/identity-reconcile.json"

step up
$L --integration up -d
# Single-file bind mounts retain the previous host inode after `tar -xf` replaces a file;
# recreate mqtt-broker only when a mounted file in the container differs from the host tree.
if [ -f infra/telemetry/mqtt/mosquitto.conf ] && [ -f infra/telemetry/mqtt/acl ]; then
  if ! docker exec "${HVAC_LOCAL_PROJECT}-mqtt-broker-1" cat /mosquitto/config/mosquitto.conf 2>/dev/null | cmp -s - infra/telemetry/mqtt/mosquitto.conf \
    || ! docker exec "${HVAC_LOCAL_PROJECT}-mqtt-broker-1" cat /mosquitto/config/acl 2>/dev/null | cmp -s - infra/telemetry/mqtt/acl \
    || { [ -f "$R/gateway-ca/trusted-client-ca.pem" ] && ! docker exec "${HVAC_LOCAL_PROJECT}-mqtt-broker-1" cat /mosquitto/config/gateway-ca.pem 2>/dev/null | cmp -s - "$R/gateway-ca/trusted-client-ca.pem"; }; then
    $L --integration up -d --force-recreate --no-deps mqtt-broker
  fi
fi
# Intelligence services, the Operations Agent included (DEEPSEEK_API_KEY in the runtime env, 2026-10-16).
$L --integration --intelligence up -d --no-deps forecast-service optimization-service fdd-service mv-service report-service operations-agent-service

step songyaxuan
# Simulators stay off on this server (user, 2026-10-14); the real Site is registered from its site file.
PHASE1_POSTGRES_CONTAINER=hvac-platform-postgres-1 node scripts/site-onboarding.mjs apply deploy/sites/songyaxuan.site.json

step "done"
$L --integration ps --format '{{.Service}} {{.Status}}'

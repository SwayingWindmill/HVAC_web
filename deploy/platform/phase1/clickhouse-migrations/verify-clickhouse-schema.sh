#!/usr/bin/env bash
set -euo pipefail

CLICKHOUSE_HOST="${CLICKHOUSE_HOST:-clickhouse}"
CLICKHOUSE_PORT="${CLICKHOUSE_PORT:-9000}"
CLICKHOUSE_USER="${CLICKHOUSE_USER:-phase1_schema_reader}"
CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-}"
PHASE1_CLICKHOUSE_SECURE="${PHASE1_CLICKHOUSE_SECURE:-false}"
PHASE1_CLICKHOUSE_MANIFEST_DIR="${PHASE1_CLICKHOUSE_MANIFEST_DIR:-/opt/hvac/clickhouse-migrations}"
PHASE1_PRODUCT_VERSION="${PHASE1_PRODUCT_VERSION:-0.1.0}"

manifest="${PHASE1_CLICKHOUSE_MANIFEST_DIR}/clickhouse-migration-list.tsv"
product="hvac-web"

[[ -r "${manifest}" ]] || { echo "ClickHouse migration manifest is missing: ${manifest}" >&2; exit 1; }

ch_client() {
  local args=(--host "$CLICKHOUSE_HOST" --port "$CLICKHOUSE_PORT" --user "$CLICKHOUSE_USER")
  if [[ -n "$CLICKHOUSE_PASSWORD" ]]; then
    args+=(--password "$CLICKHOUSE_PASSWORD")
  fi
  if [[ "$PHASE1_CLICKHOUSE_SECURE" == "true" ]]; then
    args+=(--secure)
  fi
  clickhouse-client "${args[@]}" "$@"
}

for attempt in $(seq 1 15); do
  if ch_client --query "SELECT 1" </dev/null >/dev/null 2>&1; then
    break
  fi
  if [[ "${attempt}" == "15" ]]; then
    echo "ClickHouse preflight endpoint is unreachable" >&2
    exit 1
  fi
  sleep 1
done

manifest_sha256="$(sha256sum "${manifest}" | awk '{print $1}')"

state="$(ch_client \
  --query="SELECT concat(product_version, '|', schema_manifest_sha256) FROM phase1_deployment.product_schema FINAL WHERE product = 'hvac-web'" \
  </dev/null | tr -d '[:space:]')"

if [[ -z "${state}" ]]; then
  echo "ClickHouse schema compatibility state missing" >&2
  exit 1
fi

actual_version="${state%%|*}"
actual_sha256="${state#*|}"

if [[ "${actual_version}" != "${PHASE1_PRODUCT_VERSION}" ]]; then
  echo "ClickHouse product version mismatch: expected=${PHASE1_PRODUCT_VERSION} actual=${actual_version}" >&2
  exit 1
fi

if [[ "${actual_sha256}" != "${manifest_sha256}" ]]; then
  echo "ClickHouse schema manifest mismatch: expected=${manifest_sha256} actual=${actual_sha256}" >&2
  exit 1
fi

echo "Phase 1 ClickHouse schema compatibility verified for ${product} ${PHASE1_PRODUCT_VERSION}; schema=${manifest_sha256}."

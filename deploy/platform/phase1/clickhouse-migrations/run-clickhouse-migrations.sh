#!/usr/bin/env bash
set -euo pipefail

CLICKHOUSE_HOST="${CLICKHOUSE_HOST:-127.0.0.1}"
CLICKHOUSE_PORT="${CLICKHOUSE_PORT:-9000}"
CLICKHOUSE_USER="${CLICKHOUSE_USER:-default}"
CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-}"
PHASE1_CLICKHOUSE_SECURE="${PHASE1_CLICKHOUSE_SECURE:-false}"
PHASE1_CLICKHOUSE_MIGRATION_ROOT="${PHASE1_CLICKHOUSE_MIGRATION_ROOT:-/opt/hvac/repo}"
PHASE1_CLICKHOUSE_MANIFEST_DIR="${PHASE1_CLICKHOUSE_MANIFEST_DIR:-/opt/hvac/clickhouse-migrations}"
PHASE1_PRODUCT_VERSION="${PHASE1_PRODUCT_VERSION:-0.1.0}"
PHASE1_RELEASE_REVISION="${PHASE1_RELEASE_REVISION:-unversioned-local-run}"
PHASE1_CLICKHOUSE_ROLE_CREDENTIALS_FILE="${PHASE1_CLICKHOUSE_ROLE_CREDENTIALS_FILE:-}"

REPO_ROOT="${PHASE1_CLICKHOUSE_MIGRATION_ROOT}"
MANIFEST_DIR="${PHASE1_CLICKHOUSE_MANIFEST_DIR}"
manifest="${MANIFEST_DIR}/clickhouse-migration-list.tsv"
equivalent_manifest="${MANIFEST_DIR}/clickhouse-migration-equivalent-sha256.tsv"
product="hvac-web"

[[ -r "${manifest}" ]] || { echo "ClickHouse migration manifest is missing: ${manifest}" >&2; exit 1; }

is_equivalent_sha() {
  local target_path="$1"
  local candidate_sha="$2"
  [[ -r "${equivalent_manifest}" ]] || return 1
  local eq_path eq_sha
  while IFS='|' read -r eq_path eq_sha || [[ -n "${eq_path:-}" ]]; do
    [[ -n "${eq_path}" && -n "${eq_sha:-}" ]] || continue
    if [[ "${eq_path}" == "${target_path}" && "${eq_sha}" == "${candidate_sha}" ]]; then
      return 0
    fi
  done < "${equivalent_manifest}"
  return 1
}

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

for attempt in $(seq 1 60); do
  if ch_client --query "SELECT 1" </dev/null >/dev/null 2>&1; then
    break
  fi
  if [[ "${attempt}" == "60" ]]; then
    echo "ClickHouse did not become ready" >&2
    exit 1
  fi
  sleep 1
done

manifest_sha256="$(sha256sum "${manifest}" | awk '{print $1}')"

ch_client --multiquery <<'SQL'
CREATE DATABASE IF NOT EXISTS phase1_deployment;
CREATE TABLE IF NOT EXISTS phase1_deployment.schema_migrations (
  migration_path String,
  sha256 FixedString(64),
  status LowCardinality(String),
  release_revision String,
  applied_at Nullable(DateTime64(3, 'UTC')),
  version UInt64,
  updated_at DateTime64(6, 'UTC')
) ENGINE = ReplacingMergeTree(version)
ORDER BY (migration_path);
CREATE TABLE IF NOT EXISTS phase1_deployment.product_schema (
  product String,
  product_version String,
  schema_manifest_sha256 FixedString(64),
  release_revision String,
  version UInt64,
  updated_at DateTime64(6, 'UTC')
) ENGINE = ReplacingMergeTree(version)
ORDER BY (product);
SQL

exec 3< "${manifest}"
while IFS='|' read -r target migration_path <&3; do
  [[ -n "${target}" && -n "${migration_path}" ]] || continue
  if [[ "${target}" != "clickhouse" ]]; then
    echo "unexpected ClickHouse migration target: ${target}" >&2
    exit 1
  fi
  case "${migration_path}" in
    *fixture*|*testdata*|*bootstrap*|*legacy-migration*)
      echo "forbidden ClickHouse migration source in production allowlist: ${migration_path}" >&2
      exit 1
      ;;
  esac

  source="${REPO_ROOT}/${migration_path}"
  [[ -r "${source}" ]] || { echo "ClickHouse migration source is missing: ${migration_path}" >&2; exit 1; }

  if grep -Eiq 'local-only|local-fixture-only|local-mutation-fixture-only' "${source}"; then
    echo "ClickHouse migration contains a local/test credential marker: ${migration_path}" >&2
    exit 1
  fi
  file_sha="$(sha256sum "${source}" | awk '{print $1}')"

  existing="$(ch_client \
    --param_p="${migration_path}" \
    --query="SELECT concat(status, '|', sha256, '|', toString(version)) FROM phase1_deployment.schema_migrations FINAL WHERE migration_path = {p:String}" \
    </dev/null | tr -d '[:space:]')"

  current_ver=0
  if [[ -n "${existing}" ]]; then
    IFS='|' read -r status recorded_sha current_ver <<< "${existing}"
    if [[ "${status}" != "APPLIED" ]]; then
      echo "incomplete ClickHouse migration ${migration_path} (${status}); operator recovery is required before retry" >&2
      exit 1
    fi
    if [[ "${recorded_sha}" != "${file_sha}" ]]; then
      if is_equivalent_sha "${migration_path}" "${recorded_sha}"; then
        echo "ClickHouse migration already applied (equivalent checksum): ${migration_path} recorded=${recorded_sha} current=${file_sha}"
        continue
      fi
      echo "ClickHouse migration drift detected for ${migration_path}: recorded=${recorded_sha} current=${file_sha}" >&2
      exit 1
    fi
    echo "ClickHouse migration already applied: ${migration_path} ${file_sha}"
    continue
  fi

  next_ver=$((current_ver + 1))
  ch_client \
    --param_p="${migration_path}" \
    --param_sha="${file_sha}" \
    --param_rev="${PHASE1_RELEASE_REVISION}" \
    --param_ver="${next_ver}" \
    --query="INSERT INTO phase1_deployment.schema_migrations (migration_path, sha256, status, release_revision, applied_at, version, updated_at) SELECT {p:String}, {sha:String}, 'APPLYING', {rev:String}, CAST(NULL AS Nullable(DateTime64(3, 'UTC'))), {ver:UInt64}, now64(6)" </dev/null

  echo "applying ClickHouse migration: ${migration_path} ${file_sha}"
  if ch_client --multiquery < "${source}"; then
    ch_client \
      --param_p="${migration_path}" \
      --param_sha="${file_sha}" \
      --param_rev="${PHASE1_RELEASE_REVISION}" \
      --param_ver="$((next_ver + 1))" \
      --query="INSERT INTO phase1_deployment.schema_migrations (migration_path, sha256, status, release_revision, applied_at, version, updated_at) SELECT {p:String}, {sha:String}, 'APPLIED', {rev:String}, now64(3), {ver:UInt64}, now64(6)" </dev/null
  else
    ch_client \
      --param_p="${migration_path}" \
      --param_sha="${file_sha}" \
      --param_rev="${PHASE1_RELEASE_REVISION}" \
      --param_ver="$((next_ver + 1))" \
      --query="INSERT INTO phase1_deployment.schema_migrations (migration_path, sha256, status, release_revision, applied_at, version, updated_at) SELECT {p:String}, {sha:String}, 'FAILED', {rev:String}, CAST(NULL AS Nullable(DateTime64(3, 'UTC'))), {ver:UInt64}, now64(6)" </dev/null
    echo "ClickHouse migration failed: ${migration_path}" >&2
    exit 1
  fi
done
exec 3<&-

ch_client --multiquery <<'SQL'
CREATE USER IF NOT EXISTS phase1_schema_reader IDENTIFIED WITH no_password;
GRANT SELECT ON phase1_deployment.product_schema TO phase1_schema_reader;
SQL

if [[ -n "${PHASE1_CLICKHOUSE_ROLE_CREDENTIALS_FILE}" ]]; then
  [[ -f "${PHASE1_CLICKHOUSE_ROLE_CREDENTIALS_FILE}" && -r "${PHASE1_CLICKHOUSE_ROLE_CREDENTIALS_FILE}" ]] || {
    echo "ClickHouse runtime role credential SQL is missing or not readable: ${PHASE1_CLICKHOUSE_ROLE_CREDENTIALS_FILE}" >&2
    exit 1
  }
  if grep -Fq '[REDACTED_SECRET]' "${PHASE1_CLICKHOUSE_ROLE_CREDENTIALS_FILE}"; then
    echo "ClickHouse runtime role credential SQL still contains redacted placeholders" >&2
    exit 1
  fi
  ch_client --multiquery < "${PHASE1_CLICKHOUSE_ROLE_CREDENTIALS_FILE}"
fi

incomplete="$(ch_client --query="SELECT count() FROM phase1_deployment.schema_migrations FINAL WHERE status != 'APPLIED'" </dev/null | tr -d '[:space:]')"
if [[ "${incomplete}" != "0" ]]; then
  echo "ClickHouse contains incomplete migration state (${incomplete})" >&2
  exit 1
fi

current_prod_ver="$(ch_client \
  --param_prod="${product}" \
  --query="SELECT max(version) FROM phase1_deployment.product_schema FINAL WHERE product = {prod:String}" \
  </dev/null | tr -d '[:space:]')"
if [[ ! "${current_prod_ver}" =~ ^[0-9]+$ ]]; then
  current_prod_ver=0
fi
next_prod_ver=$((current_prod_ver + 1))

ch_client \
  --param_prod="${product}" \
  --param_pv="${PHASE1_PRODUCT_VERSION}" \
  --param_msha="${manifest_sha256}" \
  --param_rev="${PHASE1_RELEASE_REVISION}" \
  --param_ver="${next_prod_ver}" \
  --query="INSERT INTO phase1_deployment.product_schema (product, product_version, schema_manifest_sha256, release_revision, version, updated_at) SELECT {prod:String}, {pv:String}, {msha:String}, {rev:String}, {ver:UInt64}, now64(6)" </dev/null

echo "Phase 1 ClickHouse migrations completed for ${product} ${PHASE1_PRODUCT_VERSION}; schema=${manifest_sha256}."

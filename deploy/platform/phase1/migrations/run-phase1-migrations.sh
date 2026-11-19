#!/usr/bin/env bash
set -euo pipefail

root=/opt/hvac
repo="${root}/repo"
manifest="${root}/migrations/migration-list.tsv"
role_bootstrap="${root}/migrations/bootstrap-roles.sql"
database_bootstrap="${root}/migrations/create-databases.sql"
role_credentials="${PHASE1_DB_ROLE_CREDENTIALS_FILE:-/run/hvac/db-role-credentials/roles.sql}"
product="hvac-web"
product_version="${PHASE1_PRODUCT_VERSION:-0.1.0}"
release_revision="${PHASE1_RELEASE_REVISION:-unversioned-local-run}"
# Every database that has a migration, in manifest order.
mapfile -t databases < <(cut -d'|' -f1 "${manifest}" | awk 'NF && !seen[$0]++')

export PGHOST="${PGHOST:-postgres}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${PGUSER:-postgres}"

for attempt in $(seq 1 60); do
  if pg_isready -h "${PGHOST}" -p "${PGPORT}" -U "${PGUSER}" -d postgres >/dev/null 2>&1; then
    break
  fi
  if [[ "${attempt}" == "60" ]]; then
    echo "PostgreSQL did not become ready" >&2
    exit 1
  fi
  sleep 1
done

psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname=postgres --file="${database_bootstrap}"
psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname=postgres --file="${role_bootstrap}"

ensure_tracking_tables() {
  local database=$1
  psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" <<'SQL'
CREATE SCHEMA IF NOT EXISTS phase1_deployment;
REVOKE ALL ON SCHEMA phase1_deployment FROM PUBLIC;
CREATE TABLE IF NOT EXISTS phase1_deployment.schema_migrations (
  migration_path text PRIMARY KEY,
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  status text NOT NULL DEFAULT 'APPLIED',
  started_at timestamptz NOT NULL DEFAULT now(),
  applied_at timestamptz,
  release_revision text NOT NULL
);
ALTER TABLE phase1_deployment.schema_migrations
  ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'APPLIED';
ALTER TABLE phase1_deployment.schema_migrations
  ADD COLUMN IF NOT EXISTS started_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE phase1_deployment.schema_migrations
  ALTER COLUMN applied_at DROP NOT NULL;
CREATE TABLE IF NOT EXISTS phase1_deployment.product_schema (
  product text PRIMARY KEY,
  product_version text NOT NULL,
  schema_manifest_sha256 text NOT NULL CHECK (schema_manifest_sha256 ~ '^[0-9a-f]{64}$'),
  release_revision text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
REVOKE ALL ON phase1_deployment.schema_migrations, phase1_deployment.product_schema FROM PUBLIC;
DO $$
DECLARE
  role_name text;
  schema_reader_roles text[] := ARRAY[
    's0_migrator',
    'gateway_runtime',
    'gateway_relay_runtime',
    'audit_consumer_runtime',
    'audit_query_runtime',
    'identity_runtime',
    'identity_admin',
    'identity_directory_reader',
    's1_iam_migrator',
    's1_iam_runtime',
    's1_iam_admin',
    's1_iam_reconciler',
    's1_core_service',
    'metric_engine_runtime',
    'scheduler_runtime',
    'maintenance_runtime',
    'settlement_runtime',
    'forecast_runtime',
    'optimization_runtime',
    'fdd_runtime',
    'mv_runtime',
    'report_runtime',
    'connectivity_runtime',
    's2_iam_grant_runtime',
    's2_telemetry_migrator_service',
    's2_telemetry_service',
    's2_telemetry_gateway',
    's2_telemetry_iam',
    's2_telemetry_relay_service',
    's2_telemetry_history_service',
    's3_command_migrator_service',
    's3_command_service',
    's4_alarm_service',
    's5_work_order_service',
    's5_work_order_mutation_service',
    's16_notification_service',
    'operations_agent_operations_runtime',
    's2_telemetry_runtime',
    's2_telemetry_history',
    's3_command_runtime',
    's4_alarm_runtime',
    's4_alarm_notification_relay',
    's16_notification_scheduler'
  ];
BEGIN
  FOREACH role_name IN ARRAY schema_reader_roles LOOP
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = role_name) THEN
      EXECUTE format('GRANT USAGE ON SCHEMA phase1_deployment TO %I', role_name);
      EXECUTE format('GRANT SELECT ON phase1_deployment.product_schema TO %I', role_name);
    END IF;
  END LOOP;
END
$$;
SQL
}

for database in "${databases[@]}"; do
  ensure_tracking_tables "${database}"
done

while IFS='|' read -r database relative_path; do
  [[ -n "${database}" && -n "${relative_path}" ]] || continue
  case "${relative_path}" in
    *fixture*|*testdata*|*bootstrap*|*legacy-migration*)
      echo "forbidden migration source in production allowlist: ${relative_path}" >&2
      exit 1
      ;;
  esac

  source="${repo}/${relative_path}"
  [[ -r "${source}" ]] || { echo "migration source is missing: ${relative_path}" >&2; exit 1; }

  if grep -Eiq 'local-only|local-fixture-only|local-mutation-fixture-only' "${source}"; then
    echo "migration contains a local/test credential marker: ${relative_path}" >&2
    exit 1
  fi
  digest="$(sha256sum "${source}" | awk '{print $1}')"

  existing="$(psql --no-psqlrc --tuples-only --no-align --dbname="${database}" \
    --set=path="${relative_path}" <<'SQL' | tr -d '[:space:]'
SELECT sha256 || '|' || status
FROM phase1_deployment.schema_migrations
WHERE migration_path = :'path';
SQL
  )"

  if [[ -n "${existing}" ]]; then
    existing_digest="${existing%%|*}"
    existing_status="${existing#*|}"
    if [[ "${existing_digest}" != "${digest}" ]]; then
      echo "migration drift detected for ${relative_path}: recorded=${existing_digest} current=${digest}" >&2
      exit 1
    fi
    if [[ "${existing_status}" == "APPLIED" ]]; then
      echo "migration already applied: ${database} ${relative_path} ${digest}"
      continue
    fi
    echo "migration ${relative_path} is ${existing_status}; operator recovery is required before retry" >&2
    exit 1
  fi

  psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" \
    --set=path="${relative_path}" \
    --set=digest="${digest}" \
    --set=release_revision="${release_revision}" <<'SQL'
INSERT INTO phase1_deployment.schema_migrations
  (migration_path, sha256, status, started_at, applied_at, release_revision)
VALUES
  (:'path', :'digest', 'APPLYING', now(), NULL, :'release_revision');
SQL

  echo "applying migration: ${database} ${relative_path} ${digest}"
  if psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" --file="${source}"; then
    psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" --set=path="${relative_path}" <<'SQL'
UPDATE phase1_deployment.schema_migrations
SET status = 'APPLIED', applied_at = now()
WHERE migration_path = :'path' AND status = 'APPLYING';
SQL
  else
    psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" --set=path="${relative_path}" <<'SQL'
UPDATE phase1_deployment.schema_migrations
SET status = 'FAILED'
WHERE migration_path = :'path' AND status = 'APPLYING';
SQL
    echo "migration failed: ${database} ${relative_path}" >&2
    exit 1
  fi
done < "${manifest}"

[[ -r "${role_credentials}" ]] || { echo "runtime role credential SQL is not readable" >&2; exit 1; }
if grep -Fq '[REDACTED_SECRET]' "${role_credentials}"; then
  echo "runtime role credential SQL still contains redacted placeholders" >&2
  exit 1
fi
psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname=postgres --file="${role_credentials}"

schema_manifest_sha256="$(sha256sum "${manifest}" | awk '{print $1}')"
for database in "${databases[@]}"; do
  incomplete="$(psql --no-psqlrc --tuples-only --no-align --dbname="${database}" \
    --command="SELECT count(*) FROM phase1_deployment.schema_migrations WHERE status <> 'APPLIED'" | tr -d '[:space:]')"
  if [[ "${incomplete}" != "0" ]]; then
    echo "database ${database} contains incomplete migration state" >&2
    exit 1
  fi
  psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" \
    --set=product="${product}" \
    --set=product_version="${product_version}" \
    --set=schema_manifest_sha256="${schema_manifest_sha256}" \
    --set=release_revision="${release_revision}" <<'SQL'
INSERT INTO phase1_deployment.product_schema
  (product, product_version, schema_manifest_sha256, release_revision, updated_at)
VALUES
  (:'product', :'product_version', :'schema_manifest_sha256', :'release_revision', now())
ON CONFLICT (product) DO UPDATE SET
  product_version = EXCLUDED.product_version,
  schema_manifest_sha256 = EXCLUDED.schema_manifest_sha256,
  release_revision = EXCLUDED.release_revision,
  updated_at = EXCLUDED.updated_at;
SQL
done

echo "Phase 1 migrations completed for ${product} ${product_version}; schema=${schema_manifest_sha256}."

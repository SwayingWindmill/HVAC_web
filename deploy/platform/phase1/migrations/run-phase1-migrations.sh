#!/usr/bin/env bash
set -euo pipefail

root=/opt/hvac
repo="${root}/repo"
manifest="${root}/migrations/migration-list.tsv"
role_bootstrap="${root}/migrations/bootstrap-roles.sql"
database_bootstrap="${root}/migrations/create-databases.sql"
role_credentials="${PHASE1_DB_ROLE_CREDENTIALS_FILE:-/run/hvac/db-role-credentials/roles.sql}"

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

ensure_tracking_table() {
  local database=$1
  psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" <<'SQL'
CREATE SCHEMA IF NOT EXISTS phase1_deployment;
REVOKE ALL ON SCHEMA phase1_deployment FROM PUBLIC;
CREATE TABLE IF NOT EXISTS phase1_deployment.schema_migrations (
  migration_path text PRIMARY KEY,
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  applied_at timestamptz NOT NULL DEFAULT now(),
  release_revision text NOT NULL
);
REVOKE ALL ON phase1_deployment.schema_migrations FROM PUBLIC;
SQL
}

for database in hvac_s0 hvac_s1 hvac_s2 hvac_s3 hvac_s4 hvac_s5; do
  ensure_tracking_table "${database}"
done

sanitize_sql() {
  local source=$1
  local relative_path=$2
  local target=$3
  local stage
  stage="$(mktemp)"

  awk '
    /ALTER ROLE[[:space:]].*local-only/ { next }
    /ALTER ROLE[[:space:]].*local-fixture-only/ { next }
    /ALTER ROLE[[:space:]].*local-mutation-fixture-only/ { next }
    { print }
  ' "${source}" > "${stage}"

  case "${relative_path}" in
    infra/s1-registry/postgres/init/006-s2-telemetry-authorization.sql)
      awk '
        /^INSERT INTO iam\.role_bindings/ { skip = 1 }
        /^DROP TRIGGER IF EXISTS organization_memberships_telemetry_revocation/ { skip = 0 }
        /^INSERT INTO iam\.policies/ { skip = 1 }
        /^RESET ROLE;/ { skip = 0 }
        !skip { print }
      ' "${stage}" > "${target}"
      ;;
    infra/s1-registry/postgres/init/007-s4-alarm-authorization.sql|infra/s1-registry/postgres/init/008-s5-work-order-authorization.sql)
      awk '
        /^INSERT INTO iam\.policies/ { skip = 1 }
        /^COMMIT;/ { skip = 0 }
        !skip { print }
      ' "${stage}" > "${target}"
      ;;
    *)
      cp "${stage}" "${target}"
      ;;
  esac
  rm -f "${stage}"

  if grep -Eiq 'local-only|local-fixture-only|local-mutation-fixture-only' "${target}"; then
    echo "migration still contains a local/test credential marker: ${source}" >&2
    exit 1
  fi
}

release_revision="${PHASE1_RELEASE_REVISION:-unversioned-local-run}"

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

  sanitized="$(mktemp)"
  sanitize_sql "${source}" "${relative_path}" "${sanitized}"
  digest="$(sha256sum "${sanitized}" | awk '{print $1}')"

  existing="$(psql --no-psqlrc --tuples-only --no-align --dbname="${database}" \
    --command="SELECT sha256 FROM phase1_deployment.schema_migrations WHERE migration_path = '${relative_path}'" | tr -d '[:space:]')"

  if [[ -n "${existing}" ]]; then
    if [[ "${existing}" != "${digest}" ]]; then
      rm -f "${sanitized}"
      echo "migration drift detected for ${relative_path}: recorded=${existing} current=${digest}" >&2
      exit 1
    fi
    echo "migration already applied: ${database} ${relative_path} ${digest}"
    rm -f "${sanitized}"
    continue
  fi

  echo "applying migration: ${database} ${relative_path} ${digest}"
  psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" --file="${sanitized}"
  psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="${database}" \
    --set=path="${relative_path}" \
    --set=digest="${digest}" \
    --set=release_revision="${release_revision}" <<'SQL'
INSERT INTO phase1_deployment.schema_migrations (migration_path, sha256, release_revision)
VALUES (:'path', :'digest', :'release_revision');
SQL
  rm -f "${sanitized}"
done < "${manifest}"

[[ -r "${role_credentials}" ]] || { echo "runtime role credential SQL is not readable" >&2; exit 1; }
if grep -Fq '[REDACTED_SECRET]' "${role_credentials}"; then
  echo "runtime role credential SQL still contains redacted placeholders" >&2
  exit 1
fi
psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname=postgres --file="${role_credentials}"

echo "Phase 1 production-safe migrations completed successfully."

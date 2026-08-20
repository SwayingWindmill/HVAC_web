#!/usr/bin/env bash
set -euo pipefail

root=/opt/hvac
manifest="${root}/migrations/migration-list.tsv"
product="hvac-web"
product_version="${PHASE1_PRODUCT_VERSION:-0.1.0}"
databases=(hvac_identity hvac_s0 hvac_s1 hvac_s2 hvac_s3 hvac_s4 hvac_s5)

export PGHOST="${PGHOST:-postgres}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${PGUSER:-postgres}"

schema_manifest_sha256="$(sha256sum "${manifest}" | awk '{print $1}')"

for database in "${databases[@]}"; do
  state="$(psql --no-psqlrc --tuples-only --no-align --set=ON_ERROR_STOP=1 --dbname="${database}" \
    --set=product="${product}" <<'SQL' | tr -d '[:space:]'
SELECT product_version || '|' || schema_manifest_sha256
FROM phase1_deployment.product_schema
WHERE product = :'product';
SQL
  )"
  if [[ -z "${state}" ]]; then
    echo "schema compatibility state is missing for ${database}" >&2
    exit 1
  fi

  database_product_version="${state%%|*}"
  database_schema_manifest_sha256="${state#*|}"
  if [[ "${database_product_version}" != "${product_version}" ]]; then
    echo "product version mismatch for ${database}: expected=${product_version} actual=${database_product_version}" >&2
    exit 1
  fi
  if [[ "${database_schema_manifest_sha256}" != "${schema_manifest_sha256}" ]]; then
    echo "schema manifest mismatch for ${database}: expected=${schema_manifest_sha256} actual=${database_schema_manifest_sha256}" >&2
    exit 1
  fi

  incomplete="$(psql --no-psqlrc --tuples-only --no-align --set=ON_ERROR_STOP=1 --dbname="${database}" \
    --command="SELECT count(*) FROM phase1_deployment.schema_migrations WHERE status <> 'APPLIED'" | tr -d '[:space:]')"
  if [[ "${incomplete}" != "0" ]]; then
    echo "database ${database} contains incomplete migration state" >&2
    exit 1
  fi
done

echo "Phase 1 schema compatibility verified for ${product} ${product_version}; schema=${schema_manifest_sha256}."

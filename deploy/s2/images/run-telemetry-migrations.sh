#!/usr/bin/env sh
set -eu

: "${PGHOST:?PGHOST is required}"
: "${PGDATABASE:?PGDATABASE is required}"
: "${PGUSER:?PGUSER is required}"

lock_name='hvac:s2:telemetry:migrations'

for migration in /migrations/*.sql; do
  migration_name=${migration##*/}
  migration_sha256=$(sha256sum "$migration" | awk '{print $1}')
  case "$migration_name" in
    *[!A-Za-z0-9._-]*) echo "invalid migration filename: $migration_name" >&2; exit 1 ;;
  esac
  case "$migration_sha256" in
    ''|*[!a-f0-9]*) echo "invalid migration sha256 for $migration_name" >&2; exit 1 ;;
  esac
  [ "${#migration_sha256}" -eq 64 ] || { echo "invalid migration sha256 length for $migration_name" >&2; exit 1; }

  psql -X -v ON_ERROR_STOP=1 <<SQL
SELECT pg_advisory_lock(hashtextextended('$lock_name', 0));
SET ROLE s2_telemetry_migrator;
CREATE TABLE IF NOT EXISTS telemetry_runtime.schema_migrations (
  migration_name text PRIMARY KEY,
  sha256 text NOT NULL CHECK (sha256 ~ '^[a-f0-9]{64}$'),
  status text NOT NULL CHECK (status IN ('APPLYING', 'APPLIED')),
  started_at timestamptz NOT NULL DEFAULT now(),
  applied_at timestamptz,
  CHECK (
    (status = 'APPLYING' AND applied_at IS NULL)
    OR (status = 'APPLIED' AND applied_at IS NOT NULL)
  )
);
REVOKE ALL ON telemetry_runtime.schema_migrations FROM PUBLIC, s2_telemetry_runtime, s2_telemetry_relay;
SELECT
  NOT EXISTS (
    SELECT 1
    FROM telemetry_runtime.schema_migrations
    WHERE migration_name = '$migration_name'
  ) AS migration_missing,
  COALESCE((
    SELECT sha256 = '$migration_sha256' AND status = 'APPLIED'
    FROM telemetry_runtime.schema_migrations
    WHERE migration_name = '$migration_name'
  ), false) AS migration_current,
  COALESCE((
    SELECT sha256 = '$migration_sha256' AND status = 'APPLYING'
    FROM telemetry_runtime.schema_migrations
    WHERE migration_name = '$migration_name'
  ), false) AS migration_incomplete
\gset

\if :migration_missing
INSERT INTO telemetry_runtime.schema_migrations (migration_name, sha256, status)
VALUES ('$migration_name', '$migration_sha256', 'APPLYING');
RESET ROLE;
\echo applying $migration_name
\i $migration
SET ROLE s2_telemetry_migrator;
REVOKE ALL ON telemetry_runtime.schema_migrations FROM PUBLIC, s2_telemetry_runtime, s2_telemetry_relay;
UPDATE telemetry_runtime.schema_migrations
SET status = 'APPLIED', applied_at = now()
WHERE migration_name = '$migration_name'
  AND sha256 = '$migration_sha256'
  AND status = 'APPLYING';
DO \$migration_record\$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM telemetry_runtime.schema_migrations
    WHERE migration_name = '$migration_name'
      AND sha256 = '$migration_sha256'
      AND status = 'APPLIED'
      AND applied_at IS NOT NULL
  ) THEN
    RAISE EXCEPTION 'failed to record applied migration';
  END IF;
END
\$migration_record\$;
RESET ROLE;
\else
RESET ROLE;
\if :migration_current
\echo already applied $migration_name
\else
SELECT pg_advisory_unlock(hashtextextended('$lock_name', 0));
\if :migration_incomplete
\echo incomplete migration requires operator review: $migration_name
DO \$migration_error\$
BEGIN
  RAISE EXCEPTION 'incomplete migration requires operator review';
END
\$migration_error\$;
\else
\echo migration hash mismatch for $migration_name
DO \$migration_error\$
BEGIN
  RAISE EXCEPTION 'migration hash mismatch';
END
\$migration_error\$;
\endif
\endif
\endif

SELECT pg_advisory_unlock(hashtextextended('$lock_name', 0));
SQL
done

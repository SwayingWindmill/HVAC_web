#!/usr/bin/env sh
set -eu
: "${PGHOST:?PGHOST is required}"
: "${PGDATABASE:?PGDATABASE is required}"
: "${PGUSER:?PGUSER is required}"
for migration in /migrations/*.sql; do
  psql -X -v ON_ERROR_STOP=1 -f "$migration"
done

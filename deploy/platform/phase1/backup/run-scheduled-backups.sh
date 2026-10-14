#!/usr/bin/env bash
# The nightly backup run of a single-server deployment, started by hvac-backup.timer: a PostgreSQL base
# backup and a ClickHouse backup through the Compose backup services, then only the newest BACKUP_KEEP
# of each are kept. PHASE1_RUNTIME_DIR, PHASE1_ENV_FILE and PHASE1_DEPLOYMENT_TIER come from the unit's
# environment file, as for every other Compose command on the host.
set -euo pipefail
: "${PHASE1_RUNTIME_DIR:?PHASE1_RUNTIME_DIR is required}"
keep="${BACKUP_KEEP:-7}"
cd "$(dirname "$0")/../../../.."

compose() { node scripts/phase1-wsl-compose.mjs --profile backup "$@"; }
# The base backup runs inside the PostgreSQL container: the image's pg_hba.conf allows replication
# connections only from the container itself, and wsl.override.yaml mounts the backup directory there.
compose exec -T -e PGHOST=127.0.0.1 -e BACKUP_ROOT=/backups/base postgres bash -s < deploy/platform/phase1/backup/postgres-base-backup.sh
compose run --rm clickhouse-backup

prune() {
  find "$1" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort | head -n -"${keep}" | while read -r name; do
    rm -rf -- "${1:?}/${name}"
    echo "removed old backup $1/${name}"
  done
}
prune "${POSTGRES_BASE_BACKUP_DIR:-${PHASE1_RUNTIME_DIR}/backups/postgres/base}"
prune "${CLICKHOUSE_BACKUP_DIR:-${PHASE1_RUNTIME_DIR}/backups/clickhouse}"

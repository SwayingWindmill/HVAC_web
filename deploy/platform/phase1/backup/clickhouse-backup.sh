#!/usr/bin/env bash
set -euo pipefail

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_name="${stamp}"

clickhouse-client \
  --host "${CLICKHOUSE_HOST:-clickhouse}" \
  --query "BACKUP DATABASE telemetry_history, DATABASE analytics TO Disk('phase1_backups', '${backup_name}')"

echo "ClickHouse backup completed: ${backup_name}"

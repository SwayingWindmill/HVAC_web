#!/usr/bin/env bash
set -euo pipefail

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_name="telemetry_history_${stamp}"

clickhouse-client \
  --host clickhouse \
  --query "BACKUP DATABASE telemetry_history TO Disk('phase1_backups', '${backup_name}')"

echo "ClickHouse backup completed: ${backup_name}"

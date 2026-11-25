#!/usr/bin/env bash
# Encrypt and upload Phase 1 PostgreSQL + ClickHouse backups (and WAL archives) via restic
# to Tencent Cloud COS (or any configured restic repository), prune retention, and spot-check data.
# Intended for BACKUP_OFFSITE_SYNC_CMD in /etc/hvac/backup.env.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=deploy/platform/phase1/backup/restic-common.sh
source "$script_dir/restic-common.sh"
restic_load_offsite_env

keep_daily="${RESTIC_KEEP_DAILY:-7}"
keep_weekly="${RESTIC_KEEP_WEEKLY:-4}"
check_subset="${RESTIC_CHECK_SUBSET:-1%}"
project_tag="${HVAC_LOCAL_PROJECT:-hvac-platform}"

[[ "$keep_daily" =~ ^[1-9][0-9]*$ ]] || {
  echo "error: RESTIC_KEEP_DAILY must be a positive integer, got '$keep_daily'" >&2
  exit 1
}
[[ "$keep_weekly" =~ ^[1-9][0-9]*$ ]] || {
  echo "error: RESTIC_KEEP_WEEKLY must be a positive integer, got '$keep_weekly'" >&2
  exit 1
}

runtime_dir="${PHASE1_RUNTIME_DIR:-}"
pg_base_dir="${POSTGRES_BASE_BACKUP_DIR:-${runtime_dir:+$runtime_dir/backups/postgres/base}}"
pg_wal_dir="${POSTGRES_WAL_ARCHIVE_DIR:-${runtime_dir:+$runtime_dir/backups/postgres/wal}}"
ch_backup_dir="${CLICKHOUSE_BACKUP_DIR:-${runtime_dir:+$runtime_dir/backups/clickhouse}}"

if [[ -z "$pg_base_dir" || -z "$ch_backup_dir" ]]; then
  echo "error: set PHASE1_RUNTIME_DIR or POSTGRES_BASE_BACKUP_DIR and CLICKHOUSE_BACKUP_DIR." >&2
  exit 1
fi
if ! restic_has_timestamp_dir "$pg_base_dir"; then
  echo "error: PostgreSQL base backup directory has no timestamped backup: $pg_base_dir" >&2
  exit 1
fi
if ! restic_has_timestamp_dir "$ch_backup_dir"; then
  echo "error: ClickHouse backup directory has no timestamped backup: $ch_backup_dir" >&2
  exit 1
fi

declare -a backup_paths=("$pg_base_dir" "$ch_backup_dir")
if [[ -n "$pg_wal_dir" && -d "$pg_wal_dir" ]] && [[ -n "$(find "$pg_wal_dir" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  backup_paths+=("$pg_wal_dir")
fi
if [[ -n "${BACKUP_EXTRA_PATHS:-}" ]]; then
  local_IFS="$IFS"
  IFS=':'
  for extra_path in $BACKUP_EXTRA_PATHS; do
    if [[ -n "$extra_path" && -e "$extra_path" ]]; then
      backup_paths+=("$extra_path")
    fi
  done
  IFS="$local_IFS"
fi

probe_err=""
probe_rc=0
probe_err="$(restic "${RESTIC_COMMON_OPTS[@]}" cat config 2>&1 >/dev/null)" || probe_rc=$?
if [[ "$probe_rc" -ne 0 ]]; then
  if [[ "$probe_err" == *"Is there a repository at the following location"* ]] \
    || [[ "$probe_err" == *"config file does not exist"* ]] \
    || [[ "$probe_err" == *"unable to open config file"* ]]; then
    echo "initializing restic repository at ${RESTIC_REPOSITORY}"
    restic "${RESTIC_COMMON_OPTS[@]}" init
  else
    echo "error: failed to access restic repository (${RESTIC_REPOSITORY}): ${probe_err}" >&2
    exit 1
  fi
fi

echo "backing up ${#backup_paths[@]} directories to restic offsite repository"
restic "${RESTIC_COMMON_OPTS[@]}" backup \
  --one-file-system \
  --tag phase1-backup \
  --tag "$project_tag" \
  "${backup_paths[@]}"

echo "enforcing restic retention (keep-daily=${keep_daily}, keep-weekly=${keep_weekly})"
restic "${RESTIC_COMMON_OPTS[@]}" forget \
  --tag phase1-backup \
  --keep-daily "$keep_daily" \
  --keep-weekly "$keep_weekly" \
  --prune

if [[ "$check_subset" != "0" && "$check_subset" != "none" ]]; then
  echo "verifying offsite repository integrity (subset=${check_subset})"
  restic "${RESTIC_COMMON_OPTS[@]}" check --read-data-subset="$check_subset"
fi

echo "offsite restic sync completed"

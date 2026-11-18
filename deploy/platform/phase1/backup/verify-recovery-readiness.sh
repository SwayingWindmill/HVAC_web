#!/usr/bin/env bash
set -euo pipefail

wal_dir="${POSTGRES_WAL_ARCHIVE_DIR:?POSTGRES_WAL_ARCHIVE_DIR is required}"
base_dir="${POSTGRES_BASE_BACKUP_DIR:?POSTGRES_BASE_BACKUP_DIR is required}"
rpo_target_seconds="${POSTGRES_RPO_TARGET_SECONDS:-300}"

[[ "${rpo_target_seconds}" =~ ^[0-9]+$ ]] && (( rpo_target_seconds > 0 )) || {
  echo "POSTGRES_RPO_TARGET_SECONDS must be a positive integer" >&2
  exit 1
}
[[ -d "${wal_dir}" ]] || { echo "PostgreSQL WAL archive directory is missing: ${wal_dir}" >&2; exit 1; }
[[ -d "${base_dir}" ]] || { echo "PostgreSQL base backup directory is missing: ${base_dir}" >&2; exit 1; }

latest_wal_line="$(find "${wal_dir}" -maxdepth 1 -type f -printf '%T@ %p\n' | sort -nr | head -n 1 || true)"
[[ -n "${latest_wal_line}" ]] || { echo "No archived PostgreSQL WAL file was found" >&2; exit 1; }
latest_wal_epoch="${latest_wal_line%% *}"
latest_wal_epoch="${latest_wal_epoch%.*}"
now_epoch="$(date -u +%s)"
wal_age_seconds=$(( now_epoch - latest_wal_epoch ))
(( wal_age_seconds >= 0 )) || wal_age_seconds=0
if (( wal_age_seconds > rpo_target_seconds )); then
  echo "PostgreSQL WAL archive age ${wal_age_seconds}s exceeds RPO readiness threshold ${rpo_target_seconds}s" >&2
  exit 1
fi

latest_base="$(find "${base_dir}" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' | sort -nr | head -n 1 | cut -d' ' -f2- || true)"
[[ -n "${latest_base}" ]] || { echo "No PostgreSQL base backup directory was found" >&2; exit 1; }
[[ -r "${latest_base}/SHA256SUMS" ]] || { echo "Latest PostgreSQL base backup has no SHA256SUMS: ${latest_base}" >&2; exit 1; }
(
  cd "${latest_base}"
  sha256sum --check SHA256SUMS >/dev/null
)

printf 'PostgreSQL recovery readiness OK: walAgeSeconds=%s targetSeconds=%s latestBaseBackup=%s\n' \
  "${wal_age_seconds}" "${rpo_target_seconds}" "${latest_base}"

clickhouse_dir="${CLICKHOUSE_BACKUP_DIR:-}"
if [[ -n "${clickhouse_dir}" ]]; then
  clickhouse_rpo_target_seconds="${CLICKHOUSE_RPO_TARGET_SECONDS:-90000}"
  [[ "${clickhouse_rpo_target_seconds}" =~ ^[0-9]+$ ]] && (( clickhouse_rpo_target_seconds > 0 )) || {
    echo "CLICKHOUSE_RPO_TARGET_SECONDS must be a positive integer" >&2
    exit 1
  }
  [[ -d "${clickhouse_dir}" ]] || { echo "ClickHouse backup directory is missing: ${clickhouse_dir}" >&2; exit 1; }

  latest_ch_line=""
  while IFS= read -r line; do
    ch_path="${line#* }"
    ch_name="$(basename "${ch_path}")"
    if [[ "${ch_name}" =~ ^[0-9]{8}T[0-9]{6}Z$ ]]; then
      latest_ch_line="${line}"
      break
    fi
  done < <(find "${clickhouse_dir}" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' | sort -nr)

  [[ -n "${latest_ch_line}" ]] || { echo "No ClickHouse timestamp backup directory was found under ${clickhouse_dir}" >&2; exit 1; }
  latest_ch_epoch="${latest_ch_line%% *}"
  latest_ch_epoch="${latest_ch_epoch%.*}"
  latest_ch_dir="${latest_ch_line#* }"

  if [[ -z "$(find "${latest_ch_dir}" -type f -print -quit)" ]]; then
    echo "Latest ClickHouse backup directory is empty: ${latest_ch_dir}" >&2
    exit 1
  fi

  now_epoch="$(date -u +%s)"
  ch_age_seconds=$(( now_epoch - latest_ch_epoch ))
  (( ch_age_seconds >= 0 )) || ch_age_seconds=0
  if (( ch_age_seconds > clickhouse_rpo_target_seconds )); then
    echo "ClickHouse backup age ${ch_age_seconds}s exceeds readiness threshold ${clickhouse_rpo_target_seconds}s: ${latest_ch_dir}" >&2
    exit 1
  fi

  printf 'ClickHouse recovery readiness OK: backupAgeSeconds=%s targetSeconds=%s latestBackup=%s\n' \
    "${ch_age_seconds}" "${clickhouse_rpo_target_seconds}" "${latest_ch_dir}"
fi

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

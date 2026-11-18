#!/usr/bin/env bash
# The nightly backup run of a single-server deployment, started by hvac-backup.timer: a PostgreSQL base
# backup and a ClickHouse backup, each run inside its database's container, then only the newest
# BACKUP_KEEP of each are kept, archived PostgreSQL WAL older than the oldest retained base backup
# is pruned via pg_archivecleanup, and an optional offsite sync command runs. PHASE1_RUNTIME_DIR,
# PHASE1_ENV_FILE and PHASE1_DEPLOYMENT_TIER come from the unit's environment file, as for every
# other Compose command on the host.
set -euo pipefail
: "${PHASE1_RUNTIME_DIR:?PHASE1_RUNTIME_DIR is required}"
keep="${BACKUP_KEEP:-7}"
[[ "${keep}" =~ ^[1-9][0-9]*$ ]] || {
  echo "BACKUP_KEEP must be a positive integer, got: ${keep}" >&2
  exit 1
}
cd "$(dirname "$0")/../../../.."

compose() { node scripts/phase1-wsl-compose.mjs --profile backup "$@"; }
stamp() {
  mkdir -p "${PHASE1_RUNTIME_DIR}/observability"
  date -u +%s > "${PHASE1_RUNTIME_DIR}/observability/$1"
}
# Both backups run inside their database's container: the postgres image's pg_hba.conf allows
# replication only from the container itself (wsl.override.yaml mounts the backup directory there),
# and ClickHouse's default user only from localhost.
compose exec -T -e PGHOST=127.0.0.1 -e BACKUP_ROOT=/backups/base postgres bash -s < deploy/platform/phase1/backup/postgres-base-backup.sh
stamp postgres-backup.success
compose exec -T -e CLICKHOUSE_HOST=localhost clickhouse bash -s < deploy/platform/phase1/backup/clickhouse-backup.sh
stamp clickhouse-backup.success

prune() {
  local dir="$1"
  [[ -d "${dir}" ]] || return 0
  local -a backups=()
  local name
  while IFS= read -r name; do
    [[ "${name}" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] || continue
    backups+=("${name}")
  done < <(find "${dir}" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | LC_ALL=C sort)

  local count="${#backups[@]}"
  if (( count <= keep )); then
    return 0
  fi

  local i
  for (( i = 0; i < count - keep; i += 1 )); do
    name="${backups[i]}"
    rm -rf -- "${dir:?}/${name}"
    echo "removed old backup ${dir}/${name}"
  done
}

resolve_oldest_postgres_wal_label() {
  local base_dir="$1"
  local wal_dir="$2"
  local oldest_name=""
  local candidate
  [[ -d "${base_dir}" ]] || return 0
  while IFS= read -r candidate; do
    if [[ "${candidate}" =~ ^[0-9]{8}T[0-9]{6}Z$ ]]; then
      oldest_name="${candidate}"
      break
    fi
  done < <(find "${base_dir}" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | LC_ALL=C sort)
  [[ -n "${oldest_name}" ]] || return 0

  local oldest_dir="${base_dir}/${oldest_name}"
  local start_lsn=""
  if [[ -r "${oldest_dir}/backup_manifest" ]]; then
    start_lsn="$(
      node -e '
        const fs = require("node:fs");
        const manifest = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
        const range = Array.isArray(manifest["WAL-Ranges"]) ? manifest["WAL-Ranges"][0] : null;
        if (range && typeof range["Start-LSN"] === "string") process.stdout.write(range["Start-LSN"]);
      ' "${oldest_dir}/backup_manifest" 2>/dev/null || true
    )"
  fi

  if [[ -n "${start_lsn}" ]] && [[ -d "${wal_dir}" ]]; then
    local backup_meta
    while IFS= read -r backup_meta; do
      if grep -qF "START WAL LOCATION: ${start_lsn} " "${backup_meta}"; then
        basename "${backup_meta}"
        return 0
      fi
    done < <(find "${wal_dir}" -maxdepth 1 -type f -name '*.backup' | LC_ALL=C sort)
  fi

  local label_text=""
  if [[ -r "${oldest_dir}/backup_label" ]]; then
    label_text="$(cat "${oldest_dir}/backup_label")"
  elif [[ -r "${oldest_dir}/base.tar.gz" ]]; then
    label_text="$(
      tar --occurrence=1 -xzOf "${oldest_dir}/base.tar.gz" backup_label 2>/dev/null \
        || tar --occurrence=1 -xzOf "${oldest_dir}/base.tar.gz" ./backup_label 2>/dev/null \
        || true
    )"
  fi
  if [[ "${label_text}" =~ START\ WAL\ LOCATION:\ [0-9A-Fa-f]+/[0-9A-Fa-f]+\ \(file\ ([0-9A-Fa-f]{24})\) ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi

  # Fallback when the base backup lacks backup_manifest/backup_label: use oldest .backup marker.
  if [[ -d "${wal_dir}" ]]; then
    find "${wal_dir}" -maxdepth 1 -type f -name '*.backup' -printf '%f\n' | LC_ALL=C sort | head -n 1 || true
  fi
}

postgres_base_dir="${POSTGRES_BASE_BACKUP_DIR:-${PHASE1_RUNTIME_DIR}/backups/postgres/base}"
postgres_wal_dir="${POSTGRES_WAL_ARCHIVE_DIR:-${PHASE1_RUNTIME_DIR}/backups/postgres/wal}"
clickhouse_backup_dir="${CLICKHOUSE_BACKUP_DIR:-${PHASE1_RUNTIME_DIR}/backups/clickhouse}"

prune "${postgres_base_dir}"
prune "${clickhouse_backup_dir}"

oldest_backup_wal_label="$(resolve_oldest_postgres_wal_label "${postgres_base_dir}" "${postgres_wal_dir}")"
if [[ -n "${oldest_backup_wal_label}" ]]; then
  compose exec -T postgres pg_archivecleanup /var/lib/postgresql/wal-archive "${oldest_backup_wal_label}"
  echo "cleaned archived PostgreSQL WAL older than ${oldest_backup_wal_label}"
fi

if [[ -n "${BACKUP_OFFSITE_SYNC_CMD:-}" ]]; then
  bash -c "${BACKUP_OFFSITE_SYNC_CMD}"
  stamp offsite-backup.success
fi

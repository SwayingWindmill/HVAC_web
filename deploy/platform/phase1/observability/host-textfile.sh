#!/bin/sh
# Write node-exporter textfile metrics for backup age and container restart/health.
# Run on the host (systemd timer). Backup age prefers the success stamp written by
# run-scheduled-backups.sh, and otherwise the newest backup directory.
set -eu

runtime="${PHASE1_RUNTIME_DIR:?PHASE1_RUNTIME_DIR is required}"
out_dir="${HOST_TEXTFILE_DIR:-$runtime/observability/textfile}"
project="${HVAC_COMPOSE_PROJECT:-hvac-platform}"
postgres_stamp="${POSTGRES_BACKUP_STAMP:-$runtime/observability/postgres-backup.success}"
clickhouse_stamp="${CLICKHOUSE_BACKUP_STAMP:-$runtime/observability/clickhouse-backup.success}"
postgres_dir="${POSTGRES_BASE_BACKUP_DIR:-$runtime/backups/postgres/base}"
clickhouse_dir="${CLICKHOUSE_BACKUP_DIR:-$runtime/backups/clickhouse}"

mkdir -p "$out_dir"

newest_dir_mtime() {
  dir="$1"
  if [ ! -d "$dir" ]; then
    echo 0
    return
  fi
  newest=$(find "$dir" -mindepth 1 -maxdepth 1 -type d -printf '%T@\n' 2>/dev/null | sort -n | tail -n 1 || true)
  if [ -z "$newest" ]; then
    echo 0
    return
  fi
  printf '%.0f\n' "$newest"
}

backup_timestamp() {
  stamp="$1"
  dir="$2"
  if [ -s "$stamp" ]; then
    value=$(tr -d '[:space:]' < "$stamp")
    case "$value" in
      ''|*[!0-9]*) newest_dir_mtime "$dir" ;;
      *) printf '%s\n' "$value" ;;
    esac
    return
  fi
  newest_dir_mtime "$dir"
}

postgres_ts=$(backup_timestamp "$postgres_stamp" "$postgres_dir")
clickhouse_ts=$(backup_timestamp "$clickhouse_stamp" "$clickhouse_dir")

tmp=$(mktemp "$out_dir/.host.XXXXXX")
samples=$(mktemp "$out_dir/.samples.XXXXXX")
cleanup() { rm -f "$tmp" "$samples"; }
trap cleanup EXIT

metrics_up=0
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  metrics_up=1
  docker ps -a --filter "label=com.docker.compose.project=${project}" --format '{{.ID}} {{.Label "com.docker.compose.service"}}' | while read -r cid service; do
    case "$service" in
      ''|*[!a-zA-Z0-9_-]*) continue ;;
    esac
    inspect=$(docker inspect --format '{{.RestartCount}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$cid" 2>/dev/null || true)
    restarts=$(printf '%s' "$inspect" | awk '{print $1}')
    health=$(printf '%s' "$inspect" | awk '{print $2}')
    case "$restarts" in
      ''|*[!0-9]*) continue ;;
    esac
    unhealthy=0
    if [ "$health" = "unhealthy" ]; then
      unhealthy=1
    fi
    printf 'hvac_container_restarts{service="%s"} %s\n' "$service" "$restarts"
    printf 'hvac_container_unhealthy{service="%s"} %s\n' "$service" "$unhealthy"
  done > "$samples"
fi

{
  cat <<EOF
# HELP hvac_backup_last_success_timestamp_seconds Unix time of the newest successful backup. 0 when there is none.
# TYPE hvac_backup_last_success_timestamp_seconds gauge
hvac_backup_last_success_timestamp_seconds{store="postgres"} ${postgres_ts}
hvac_backup_last_success_timestamp_seconds{store="clickhouse"} ${clickhouse_ts}
# HELP hvac_container_restarts Docker RestartCount for one Compose service.
# TYPE hvac_container_restarts counter
EOF
  if [ -s "$samples" ]; then
    grep '^hvac_container_restarts' "$samples" || true
  fi
  cat <<'EOF'
# HELP hvac_container_unhealthy 1 when Docker reports the container health as unhealthy.
# TYPE hvac_container_unhealthy gauge
EOF
  if [ -s "$samples" ]; then
    grep '^hvac_container_unhealthy' "$samples" || true
  fi
  cat <<EOF
# HELP hvac_container_metrics_up 1 when this host could read Docker container state.
# TYPE hvac_container_metrics_up gauge
hvac_container_metrics_up ${metrics_up}
EOF
} > "$tmp"
rm -f "$samples"

chmod 644 "$tmp"
mv "$tmp" "$out_dir/host.prom"
trap - EXIT
echo "wrote $out_dir/host.prom"

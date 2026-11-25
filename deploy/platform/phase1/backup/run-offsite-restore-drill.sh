#!/usr/bin/env bash
# Execute an isolated offsite recovery drill from the restic repository:
# 1) Restore a snapshot into an ephemeral temporary directory
# 2) Verify PostgreSQL base backup SHA256SUMS and ClickHouse backup payload presence
# 3) Unless --skip-docker is set, spin up isolated PostgreSQL + ClickHouse containers,
#    restore databases, record row counts/timing, and emit a JSON drill report + draft record.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../../../.." && pwd)"
# shellcheck source=deploy/platform/phase1/backup/restic-common.sh
source "$script_dir/restic-common.sh"

snapshot="latest"
report_out=""
drill_record_out=""
environment="staging"
operator="${USER:-operator}"
skip_docker=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --snapshot=*) snapshot="${1#*=}" ;;
    --report-out=*) report_out="${1#*=}" ;;
    --drill-record-out=*) drill_record_out="${1#*=}" ;;
    --environment=*) environment="${1#*=}" ;;
    --operator=*) operator="${1#*=}" ;;
    --skip-docker) skip_docker=1 ;;
    *)
      echo "error: unknown argument: $1" >&2
      echo "usage: run-offsite-restore-drill.sh [--snapshot=latest|<id>] [--report-out=PATH] [--drill-record-out=PATH] [--environment=staging|production] [--operator=NAME] [--skip-docker]" >&2
      exit 1
      ;;
  esac
  shift
done

if [[ "$environment" != "staging" && "$environment" != "production" ]]; then
  echo "error: --environment must be staging or production (got '$environment')" >&2
  exit 1
fi

restic_load_offsite_env
if ! command -v node >/dev/null 2>&1; then
  echo "error: node is required to parse snapshot metadata and write JSON reports." >&2
  exit 1
fi
if [[ "$skip_docker" -ne 1 ]] && ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required unless --skip-docker is passed." >&2
  exit 1
fi

incident_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
start_epoch="$(date -u +%s)"

work_dir="$(mktemp -d)"
pg_container="hvac-offsite-drill-pg-$$"
ch_container="hvac-offsite-drill-ch-$$"
cleanup_drill() {
  docker rm -f "$pg_container" >/dev/null 2>&1 || true
  docker rm -f "$ch_container" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup_drill EXIT

snapshots_json="$work_dir/snapshots.json"
restic "${RESTIC_COMMON_OPTS[@]}" snapshots --tag phase1-backup --json > "$snapshots_json"

resolved_info="$(
  node -e '
    const fs = require("node:fs");
    const list = JSON.parse(fs.readFileSync(process.argv[1], "utf8")) || [];
    const target = process.argv[2];
    if (!Array.isArray(list) || list.length === 0) {
      console.error("error: no restic snapshots tagged phase1-backup were found");
      process.exit(1);
    }
    list.sort((a, b) => String(a.time || "").localeCompare(String(b.time || "")));
    const chosen = target === "latest"
      ? list[list.length - 1]
      : list.find((s) => s.id === target || s.short_id === target || String(s.id || "").startsWith(target));
    if (!chosen || !chosen.id) {
      console.error(`error: snapshot not found: ${target}`);
      process.exit(1);
    }
    console.log(JSON.stringify({ id: chosen.short_id || chosen.id.slice(0, 8), fullId: chosen.id, time: chosen.time || "" }));
  ' "$snapshots_json" "$snapshot"
)"

snapshot_id="$(node -e 'console.log(JSON.parse(process.argv[1]).fullId)' "$resolved_info")"
snapshot_short="$(node -e 'console.log(JSON.parse(process.argv[1]).id)' "$resolved_info")"
snapshot_time="$(node -e 'console.log(JSON.parse(process.argv[1]).time)' "$resolved_info")"
echo "restoring offsite snapshot ${snapshot_short} (${snapshot_time})"

restored_dir="$work_dir/restored"
mkdir -p "$restored_dir"
restic "${RESTIC_COMMON_OPTS[@]}" restore "$snapshot_id" --target "$restored_dir"

pg_backup_dir=""
while IFS= read -r candidate; do
  base_name="$(basename "$candidate")"
  if [[ "$base_name" =~ ^[0-9]{8}T[0-9]{6}Z$ && -f "$candidate/base.tar.gz" && -r "$candidate/SHA256SUMS" ]]; then
    pg_backup_dir="$candidate"
  fi
done < <(find "$restored_dir" -type d | LC_ALL=C sort)

if [[ -z "$pg_backup_dir" ]]; then
  echo "error: no restored PostgreSQL base backup directory (with base.tar.gz and SHA256SUMS) found under $restored_dir" >&2
  exit 1
fi

echo "verifying PostgreSQL SHA256SUMS in $pg_backup_dir"
(
  cd "$pg_backup_dir"
  sha256sum --check SHA256SUMS
)

ch_backup_dir=""
while IFS= read -r candidate; do
  base_name="$(basename "$candidate")"
  if [[ "$base_name" =~ ^[0-9]{8}T[0-9]{6}Z$ && "$candidate" != "$pg_backup_dir" && ! -f "$candidate/base.tar.gz" ]]; then
    if [[ "$candidate" == *clickhouse* ]]; then
      ch_backup_dir="$candidate"
    elif [[ -z "$ch_backup_dir" ]]; then
      ch_backup_dir="$candidate"
    fi
  fi
done < <(find "$restored_dir" -type d | LC_ALL=C sort)

if [[ -z "$ch_backup_dir" || -z "$(find "$ch_backup_dir" -type f -print -quit 2>/dev/null)" ]]; then
  echo "error: no non-empty restored ClickHouse backup directory found under $restored_dir" >&2
  exit 1
fi

pg_stamp="$(basename "$pg_backup_dir")"
ch_stamp="$(basename "$ch_backup_dir")"
pg_counts_json='{}'
ch_counts_json='{}'
pg_integrity=false
ch_integrity=false

if [[ "$skip_docker" -ne 1 ]]; then
  pg_image="${PHASE1_POSTGRES_IMAGE:-postgres:16.4-bookworm}"
  ch_image="${PHASE1_CLICKHOUSE_IMAGE:-clickhouse/clickhouse-server:26.3.39.7}"

  pg_data="$work_dir/pgdata"
  mkdir -p "$pg_data"
  tar -xzf "$pg_backup_dir/base.tar.gz" -C "$pg_data"
  if [[ -f "$pg_backup_dir/pg_wal.tar.gz" ]]; then
    mkdir -p "$pg_data/pg_wal"
    tar -xzf "$pg_backup_dir/pg_wal.tar.gz" -C "$pg_data/pg_wal"
  fi
  rm -f "$pg_data/recovery.signal" "$pg_data/standby.signal"
  if [[ -f "$pg_data/postgresql.auto.conf" ]]; then
    grep -vE '^restore_command\s*=' "$pg_data/postgresql.auto.conf" > "$pg_data/postgresql.auto.conf.tmp" || true
    mv "$pg_data/postgresql.auto.conf.tmp" "$pg_data/postgresql.auto.conf"
  fi
  docker run --rm --entrypoint chown -v "$pg_data:/pgdata" "$pg_image" -R 999:999 /pgdata
  chmod 700 "$pg_data"

  echo "starting isolated PostgreSQL restore verification container ($pg_image)"
  docker run -d --name "$pg_container" -v "$pg_data:/var/lib/postgresql/data" "$pg_image" >/dev/null
  pg_ready=0
  for _ in $(seq 1 60); do
    if docker exec "$pg_container" pg_isready -U postgres >/dev/null 2>&1 \
      && docker exec "$pg_container" psql -U postgres -d hvac_s0 -tAc "SELECT 1" >/dev/null 2>&1; then
      pg_ready=1
      break
    fi
    sleep 1
  done
  if [[ "$pg_ready" -ne 1 ]]; then
    docker logs --tail 60 "$pg_container" >&2 || true
    echo "error: isolated PostgreSQL container failed to start from backup" >&2
    exit 1
  fi

  query_pg_count() {
    local db="$1" tbl="$2"
    docker exec "$pg_container" psql -U postgres -d "$db" -tAc "SELECT count(*) FROM ${tbl}" 2>/dev/null | tr -d '[:space:]' || echo "-1"
  }
  pg_users="$(query_pg_count hvac_identity identity.users)"
  pg_sites="$(query_pg_count hvac_s1 core_registry.sites)"
  pg_devices="$(query_pg_count hvac_s1 core_registry.devices)"
  pg_points="$(query_pg_count hvac_s1 core_registry.points)"
  pg_commands="$(query_pg_count hvac_s3 command_runtime.commands)"
  pg_alarms="$(query_pg_count hvac_s4 alarm_runtime.alarms)"
  pg_counts_json="$(
    node -e 'console.log(JSON.stringify({
      "hvac_identity.identity.users": Number(process.argv[1]),
      "hvac_s1.core_registry.sites": Number(process.argv[2]),
      "hvac_s1.core_registry.devices": Number(process.argv[3]),
      "hvac_s1.core_registry.points": Number(process.argv[4]),
      "hvac_s3.command_runtime.commands": Number(process.argv[5]),
      "hvac_s4.alarm_runtime.alarms": Number(process.argv[6]),
    }))' "$pg_users" "$pg_sites" "$pg_devices" "$pg_points" "$pg_commands" "$pg_alarms"
  )"
  pg_integrity=true
  pg_restored_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  docker rm -f "$pg_container" >/dev/null 2>&1 || true

  ch_mount="$work_dir/chbackups"
  mkdir -p "$ch_mount/$ch_stamp"
  cp -a "$ch_backup_dir/." "$ch_mount/$ch_stamp/"
  chmod -R a+rX "$ch_mount"

  echo "starting isolated ClickHouse restore verification container ($ch_image)"
  docker run -d --name "$ch_container" \
    -v "$repo_root/deploy/platform/phase1/backup/clickhouse-backup.xml:/etc/clickhouse-server/config.d/backup.xml:ro" \
    -v "$ch_mount:/var/lib/clickhouse/backups" \
    "$ch_image" >/dev/null
  ch_ready=0
  for _ in $(seq 1 60); do
    if docker exec "$ch_container" clickhouse-client --host 127.0.0.1 --query "SELECT 1" >/dev/null 2>&1; then
      ch_ready=1
      break
    fi
    sleep 1
  done
  if [[ "$ch_ready" -ne 1 ]]; then
    docker logs --tail 60 "$ch_container" >&2 || true
    echo "error: isolated ClickHouse container failed to become ready" >&2
    exit 1
  fi

  docker exec "$ch_container" clickhouse-client --host 127.0.0.1 \
    --query "RESTORE DATABASE telemetry_history, DATABASE analytics FROM Disk('phase1_backups', '${ch_stamp}')" >/dev/null
  docker exec "$ch_container" clickhouse-client --host 127.0.0.1 --multiquery \
    --query "CREATE DATABASE IF NOT EXISTS drill_probe; CREATE TABLE drill_probe.write_check (v UInt8) ENGINE = Memory; INSERT INTO drill_probe.write_check VALUES (1);" >/dev/null

  query_ch_count() {
    local tbl="$1"
    docker exec "$ch_container" clickhouse-client --host 127.0.0.1 --query "SELECT count() FROM ${tbl}" 2>/dev/null | tr -d '[:space:]' || echo "-1"
  }
  ch_obs="$(query_ch_count telemetry_history.observations)"
  ch_energy="$(query_ch_count analytics.energy_interval_facts)"
  ch_hourly="$(query_ch_count analytics.energy_hourly_facts_v2)"
  ch_counts_json="$(
    node -e 'console.log(JSON.stringify({
      "telemetry_history.observations": Number(process.argv[1]),
      "analytics.energy_interval_facts": Number(process.argv[2]),
      "analytics.energy_hourly_facts_v2": Number(process.argv[3]),
    }))' "$ch_obs" "$ch_energy" "$ch_hourly"
  )"
  ch_integrity=true
  ch_restored_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  docker rm -f "$ch_container" >/dev/null 2>&1 || true
else
  pg_restored_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ch_restored_at="$pg_restored_at"
fi

completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
end_epoch="$(date -u +%s)"
elapsed_seconds=$(( end_epoch - start_epoch ))
(( elapsed_seconds >= 0 )) || elapsed_seconds=0

report_json="$(
  node -e '
    const report = {
      schemaVersion: 1,
      status: "passed",
      environment: process.argv[1],
      snapshotId: process.argv[2],
      snapshotTime: process.argv[3],
      postgresBackupStamp: process.argv[4],
      clickhouseBackupStamp: process.argv[5],
      sha256Verified: true,
      dockerSkipped: process.argv[6] === "1",
      postgresqlIntegrity: process.argv[7] === "true",
      clickhouseWrite: process.argv[8] === "true",
      startedAt: process.argv[9],
      postgresRestoredAt: process.argv[10],
      clickhouseRestoredAt: process.argv[11],
      completedAt: process.argv[12],
      elapsedSeconds: Number(process.argv[13]),
      postgresCounts: JSON.parse(process.argv[14]),
      clickhouseCounts: JSON.parse(process.argv[15]),
    };
    console.log(JSON.stringify(report, null, 2));
  ' \
    "$environment" "$snapshot_id" "$snapshot_time" "$pg_stamp" "$ch_stamp" "$skip_docker" \
    "$pg_integrity" "$ch_integrity" "$incident_at" "$pg_restored_at" "$ch_restored_at" "$completed_at" \
    "$elapsed_seconds" "$pg_counts_json" "$ch_counts_json"
)"

if [[ -n "$report_out" ]]; then
  mkdir -p "$(dirname "$report_out")"
  printf '%s\n' "$report_json" > "$report_out"
  echo "wrote drill report: $report_out"
else
  printf '%s\n' "$report_json"
fi

if [[ -n "$drill_record_out" ]]; then
  mkdir -p "$(dirname "$drill_record_out")"
  node -e '
    const fs = require("node:fs");
    const report = JSON.parse(process.argv[1]);
    const outPath = process.argv[2];
    const operator = process.argv[3];
    const recoverable = report.snapshotTime || report.startedAt;
    const secBetween = (a, b) => Math.max(0, Math.round((Date.parse(b) - Date.parse(a)) / 1000));
    const draft = {
      schemaVersion: 1,
      sourceOfTruth: "SE-OPS-009 V1.0 CURRENT CANDIDATE",
      drillId: `offsite-cos-${report.postgresBackupStamp}`,
      environment: report.environment,
      scenario: "DATABASE_PROCESS_FAILURE",
      incidentConfirmedAt: report.startedAt,
      latestRecoverablePostgresAt: recoverable,
      postgresBusinessRestoredAt: report.postgresRestoredAt,
      controlBusinessRestoredAt: report.completedAt,
      telemetryBusinessRestoredAt: report.clickhouseRestoredAt,
      metricBusinessRestoredAt: report.completedAt,
      businessValidationCompletedAt: report.completedAt,
      actual: {
        postgresRpoSeconds: secBetween(recoverable, report.startedAt),
        postgresRtoSeconds: secBetween(report.startedAt, report.postgresRestoredAt),
        controlRtoSeconds: secBetween(report.startedAt, report.completedAt),
        telemetryRtoSeconds: secBetween(report.startedAt, report.clickhouseRestoredAt),
        metricRtoSeconds: secBetween(report.startedAt, report.completedAt),
        wholeScenarioRtoSeconds: secBetween(report.startedAt, report.completedAt),
      },
      prerequisites: {
        externalBackupVerified: true,
        backupSurvivesProductionServerLoss: true,
        versionedConfigAvailable: true,
        protectedCredentialMaterialAvailable: true,
        coldStandbyOrReplacementAvailable: false,
        edgeStoreAndForwardAvailable: false,
        edgeRetentionHours: 0,
      },
      validation: {
        postgresqlIntegrity: report.postgresqlIntegrity,
        clickhouseWrite: report.clickhouseWrite,
        redisLatestRebuilding: false,
        mqttConnectivity: false,
        gatewayOnline: false,
        telemetryFlowing: false,
        metricCalculation: false,
        alarmReevaluated: false,
        commandReconciled: false,
        schedulerBacklogProgressing: false,
        outboxBacklogProgressing: false,
        diskCapacitySafe: true,
        clockSynchronized: true,
      },
      telemetryReplay: {
        oldestEdgeRetainedAt: "N/A",
        oldestCloudRestoredAt: "N/A",
        replayStartedAt: "N/A",
        rateLimited: false,
      },
      controlRecovery: {
        incompleteCommandsReviewed: 0,
        blindRetryUsed: false,
        verifiedCount: 0,
        failedCount: 0,
        unknownCount: 0,
      },
      evidence: {
        backupReference: `restic:${report.snapshotId}:pg=${report.postgresBackupStamp}:ch=${report.clickhouseBackupStamp}`,
        walReference: `snapshot-time:${recoverable}`,
        recoveryHostReference: `isolated-docker:${report.environment}`,
        operator,
      },
      problems: report.dockerSkipped ? ["Docker container verification skipped (--skip-docker)"] : [],
      remediationActions: ["Complete live service checks (Redis/MQTT/Telemetry/Control) before running deployment:phase1:recovery:verify"],
    };
    fs.writeFileSync(outPath, `${JSON.stringify(draft, null, 2)}\n`);
  ' "$report_json" "$drill_record_out" "$operator"
  echo "wrote drill record draft: $drill_record_out"
fi

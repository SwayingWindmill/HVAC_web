#!/usr/bin/env bash
# Backfills telemetry_history.numeric_gauge_1min_states and numeric_gauge_15min_states
# from observations recorded before 018-quality-filtered-rollups.sql was applied.
#
#   backfill-quality-filtered-rollups.sh            backfill all (tenant, site, month) batches
#   backfill-quality-filtered-rollups.sh --resume   backfill only months missing in target tables
#   backfill-quality-filtered-rollups.sh --verify   compare 1min and 15min totals against observations
#
# Run backfill / --resume with telemetry-worker stopped (or before exposing 018) to avoid
# double-counting observations concurrently projected by the materialized views.
set -euo pipefail

read -r -a client <<< "${CLICKHOUSE_CLIENT:-docker exec -i hvac-platform-clickhouse-1 clickhouse-client}"
mode="${1:-backfill}"
case "$mode" in
  backfill|--resume|--verify) ;;
  *) echo "usage: $0 [--resume | --verify]" >&2; exit 2 ;;
esac

query() { "${client[@]}" --query "$1" < /dev/null; }

batches="$(query "
  SELECT DISTINCT toString(assumeNotNull(tenant_id)), toString(assumeNotNull(site_id)), toString(toStartOfMonth(sampled_at))
  FROM telemetry_history.observations
  WHERE acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND point_type IN ('TELEMETRY', 'SETTING')
    AND tenant_id IS NOT NULL AND site_id IS NOT NULL AND point_id IS NOT NULL AND point_revision IS NOT NULL
    AND value_type = 'NUMBER' AND value_number IS NOT NULL AND isFinite(value_number)
  ORDER BY 3, 1, 2
  FORMAT TSVRaw")"

if [[ "$mode" == "--verify" ]]; then
  for table_spec in \
    "telemetry_history.numeric_gauge_1min_states|toStartOfMinute(obs.sampled_at)" \
    "telemetry_history.numeric_gauge_15min_states|toStartOfInterval(obs.sampled_at, INTERVAL 15 MINUTE)"; do
    IFS='|' read -r table bucket_expr <<< "$table_spec"
    drift="$(query "
      WITH
        raw AS (
          SELECT assumeNotNull(obs.tenant_id) AS tenant_id, assumeNotNull(obs.site_id) AS site_id,
            toStartOfMonth(obs.sampled_at) AS month, count() AS raw_count
          FROM telemetry_history.observations AS obs
          WHERE obs.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
            AND obs.point_type IN ('TELEMETRY', 'SETTING')
            AND obs.tenant_id IS NOT NULL AND obs.site_id IS NOT NULL AND obs.point_id IS NOT NULL AND obs.point_revision IS NOT NULL
            AND obs.value_type = 'NUMBER' AND obs.value_number IS NOT NULL AND isFinite(obs.value_number)
          GROUP BY tenant_id, site_id, month
        ),
        rollup AS (
          SELECT r.tenant_id AS tenant_id, r.site_id AS site_id, toStartOfMonth(r.bucket) AS month,
            countMerge(r.total_count) AS rollup_count
          FROM $table AS r
          GROUP BY tenant_id, site_id, month
        )
      SELECT toString(tenant_id), toString(site_id), toString(month),
        toString(raw.raw_count), toString(rollup.rollup_count)
      FROM raw FULL OUTER JOIN rollup USING (tenant_id, site_id, month)
      WHERE raw.raw_count IS NULL OR rollup.rollup_count IS NULL OR raw.raw_count != rollup.rollup_count
      ORDER BY 3, 1, 2
      SETTINGS join_use_nulls = 1
      FORMAT TSVRaw")"
    if [[ -n "$drift" ]]; then
      echo "$table drift detected:"
      echo "$drift"
      exit 1
    fi
  done
  echo "quality-filtered numeric rollups match observations for $(grep -c . <<< "$batches") site months"
  exit 0
fi

for table_spec in \
  "telemetry_history.numeric_gauge_1min_states|toStartOfMinute(obs.sampled_at)|1m" \
  "telemetry_history.numeric_gauge_15min_states|toStartOfInterval(obs.sampled_at, INTERVAL 15 MINUTE)|15m"; do
  IFS='|' read -r table bucket_expr tag <<< "$table_spec"
  if [[ "$mode" == "backfill" && "$(query "SELECT count() FROM $table")" != "0" ]]; then
    echo "$table already has rows: use --resume to fill missing months" >&2
    exit 1
  fi
  while IFS=$'\t' read -r tenant site month; do
    [[ -z "$tenant" ]] && continue
    scope="tenant_id = toUUID('$tenant') AND site_id = toUUID('$site')"
    if [[ "$mode" == "--resume" && "$(query "SELECT count() FROM $table WHERE $scope AND toStartOfMonth(bucket) = toDate('$month')")" != "0" ]]; then
      echo "skip $tag $tenant $site $month"
      continue
    fi
    query "
      INSERT INTO $table
      SETTINGS insert_deduplication_token = 'numeric-gauge-$tag-backfill:$tenant:$site:$month'
      SELECT
        $bucket_expr AS bucket,
        assumeNotNull(obs.tenant_id) AS tenant_id,
        assumeNotNull(obs.site_id) AS site_id,
        assumeNotNull(obs.device_id) AS device_id,
        assumeNotNull(obs.point_id) AS point_id,
        assumeNotNull(obs.point_revision) AS point_revision,
        obs.telemetry_key AS telemetry_key,
        assumeNotNull(obs.point_type) AS point_type,
        anyState(obs.unit) AS unit_state,
        maxState(obs.projected_at) AS max_projected_at,
        countIfState(obs.quality = 'GOOD') AS good_count,
        countIfState(obs.quality = 'PARTIAL') AS partial_count,
        countIfState(obs.quality = 'ESTIMATED') AS estimated_count,
        countIfState(obs.quality = 'MANUAL') AS manual_count,
        countIfState(obs.quality = 'STALE') AS stale_count,
        countIfState(obs.quality = 'INVALID') AS invalid_count,
        countState() AS total_count,
        countIfState(obs.quality = 'GOOD') AS valid_count,
        avgIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_avg,
        minIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_min,
        maxIfState(assumeNotNull(obs.value_number), obs.quality = 'GOOD') AS valid_max,
        argMinIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality = 'GOOD') AS valid_first,
        argMaxIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality = 'GOOD') AS valid_last,
        countIfState(obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_count,
        avgIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_avg,
        minIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_min,
        maxIfState(assumeNotNull(obs.value_number), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_max,
        argMinIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_first,
        argMaxIfState(assumeNotNull(obs.value_number), tuple(obs.sampled_at, toString(obs.observation_id)), obs.quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')) AS usable_last
      FROM telemetry_history.observations AS obs
      WHERE obs.tenant_id = toUUID('$tenant') AND obs.site_id = toUUID('$site')
        AND obs.sampled_at >= toDateTime64('$month 00:00:00', 3, 'UTC')
        AND obs.sampled_at < toDateTime64('$month 00:00:00', 3, 'UTC') + INTERVAL 1 MONTH
        AND obs.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
        AND obs.point_type IN ('TELEMETRY', 'SETTING')
        AND obs.device_id IS NOT NULL AND obs.point_id IS NOT NULL AND obs.point_revision IS NOT NULL
        AND obs.value_type = 'NUMBER' AND obs.value_number IS NOT NULL AND isFinite(obs.value_number)
      GROUP BY bucket, tenant_id, site_id, device_id, point_id, point_revision, telemetry_key, point_type"
    echo "filled $tag $tenant $site $month"
  done <<< "$batches"
done

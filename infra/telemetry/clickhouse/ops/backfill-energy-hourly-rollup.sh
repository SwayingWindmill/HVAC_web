#!/usr/bin/env bash
# Fills analytics.energy_hourly_states from the Energy facts that existed before
# 015-energy-hourly-rollup.sql was applied, or checks the rollup against the facts.
#
#   backfill-energy-hourly-rollup.sh            backfill, one (Tenant, Site, month) per insert
#   backfill-energy-hourly-rollup.sh --resume   backfill only the months that have no rollup row yet
#   backfill-energy-hourly-rollup.sh --verify   compare every (Tenant, Site, month) and exit 1 on drift
#
# The backfill must run with the Energy projector (telemetry-worker) stopped, after 015 and before
# the projector starts again: a fact projected while it runs would be counted twice, once by the
# materialized view and once by its month's batch. docs/operations/energy-hourly-rollup.md has
# the whole procedure. --verify may run at any time.
#
# CLICKHOUSE_CLIENT is the client command, split on spaces; the default runs the client inside the
# phase 1 ClickHouse container as its default user, as the other manual ClickHouse steps do.
set -euo pipefail

read -r -a client <<< "${CLICKHOUSE_CLIENT:-docker exec -i hvac-platform-clickhouse-1 clickhouse-client}"
mode="${1:-backfill}"
case "$mode" in
  backfill|--resume|--verify) ;;
  *) echo "usage: $0 [--resume | --verify]" >&2; exit 2 ;;
esac

query() { "${client[@]}" --query "$1"; }

zero="toUUID('00000000-0000-0000-0000-000000000000')"
# One line per (Tenant, Site, month) that has visible facts, oldest month first.
batches="$(query "
  SELECT DISTINCT toString(tenant_id), toString(site_id), toString(toStartOfMonth(period_end))
  FROM analytics.energy_interval_facts
  ORDER BY 3, 1, 2
  FORMAT TSVRaw")"

if [[ "$mode" == "--verify" ]]; then
  # Energy is compared to 1e-6 kWh: the rollup adds the same values in another order.
  drift="$(query "
    WITH
      facts AS (
        SELECT tenant_id, site_id, toStartOfMonth(period_end) AS month,
          sum(energy_kwh) AS energy_kwh, toInt64(count()) AS intervals
        FROM analytics.energy_interval_facts_visible
        GROUP BY tenant_id, site_id, month
      ),
      rollup AS (
        SELECT tenant_id, site_id, toStartOfMonth(hour) AS month,
          sum(energy_kwh) AS energy_kwh, sum(interval_count) AS intervals
        FROM analytics.energy_hourly
        GROUP BY tenant_id, site_id, month
      )
    SELECT toString(tenant_id), toString(site_id), toString(month),
      toString(facts.intervals), toString(rollup.intervals),
      toString(facts.energy_kwh), toString(rollup.energy_kwh)
    FROM facts FULL OUTER JOIN rollup USING (tenant_id, site_id, month)
    WHERE facts.intervals IS NULL OR rollup.intervals IS NULL
      OR facts.intervals != rollup.intervals OR abs(facts.energy_kwh - rollup.energy_kwh) > 1e-6
    ORDER BY 3, 1, 2
    SETTINGS join_use_nulls = 1
    FORMAT TSVRaw")"
  if [[ -n "$drift" ]]; then
    echo "tenant	site	month	fact intervals	rollup intervals	fact kWh	rollup kWh"
    echo "$drift"
    exit 1
  fi
  echo "energy hourly rollup matches the visible facts for $(grep -c . <<< "$batches") Site months"
  exit 0
fi

if [[ "$mode" == "backfill" && "$(query "SELECT count() FROM analytics.energy_hourly_states")" != "0" ]]; then
  echo "analytics.energy_hourly_states already has rows: use --resume to fill only the months without any" >&2
  exit 1
fi

while IFS=$'\t' read -r tenant site month; do
  [[ -z "$tenant" ]] && continue
  scope="tenant_id = toUUID('$tenant') AND site_id = toUUID('$site')"
  if [[ "$mode" == "--resume" && "$(query "SELECT count() FROM analytics.energy_hourly_states WHERE $scope AND toStartOfMonth(hour) = toDate('$month')")" != "0" ]]; then
    echo "skip $tenant $site $month"
    continue
  fi
  # The token makes a retried batch a no-op inside the table's deduplication window.
  query "
    INSERT INTO analytics.energy_hourly_states
    SETTINGS insert_deduplication_token = 'energy-hourly-backfill:$tenant:$site:$month'
    SELECT tenant_id, site_id, point_id, toStartOfHour(period_end) AS hour,
      accounting_scope, energy_type, quality, ifNull(load_device_id, $zero) AS load_device_id,
      sum(energy_kwh), toInt64(count()), max(data_watermark), max(dataset_revision)
    FROM analytics.energy_interval_facts_visible
    WHERE $scope
      AND period_end >= toDateTime64('$month 00:00:00', 3, 'UTC')
      AND period_end < toDateTime64('$month 00:00:00', 3, 'UTC') + INTERVAL 1 MONTH
    GROUP BY tenant_id, site_id, point_id, hour, accounting_scope, energy_type, quality, load_device_id"
  echo "filled $tenant $site $month"
done <<< "$batches"

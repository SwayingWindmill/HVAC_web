-- W1 Energy Fact rebuild semantics: append-only run events and a stable
-- latest-revision read surface for every downstream aggregate.
CREATE TABLE IF NOT EXISTS analytics.energy_rebuild_runs (
  event_id UUID,
  run_id UUID,
  tenant_id UUID,
  site_id UUID,
  scope_type LowCardinality(String),
  meter_binding_id Nullable(UUID),
  window_start Nullable(DateTime64(3, 'UTC')),
  window_end Nullable(DateTime64(3, 'UTC')),
  reason_code LowCardinality(String),
  trigger_ref String,
  event_type LowCardinality(String),
  chunk_cursor String,
  detail String,
  recorded_at DateTime64(3, 'UTC')
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(recorded_at)
ORDER BY (tenant_id, site_id, run_id, recorded_at, event_id)
SETTINGS index_granularity = 8192;

-- One row per logical fact (tenant, site, meter binding, current observation): its highest
-- (fact_revision, projected_at, fact_id). period_end is the current observation's sample time, so it
-- never differs between revisions; grouping by it as well lets a caller's Site and period filters
-- reach the table's index, where ORDER BY ... LIMIT 1 BY made every query read and sort the whole
-- table. The version is packed into one UInt256 because argMax over a tuple key keeps a generic,
-- several times larger state per group.
CREATE VIEW IF NOT EXISTS analytics.energy_interval_facts_visible AS
SELECT
  argMax(fact_id, version) AS fact_id,
  tenant_id,
  site_id,
  argMax(meter_id, version) AS meter_id,
  meter_binding_id,
  argMax(topology_version_id, version) AS topology_version_id,
  argMax(binding_version, version) AS binding_version,
  argMax(energy_type_id, version) AS energy_type_id,
  argMax(meter_role, version) AS meter_role,
  argMax(direction, version) AS direction,
  argMax(accounting_scope, version) AS accounting_scope,
  argMax(load_device_id, version) AS load_device_id,
  argMax(device_id, version) AS device_id,
  argMax(point_id, version) AS point_id,
  argMax(sensor_id, version) AS sensor_id,
  argMax(telemetry_key, version) AS telemetry_key,
  argMax(energy_type, version) AS energy_type,
  argMax(point_revision, version) AS point_revision,
  argMax(unit, version) AS unit,
  argMax(counter_decrease_mode, version) AS counter_decrease_mode,
  argMax(counter_rollover_modulus, version) AS counter_rollover_modulus,
  argMax(period_start, version) AS period_start,
  period_end,
  argMax(energy_kwh, version) AS energy_kwh,
  argMax(transition_type, version) AS transition_type,
  argMax(quality, version) AS quality,
  argMax(quality_reasons, version) AS quality_reasons,
  argMax(source_previous_observation_id, version) AS source_previous_observation_id,
  source_current_observation_id,
  argMax(source_event_id, version) AS source_event_id,
  argMax(source_partition, version) AS source_partition,
  argMax(source_offset, version) AS source_offset,
  argMax(dataset_revision, version) AS dataset_revision,
  argMax(data_watermark, version) AS data_watermark,
  argMax(projected_at, version) AS projected_at,
  argMax(fact_revision, version) AS fact_revision,
  argMax(rebuild_run_id, version) AS rebuild_run_id
FROM (
  SELECT *,
    bitShiftLeft(toUInt256(fact_revision), 192)
      + bitShiftLeft(toUInt256(toUnixTimestamp64Milli(projected_at)), 128)
      + toUInt256(toUInt128(fact_id)) AS version
  FROM analytics.energy_interval_facts
)
GROUP BY tenant_id, site_id, meter_binding_id, period_end, source_current_observation_id;

GRANT SELECT ON analytics.energy_rebuild_runs TO analytics_projector_reader;
GRANT INSERT ON analytics.energy_rebuild_runs TO analytics_projector_writer;
GRANT SELECT ON analytics.energy_rebuild_runs TO cube_analytics_reader;
GRANT SELECT ON analytics.energy_interval_facts_visible TO cube_analytics_reader;

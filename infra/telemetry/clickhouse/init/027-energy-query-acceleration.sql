-- Accelerate Cube energy queries:
-- 1) Separate cross-gap intervals (> 1h) into a small MergeTree table + MVs + visible view so
--    normal hourly/daily queries stop scanning full interval partitions to find a handful of gaps.
-- 2) Add minmax skipping index on addMilliseconds(period_end, -1) and a site-period projection
--    plus a narrow analytics.energy_interval_facts_cube view (14 columns instead of 34).

CREATE TABLE IF NOT EXISTS analytics.energy_cross_gap_facts (
  fact_id UUID,
  tenant_id UUID,
  site_id UUID,
  meter_binding_id UUID,
  accounting_scope LowCardinality(String),
  load_device_id Nullable(UUID),
  point_id UUID,
  energy_type LowCardinality(String),
  period_start DateTime64(3, 'UTC'),
  period_end DateTime64(3, 'UTC'),
  energy_kwh Float64,
  quality LowCardinality(String),
  source_current_observation_id UUID,
  dataset_revision UInt64,
  data_watermark DateTime64(3, 'UTC'),
  projected_at DateTime64(3, 'UTC'),
  fact_revision UInt64 DEFAULT 0
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(period_end)
ORDER BY (
  tenant_id,
  site_id,
  energy_type,
  period_end,
  meter_binding_id,
  fact_revision,
  source_current_observation_id
)
SETTINGS index_granularity = 8192,
         non_replicated_deduplication_window = 10000,
         ttl_only_drop_parts = 1;

ALTER TABLE analytics.energy_cross_gap_facts
  MODIFY TTL toDateTime(period_end) + INTERVAL 1825 DAY DELETE;

DROP VIEW IF EXISTS analytics.energy_facts_to_cross_gap;
CREATE MATERIALIZED VIEW analytics.energy_facts_to_cross_gap
TO analytics.energy_cross_gap_facts
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  fact_id,
  tenant_id,
  site_id,
  meter_binding_id,
  accounting_scope,
  load_device_id,
  point_id,
  energy_type,
  period_start,
  period_end,
  energy_kwh,
  quality,
  source_current_observation_id,
  dataset_revision,
  data_watermark,
  projected_at,
  fact_revision
FROM analytics.energy_interval_facts
WHERE dateDiff('millisecond', period_start, period_end) > 3600000;

DROP VIEW IF EXISTS analytics.energy_fact_corrections_to_cross_gap;
CREATE MATERIALIZED VIEW analytics.energy_fact_corrections_to_cross_gap
TO analytics.energy_cross_gap_facts
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  fact_id,
  tenant_id,
  site_id,
  meter_binding_id,
  accounting_scope,
  load_device_id,
  point_id,
  energy_type,
  period_start,
  period_end,
  energy_kwh,
  quality,
  source_current_observation_id,
  dataset_revision,
  data_watermark,
  projected_at,
  fact_revision
FROM analytics.energy_interval_facts
WHERE fact_revision > 0
  AND dateDiff('millisecond', period_start, period_end) <= 3600000
  AND (tenant_id, site_id, meter_binding_id, period_end, source_current_observation_id) IN (
    SELECT tenant_id, site_id, meter_binding_id, period_end, source_current_observation_id
    FROM analytics.energy_cross_gap_facts
  );

INSERT INTO analytics.energy_cross_gap_facts
SELECT
  fact_id,
  tenant_id,
  site_id,
  meter_binding_id,
  accounting_scope,
  load_device_id,
  point_id,
  energy_type,
  period_start,
  period_end,
  energy_kwh,
  quality,
  source_current_observation_id,
  dataset_revision,
  data_watermark,
  projected_at,
  fact_revision
FROM analytics.energy_interval_facts
WHERE (
  dateDiff('millisecond', period_start, period_end) > 3600000
  OR (
    fact_revision > 0
    AND (tenant_id, site_id, meter_binding_id, period_end, source_current_observation_id) IN (
      SELECT tenant_id, site_id, meter_binding_id, period_end, source_current_observation_id
      FROM analytics.energy_interval_facts
      WHERE dateDiff('millisecond', period_start, period_end) > 3600000
    )
  )
)
AND fact_id NOT IN (
  SELECT fact_id
  FROM analytics.energy_cross_gap_facts
);

CREATE OR REPLACE VIEW analytics.energy_cross_gap_facts_visible
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  tenant_id,
  site_id,
  meter_binding_id,
  cg_accounting_scope AS accounting_scope,
  cg_load_device_id AS load_device_id,
  cg_point_id AS point_id,
  cg_energy_type AS energy_type,
  cg_period_start AS period_start,
  period_end,
  cg_energy_kwh AS energy_kwh,
  cg_quality AS quality,
  source_current_observation_id,
  cg_dataset_revision AS dataset_revision,
  cg_data_watermark AS data_watermark
FROM (
  SELECT
    tenant_id,
    site_id,
    meter_binding_id,
    period_end,
    source_current_observation_id,
    argMax(accounting_scope, ver) AS cg_accounting_scope,
    argMax(load_device_id, ver) AS cg_load_device_id,
    argMax(point_id, ver) AS cg_point_id,
    argMax(energy_type, ver) AS cg_energy_type,
    argMax(period_start, ver) AS cg_period_start,
    argMax(energy_kwh, ver) AS cg_energy_kwh,
    argMax(quality, ver) AS cg_quality,
    argMax(dataset_revision, ver) AS cg_dataset_revision,
    argMax(data_watermark, ver) AS cg_data_watermark
  FROM (
    SELECT
      fact_id,
      tenant_id,
      site_id,
      meter_binding_id,
      accounting_scope,
      load_device_id,
      point_id,
      energy_type,
      period_start,
      period_end,
      energy_kwh,
      quality,
      source_current_observation_id,
      dataset_revision,
      data_watermark,
      bitShiftLeft(toUInt256(fact_revision), 192)
        + bitShiftLeft(toUInt256(toUnixTimestamp64Milli(projected_at)), 128)
        + toUInt256(toUInt128(fact_id)) AS ver
    FROM analytics.energy_cross_gap_facts
  )
  GROUP BY tenant_id, site_id, meter_binding_id, period_end, source_current_observation_id
)
WHERE dateDiff('millisecond', cg_period_start, period_end) > 3600000;

ALTER TABLE analytics.energy_interval_facts
  ADD INDEX IF NOT EXISTS idx_energy_interval_period_end_m1 addMilliseconds(period_end, -1) TYPE minmax GRANULARITY 1;

ALTER TABLE analytics.energy_interval_facts
  MATERIALIZE INDEX idx_energy_interval_period_end_m1 SETTINGS mutations_sync = 1;

ALTER TABLE analytics.energy_interval_facts
  ADD PROJECTION IF NOT EXISTS prj_energy_interval_site_period (
    SELECT
      fact_id,
      tenant_id,
      site_id,
      meter_binding_id,
      accounting_scope,
      load_device_id,
      point_id,
      energy_type,
      period_start,
      period_end,
      energy_kwh,
      quality,
      source_current_observation_id,
      dataset_revision,
      data_watermark,
      projected_at,
      fact_revision
    ORDER BY tenant_id, site_id, energy_type, period_end, meter_binding_id, source_current_observation_id
  );

ALTER TABLE analytics.energy_interval_facts
  MATERIALIZE PROJECTION prj_energy_interval_site_period SETTINGS mutations_sync = 1;

CREATE OR REPLACE VIEW analytics.energy_interval_facts_cube
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  tenant_id,
  site_id,
  meter_binding_id,
  cube_accounting_scope AS accounting_scope,
  cube_load_device_id AS load_device_id,
  cube_point_id AS point_id,
  cube_energy_type AS energy_type,
  cube_period_start AS period_start,
  period_end,
  cube_energy_kwh AS energy_kwh,
  cube_quality AS quality,
  source_current_observation_id,
  cube_dataset_revision AS dataset_revision,
  cube_data_watermark AS data_watermark
FROM (
  SELECT
    tenant_id,
    site_id,
    meter_binding_id,
    period_end,
    source_current_observation_id,
    argMax(accounting_scope, ver) AS cube_accounting_scope,
    argMax(load_device_id, ver) AS cube_load_device_id,
    argMax(point_id, ver) AS cube_point_id,
    argMax(energy_type, ver) AS cube_energy_type,
    argMax(period_start, ver) AS cube_period_start,
    argMax(energy_kwh, ver) AS cube_energy_kwh,
    argMax(quality, ver) AS cube_quality,
    argMax(dataset_revision, ver) AS cube_dataset_revision,
    argMax(data_watermark, ver) AS cube_data_watermark
  FROM (
    SELECT
      fact_id,
      tenant_id,
      site_id,
      meter_binding_id,
      accounting_scope,
      load_device_id,
      point_id,
      energy_type,
      period_start,
      period_end,
      energy_kwh,
      quality,
      source_current_observation_id,
      dataset_revision,
      data_watermark,
      bitShiftLeft(toUInt256(fact_revision), 192)
        + bitShiftLeft(toUInt256(toUnixTimestamp64Milli(projected_at)), 128)
        + toUInt256(toUInt128(fact_id)) AS ver
    FROM analytics.energy_interval_facts
  )
  GROUP BY tenant_id, site_id, meter_binding_id, period_end, source_current_observation_id
);

GRANT SELECT ON analytics.energy_cross_gap_facts TO cube_analytics_reader, analytics_projector_reader, telemetry_query_history_reader;
GRANT INSERT ON analytics.energy_cross_gap_facts TO analytics_projector_writer;
GRANT SELECT, ALTER DELETE ON analytics.energy_cross_gap_facts TO telemetry_retention_writer;
GRANT SELECT ON analytics.energy_cross_gap_facts_visible TO cube_analytics_reader, analytics_projector_reader, telemetry_query_history_reader;
GRANT SELECT ON analytics.energy_interval_facts_cube TO cube_analytics_reader, analytics_projector_reader, telemetry_query_history_reader;

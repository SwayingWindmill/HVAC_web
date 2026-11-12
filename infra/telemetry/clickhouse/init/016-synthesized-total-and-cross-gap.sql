-- Synthesized Site total electricity sub-meter registry (#798) and cross-gap exclusion (> 1 hour)
-- on the hourly Energy rollup materialized views.
--
-- Each change of a Site's synthesized total sub-meter list is written as a new generation, and
-- the sync row that points at it is written last, so a sub-meter list is never half visible.
-- Sites using a physical total meter write synthesized = 0 so site_total_submeters_visible stays
-- empty for them.
CREATE TABLE IF NOT EXISTS analytics.site_total_submeters (
  tenant_id UUID,
  site_id UUID,
  generation UInt64,
  point_id UUID,
  device_id UUID,
  meter_name String,
  ordinal UInt32,
  projected_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(projected_at)
ORDER BY (tenant_id, site_id, generation, ordinal, point_id);

CREATE TABLE IF NOT EXISTS analytics.site_total_submeter_syncs (
  tenant_id UUID,
  site_id UUID,
  generation UInt64,
  synthesized UInt8,
  sources_digest String,
  synced_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(synced_at)
ORDER BY (tenant_id, site_id, generation);

CREATE OR REPLACE VIEW analytics.site_total_submeters_visible AS
SELECT tenant_id, site_id, point_id, device_id, meter_name, ordinal
FROM analytics.site_total_submeters FINAL
WHERE (tenant_id, site_id, generation) IN (
  SELECT tenant_id, site_id, max(generation)
  FROM analytics.site_total_submeter_syncs
  GROUP BY tenant_id, site_id
  HAVING argMax(synthesized, generation) = 1
);

GRANT SELECT ON analytics.site_total_submeters TO analytics_projector_reader;
GRANT SELECT ON analytics.site_total_submeter_syncs TO analytics_projector_reader;
GRANT INSERT, ALTER DELETE ON analytics.site_total_submeters TO analytics_projector_writer;
GRANT INSERT ON analytics.site_total_submeter_syncs TO analytics_projector_writer;
GRANT SELECT ON analytics.site_total_submeters TO cube_analytics_reader;
GRANT SELECT ON analytics.site_total_submeter_syncs TO cube_analytics_reader;
GRANT SELECT ON analytics.site_total_submeters_visible TO cube_analytics_reader;

-- Upgrade existing ClickHouse volumes where 015-energy-hourly-rollup.sql ran before cross-gap
-- intervals (> 1 hour) were excluded from energy_hourly_states.
CREATE OR REPLACE VIEW analytics.energy_interval_fact_revisions
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  tenant_id, site_id, meter_id, meter_binding_id, period_start, period_end, source_current_observation_id, fact_revision,
  point_id, accounting_scope, energy_type, quality, load_device_id, energy_kwh
FROM analytics.energy_interval_facts;

DROP VIEW IF EXISTS analytics.energy_facts_to_hourly;
CREATE MATERIALIZED VIEW analytics.energy_facts_to_hourly
TO analytics.energy_hourly_states
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  tenant_id,
  site_id,
  point_id,
  toStartOfHour(period_end) AS hour,
  accounting_scope,
  energy_type,
  quality,
  ifNull(load_device_id, toUUID('00000000-0000-0000-0000-000000000000')) AS load_device_id,
  energy_kwh,
  toInt64(1) AS interval_count,
  data_watermark,
  dataset_revision
FROM analytics.energy_interval_facts
WHERE dateDiff('millisecond', period_start, period_end) <= 3600000;

DROP VIEW IF EXISTS analytics.energy_fact_corrections_to_hourly;
CREATE MATERIALIZED VIEW analytics.energy_fact_corrections_to_hourly
TO analytics.energy_hourly_states
DEFINER = CURRENT_USER
SQL SECURITY DEFINER AS
SELECT
  prior.tenant_id AS tenant_id,
  prior.site_id AS site_id,
  prior.point_id AS point_id,
  toStartOfHour(prior.period_end) AS hour,
  prior.accounting_scope AS accounting_scope,
  prior.energy_type AS energy_type,
  prior.quality AS quality,
  ifNull(prior.load_device_id, toUUID('00000000-0000-0000-0000-000000000000')) AS load_device_id,
  -prior.energy_kwh AS energy_kwh,
  toInt64(-1) AS interval_count,
  toDateTime64(0, 3, 'UTC') AS data_watermark,
  toUInt64(0) AS dataset_revision
FROM analytics.energy_interval_facts AS correction
INNER JOIN (
  SELECT *
  FROM analytics.energy_interval_fact_revisions
  WHERE (tenant_id, site_id, meter_id, meter_binding_id, period_end) IN (
    SELECT tenant_id, site_id, meter_id, meter_binding_id, period_end
    FROM analytics.energy_interval_facts
    WHERE fact_revision > 0
  )
) AS prior
  ON prior.tenant_id = correction.tenant_id
  AND prior.site_id = correction.site_id
  AND prior.meter_binding_id = correction.meter_binding_id
  AND prior.period_end = correction.period_end
  AND prior.source_current_observation_id = correction.source_current_observation_id
  AND prior.fact_revision + 1 = correction.fact_revision
WHERE correction.fact_revision > 0
  AND dateDiff('millisecond', prior.period_start, prior.period_end) <= 3600000;

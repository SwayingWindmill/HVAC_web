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

CREATE VIEW IF NOT EXISTS analytics.energy_interval_facts_visible AS
SELECT *
FROM analytics.energy_interval_facts
ORDER BY tenant_id, site_id, meter_binding_id, source_current_observation_id,
         fact_revision DESC, projected_at DESC, fact_id DESC
LIMIT 1 BY tenant_id, site_id, meter_binding_id, source_current_observation_id;

GRANT SELECT ON analytics.energy_rebuild_runs TO analytics_projector_reader;
GRANT INSERT ON analytics.energy_rebuild_runs TO analytics_projector_writer;
GRANT SELECT ON analytics.energy_rebuild_runs TO cube_analytics_reader;
GRANT SELECT ON analytics.energy_interval_facts_visible TO cube_analytics_reader;

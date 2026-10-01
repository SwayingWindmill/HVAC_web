-- Energy Slice v1 migration. The ordered schema change removes the obsolete
-- interval contract and installs the explicit v1 physical contract.
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS meter_id UUID DEFAULT toUUID('00000000-0000-0000-0000-000000000000') AFTER site_id;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS meter_binding_id UUID DEFAULT toUUID('00000000-0000-0000-0000-000000000000') AFTER meter_id;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS topology_version_id UUID DEFAULT toUUID('00000000-0000-0000-0000-000000000000') AFTER meter_binding_id;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS binding_version UInt64 DEFAULT 0 AFTER topology_version_id;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS energy_type_id UUID DEFAULT toUUID('00000000-0000-0000-0000-000000000000') AFTER binding_version;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS meter_role LowCardinality(String) DEFAULT '' AFTER energy_type_id;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS direction LowCardinality(String) DEFAULT '' AFTER meter_role;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS point_revision UInt64 DEFAULT 0 AFTER telemetry_key;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS unit LowCardinality(String) DEFAULT '' AFTER point_revision;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS counter_decrease_mode LowCardinality(String) DEFAULT '' AFTER unit;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS counter_rollover_modulus Nullable(Float64) AFTER counter_decrease_mode;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS transition_type LowCardinality(String) DEFAULT '' AFTER energy_kwh;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS source_event_id UUID DEFAULT toUUID('00000000-0000-0000-0000-000000000000') AFTER source_current_observation_id;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS source_partition String DEFAULT '' AFTER source_event_id;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS fact_revision UInt64 DEFAULT 0 AFTER projected_at;
ALTER TABLE analytics.energy_interval_facts
  ADD COLUMN IF NOT EXISTS rebuild_run_id Nullable(UUID) AFTER fact_revision;
ALTER TABLE analytics.energy_interval_facts
  DROP COLUMN IF EXISTS observation_count;
ALTER TABLE analytics.energy_interval_facts
  MODIFY ORDER BY (
    tenant_id,
    site_id,
    meter_id,
    meter_binding_id,
    point_id,
    device_id,
    energy_type,
    period_end,
    fact_revision,
    source_current_observation_id
  );

GRANT SELECT ON telemetry_history.counter_deltas TO analytics_projector_reader;

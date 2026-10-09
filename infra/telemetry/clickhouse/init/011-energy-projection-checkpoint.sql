-- Durable read position of the energy projection in Telemetry History order (#441). The
-- writer advances it after the batch's facts are written, so a lost advance replays one
-- batch, which the existing-fact check makes a no-op. ReplacingMergeTree keeps the latest.
CREATE TABLE IF NOT EXISTS analytics.energy_projection_checkpoints (
  projection_name LowCardinality(String),
  history_sequence UInt64,
  observation_id UUID,
  advanced_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(advanced_at)
ORDER BY projection_name;

GRANT SELECT ON analytics.energy_projection_checkpoints TO analytics_projector_reader;
GRANT INSERT ON analytics.energy_projection_checkpoints TO analytics_projector_writer;
GRANT SELECT ON telemetry_history.counter_arrivals TO analytics_projector_reader;
GRANT SELECT ON telemetry_history.counter_deltas_from TO analytics_projector_reader;

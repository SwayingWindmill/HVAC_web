-- A Site's import Tariff laid out as UTC price windows (#575). ClickHouse cannot read a per-row
-- timezone, so the Energy Processing projector expands each Tariff Version on its own clock and
-- day types in Go; Cube prices every electricity fact at the window its period ends in.
--
-- Each change of a Site's Tariff timeline is written as a new generation, and the sync row that
-- points at it is written last, so a timeline is never half visible. Extending the horizon
-- appends days to the current generation.
CREATE TABLE IF NOT EXISTS analytics.tariff_price_windows (
  tenant_id UUID,
  site_id UUID,
  generation UInt64,
  window_start DateTime64(3, 'UTC'),
  window_end DateTime64(3, 'UTC'),
  period_code LowCardinality(String),
  energy_rate_per_kwh Float64,
  tariff_version_id UUID,
  projected_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(projected_at)
ORDER BY (tenant_id, site_id, generation, window_start);

CREATE TABLE IF NOT EXISTS analytics.tariff_price_window_syncs (
  tenant_id UUID,
  site_id UUID,
  generation UInt64,
  timeline_digest String,
  covered_to DateTime64(3, 'UTC'),
  synced_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(synced_at)
ORDER BY (tenant_id, site_id, generation, covered_to);

CREATE VIEW IF NOT EXISTS analytics.tariff_price_windows_visible AS
SELECT tenant_id, site_id, window_start, window_end, period_code, energy_rate_per_kwh, tariff_version_id
FROM analytics.tariff_price_windows FINAL
WHERE (tenant_id, site_id, generation) IN (
  SELECT tenant_id, site_id, max(generation) FROM analytics.tariff_price_window_syncs GROUP BY tenant_id, site_id
);

GRANT SELECT ON analytics.tariff_price_windows TO analytics_projector_reader;
GRANT SELECT ON analytics.tariff_price_window_syncs TO analytics_projector_reader;
GRANT INSERT, ALTER DELETE ON analytics.tariff_price_windows TO analytics_projector_writer;
GRANT INSERT ON analytics.tariff_price_window_syncs TO analytics_projector_writer;
GRANT SELECT ON analytics.tariff_price_windows TO cube_analytics_reader;
GRANT SELECT ON analytics.tariff_price_window_syncs TO cube_analytics_reader;
GRANT SELECT ON analytics.tariff_price_windows_visible TO cube_analytics_reader;

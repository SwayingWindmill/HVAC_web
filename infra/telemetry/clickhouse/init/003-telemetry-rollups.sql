-- Phase 1 layered telemetry rollups moved to quality-filtered 018-quality-filtered-rollups.sql
-- (numeric_gauge_1min_states and numeric_gauge_15min_states with compatibility views
-- numeric_1min, numeric_15min, and numeric_hourly).
-- Raw authority remains telemetry_history.observations. These layers are rebuildable.

-- Deliberately no canonical daily rollup here. Business-day aggregation must use
-- the Site timezone from Registry; a UTC toStartOfDay bucket is not a V2 business day.
SELECT 1 FORMAT Null;

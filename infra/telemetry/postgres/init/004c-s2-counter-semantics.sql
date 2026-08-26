BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

-- Runtime Point bindings snapshot the effective Counter rule used for each observation.
ALTER TABLE telemetry_runtime.registry_point_bindings
  ADD COLUMN IF NOT EXISTS counter_decrease_mode text,
  ADD COLUMN IF NOT EXISTS counter_rollover_modulus double precision;

ALTER TABLE telemetry_runtime.registry_point_bindings
  DROP CONSTRAINT IF EXISTS registry_point_bindings_counter_semantics_check;
ALTER TABLE telemetry_runtime.registry_point_bindings
  ADD CONSTRAINT registry_point_bindings_counter_semantics_check CHECK (
    (
      point_type <> 'COUNTER'
      AND counter_decrease_mode IS NULL
      AND counter_rollover_modulus IS NULL
    )
    OR
    (
      point_type = 'COUNTER'
      AND value_type = 'NUMBER'
      AND counter_decrease_mode IS NOT NULL
      AND counter_decrease_mode IN ('RESET_TO_ZERO', 'ROLLOVER', 'INVALID')
      AND (
        (counter_decrease_mode = 'ROLLOVER' AND counter_rollover_modulus IS NOT NULL AND counter_rollover_modulus > 0)
        OR
        (counter_decrease_mode <> 'ROLLOVER' AND counter_rollover_modulus IS NULL)
      )
    )
  );

COMMIT;

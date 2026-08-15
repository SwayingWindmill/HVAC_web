BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- Counter semantics are Point-version metadata. A decrease is never guessed at query time.
ALTER TABLE core_registry.telemetry_points
  ADD COLUMN IF NOT EXISTS counter_decrease_mode text,
  ADD COLUMN IF NOT EXISTS counter_rollover_modulus double precision;

ALTER TABLE core_registry.telemetry_points
  DROP CONSTRAINT IF EXISTS telemetry_points_counter_semantics_check;
ALTER TABLE core_registry.telemetry_points
  ADD CONSTRAINT telemetry_points_counter_semantics_check CHECK (
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

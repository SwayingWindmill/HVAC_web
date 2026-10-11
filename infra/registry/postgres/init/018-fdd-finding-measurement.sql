-- An FDD finding states what its evidence measured against the threshold its rule applied,
-- so it reads without fetching the evidence observations.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.fdd_findings
  ADD COLUMN observed_value double precision NOT NULL,
  ADD COLUMN threshold_value double precision NOT NULL,
  ADD COLUMN value_unit text NOT NULL CHECK (length(btrim(value_unit)) BETWEEN 1 AND 32);

COMMIT;

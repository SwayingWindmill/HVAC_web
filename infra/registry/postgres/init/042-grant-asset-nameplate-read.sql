-- Grant least-privilege read access on core_registry.assets.nameplate to FDD and
-- Optimization runtime roles so unit-feeder auxiliary power offsets (feederAuxiliaryPowerKw)
-- are evaluated directly from the authoritative asset nameplate under existing RLS policies.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

GRANT SELECT (nameplate) ON core_registry.assets TO fdd_runtime, optimization_runtime;

RESET ROLE;
COMMIT;

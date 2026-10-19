-- The one-off import of the old system's Site, Asset and Device lists is not needed any more
-- (the legacy registry migrator is gone). Its mapping, provenance and quarantine tables go with it.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

DROP TABLE IF EXISTS core_registry.migration_provenance;
DROP TABLE IF EXISTS core_registry.migration_quarantine;
DROP TABLE IF EXISTS core_registry.legacy_resource_maps;

RESET ROLE;
COMMIT;

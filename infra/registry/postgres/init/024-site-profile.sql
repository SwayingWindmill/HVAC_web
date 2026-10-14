-- Site profile (#485): the floor area a Site's energy is compared per square metre on (GB/T 51161
-- energy intensity is per building floor area), and its thermal design zone (GB 50176). Both are
-- optional; the Registry writer keeps them with the rest of the Site.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.sites
  ADD COLUMN IF NOT EXISTS floor_area_m2 double precision CHECK (floor_area_m2 IS NULL OR floor_area_m2 > 0),
  ADD COLUMN IF NOT EXISTS climate_zone text CHECK (climate_zone IS NULL OR climate_zone IN (
    'SEVERE_COLD', 'COLD', 'HOT_SUMMER_COLD_WINTER', 'HOT_SUMMER_WARM_WINTER', 'MILD'
  ));

RESET ROLE;
COMMIT;

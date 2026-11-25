BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.sites
  ADD COLUMN IF NOT EXISTS building_plan jsonb
    CHECK (building_plan IS NULL OR jsonb_typeof(building_plan) = 'object');

COMMENT ON COLUMN core_registry.sites.building_plan IS '站点数字孪生建筑平面与楼层声明（JSON），为空时三维视图仅展示冷站。';

RESET ROLE;
COMMIT;

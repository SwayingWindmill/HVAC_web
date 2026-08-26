BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- V2 scope guard. Tenant + Site is defined in 007 directly; canonical spatial/Point
-- tables are Tenant-native and may not contain a second platform ownership root.
DO $$
DECLARE
  forbidden_count integer;
  missing_tenant_count integer;
  table_name_value text;
  v2_tables text[] := ARRAY[
    'spaces',
    'asset_space_bindings',
    'device_space_bindings',
    'sensors',
    'sensor_device_bindings',
    'sensor_space_bindings',
    'telemetry_points',
    'point_subject_bindings'
  ];
BEGIN
  SELECT count(*)
    INTO forbidden_count
  FROM information_schema.columns
  WHERE table_schema = 'core_registry'
    AND table_name = ANY (v2_tables)
    AND column_name = 'organization_id';

  IF forbidden_count <> 0 THEN
    RAISE EXCEPTION 'V2 spatial/Point tables must not contain organization_id' USING ERRCODE = '23514';
  END IF;

  FOREACH table_name_value IN ARRAY v2_tables LOOP
    SELECT count(*)
      INTO missing_tenant_count
    FROM information_schema.columns
    WHERE table_schema = 'core_registry'
      AND table_name = table_name_value
      AND column_name IN ('tenant_id', 'site_id');

    IF missing_tenant_count <> 2 THEN
      RAISE EXCEPTION 'V2 table % must contain tenant_id and site_id', table_name_value USING ERRCODE = '23514';
    END IF;
  END LOOP;
END
$$;

RESET ROLE;
COMMIT;

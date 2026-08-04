BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.areas (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  parent_area_id uuid,
  code text NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
  display_name text NOT NULL,
  area_type text NOT NULL CHECK (area_type IN ('CAMPUS', 'BUILDING', 'FLOOR', 'ZONE', 'ROOM', 'PLANT_ROOM', 'ROOFTOP', 'OUTDOOR', 'TENANT_SPACE', 'OTHER')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, site_id, code),
  UNIQUE (organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id) REFERENCES core_registry.sites(organization_id, id),
  FOREIGN KEY (organization_id, site_id, parent_area_id) REFERENCES core_registry.areas(organization_id, site_id, id),
  CHECK (parent_area_id IS NULL OR parent_area_id <> id),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_area_cycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.parent_area_id IS NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.parent_area_id = NEW.id THEN
    RAISE EXCEPTION 'area cannot be its own parent' USING ERRCODE = '23514';
  END IF;
  IF EXISTS (
    WITH RECURSIVE descendants AS (
      SELECT area.id
      FROM core_registry.areas AS area
      WHERE area.parent_area_id = NEW.id
      UNION ALL
      SELECT child.id
      FROM core_registry.areas AS child
      JOIN descendants AS parent ON child.parent_area_id = parent.id
    )
    SELECT 1 FROM descendants WHERE id = NEW.parent_area_id
  ) THEN
    RAISE EXCEPTION 'area hierarchy cycle is not allowed' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS areas_reject_cycle ON core_registry.areas;
CREATE TRIGGER areas_reject_cycle
BEFORE INSERT OR UPDATE OF parent_area_id ON core_registry.areas
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_area_cycle();

CREATE TABLE IF NOT EXISTS core_registry.equipment_area_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  equipment_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(equipment_id)),
  area_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(area_id)),
  binding_role text NOT NULL CHECK (binding_role IN ('INSTALLED_IN', 'SERVES')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, area_id) REFERENCES core_registry.areas(organization_id, site_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS equipment_area_current_installation_uidx
  ON core_registry.equipment_area_bindings (organization_id, site_id, equipment_id)
  WHERE binding_role = 'INSTALLED_IN' AND status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE IF NOT EXISTS core_registry.device_area_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  area_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(area_id)),
  binding_role text NOT NULL CHECK (binding_role IN ('INSTALLED_IN', 'SERVES', 'GATEWAY_FOR', 'SUPERVISES')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (organization_id, site_id, device_id) REFERENCES core_registry.devices(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, area_id) REFERENCES core_registry.areas(organization_id, site_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS device_area_current_installation_uidx
  ON core_registry.device_area_bindings (organization_id, site_id, device_id)
  WHERE binding_role = 'INSTALLED_IN' AND status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE IF NOT EXISTS core_registry.sensors (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  code text NOT NULL,
  display_name text NOT NULL,
  sensor_type text NOT NULL,
  manufacturer text,
  model text,
  serial_number text,
  calibration_due_at timestamptz,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, site_id, code),
  UNIQUE (organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id) REFERENCES core_registry.sites(organization_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.sensor_device_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  sensor_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(sensor_id)),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  binding_role text NOT NULL CHECK (binding_role IN ('REPORTS_THROUGH', 'INDEPENDENT_DEVICE')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (organization_id, site_id, sensor_id) REFERENCES core_registry.sensors(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, device_id) REFERENCES core_registry.devices(organization_id, site_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS sensor_device_current_reporting_uidx
  ON core_registry.sensor_device_bindings (organization_id, site_id, sensor_id)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE IF NOT EXISTS core_registry.sensor_area_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  sensor_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(sensor_id)),
  area_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(area_id)),
  binding_role text NOT NULL CHECK (binding_role IN ('MOUNTED_IN', 'SERVES')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (organization_id, site_id, sensor_id) REFERENCES core_registry.sensors(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, area_id) REFERENCES core_registry.areas(organization_id, site_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS sensor_area_current_mount_uidx
  ON core_registry.sensor_area_bindings (organization_id, site_id, sensor_id)
  WHERE binding_role = 'MOUNTED_IN' AND status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE IF NOT EXISTS core_registry.sensor_subject_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  sensor_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(sensor_id)),
  subject_type text NOT NULL CHECK (subject_type IN ('SITE', 'AREA', 'EQUIPMENT')),
  area_id uuid,
  equipment_id uuid,
  binding_role text NOT NULL CHECK (binding_role IN ('MEASURES', 'VALIDATES', 'CONTRIBUTES_TO')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (organization_id, site_id, sensor_id) REFERENCES core_registry.sensors(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, area_id) REFERENCES core_registry.areas(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(organization_id, site_id, id),
  CHECK ((subject_type = 'SITE' AND area_id IS NULL AND equipment_id IS NULL)
      OR (subject_type = 'AREA' AND area_id IS NOT NULL AND equipment_id IS NULL)
      OR (subject_type = 'EQUIPMENT' AND area_id IS NULL AND equipment_id IS NOT NULL)),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.telemetry_points (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  reporting_device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(reporting_device_id)),
  sensor_id uuid,
  point_key text NOT NULL CHECK (point_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  source_key text NOT NULL CHECK (source_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  display_name text NOT NULL,
  point_kind text NOT NULL CHECK (point_kind IN ('MEASURED', 'CALCULATED', 'STATE', 'COMMAND', 'FEEDBACK')),
  value_type text NOT NULL CHECK (value_type IN ('BOOLEAN', 'NUMBER', 'STRING', 'JSON')),
  unit text,
  writable boolean NOT NULL DEFAULT false,
  sample_interval_ms integer NOT NULL CHECK (sample_interval_ms BETWEEN 100 AND 86400000),
  publish_interval_ms integer NOT NULL CHECK (publish_interval_ms BETWEEN 100 AND 86400000),
  stale_after_ms integer NOT NULL CHECK (stale_after_ms BETWEEN 100 AND 604800000),
  formula_revision text,
  source_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(source_metadata) = 'object'),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (organization_id, site_id, id),
  UNIQUE (organization_id, site_id, reporting_device_id, point_key),
  FOREIGN KEY (organization_id, site_id, reporting_device_id) REFERENCES core_registry.devices(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, sensor_id) REFERENCES core_registry.sensors(organization_id, site_id, id),
  CHECK ((point_kind = 'CALCULATED') = (formula_revision IS NOT NULL)),
  CHECK (writable = (point_kind = 'COMMAND')),
  CHECK (publish_interval_ms >= sample_interval_ms),
  CHECK (stale_after_ms >= publish_interval_ms),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.point_subject_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  point_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(point_id)),
  subject_type text NOT NULL CHECK (subject_type IN ('SITE', 'AREA', 'EQUIPMENT')),
  area_id uuid,
  equipment_id uuid,
  binding_role text NOT NULL CHECK (binding_role IN ('DESCRIBES', 'CONTROLS', 'AGGREGATES')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (organization_id, site_id, point_id) REFERENCES core_registry.telemetry_points(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, area_id) REFERENCES core_registry.areas(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, equipment_id) REFERENCES core_registry.equipment(organization_id, site_id, id),
  CHECK ((subject_type = 'SITE' AND area_id IS NULL AND equipment_id IS NULL)
      OR (subject_type = 'AREA' AND area_id IS NOT NULL AND equipment_id IS NULL)
      OR (subject_type = 'EQUIPMENT' AND area_id IS NULL AND equipment_id IS NOT NULL)),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.calculated_point_inputs (
  organization_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(organization_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  calculated_point_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(calculated_point_id)),
  input_point_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(input_point_id)),
  input_role text NOT NULL,
  ordinal integer NOT NULL CHECK (ordinal >= 0),
  formula_revision text NOT NULL,
  PRIMARY KEY (calculated_point_id, input_point_id, input_role),
  UNIQUE (calculated_point_id, ordinal),
  FOREIGN KEY (organization_id, site_id, calculated_point_id) REFERENCES core_registry.telemetry_points(organization_id, site_id, id),
  FOREIGN KEY (organization_id, site_id, input_point_id) REFERENCES core_registry.telemetry_points(organization_id, site_id, id),
  CHECK (calculated_point_id <> input_point_id)
);

CREATE OR REPLACE FUNCTION core_registry.assert_calculated_point_inputs(target_id uuid)
RETURNS void
LANGUAGE plpgsql
AS $function$
DECLARE
  target_kind text;
  target_formula text;
  input_count integer;
  mismatched_count integer;
BEGIN
  SELECT point_kind, formula_revision INTO target_kind, target_formula
  FROM core_registry.telemetry_points
  WHERE id = target_id;
  IF NOT FOUND THEN
    RETURN;
  END IF;
  SELECT count(*), count(*) FILTER (WHERE formula_revision <> target_formula)
    INTO input_count, mismatched_count
  FROM core_registry.calculated_point_inputs
  WHERE calculated_point_id = target_id;
  IF target_kind = 'CALCULATED' AND input_count = 0 THEN
    RAISE EXCEPTION 'calculated point requires at least one input' USING ERRCODE = '23514';
  END IF;
  IF target_kind <> 'CALCULATED' AND input_count > 0 THEN
    RAISE EXCEPTION 'only calculated points may reference inputs' USING ERRCODE = '23514';
  END IF;
  IF mismatched_count > 0 THEN
    RAISE EXCEPTION 'calculated point input formula revision mismatch' USING ERRCODE = '23514';
  END IF;
END
$function$;

CREATE OR REPLACE FUNCTION core_registry.validate_calculated_point_row()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
  PERFORM core_registry.assert_calculated_point_inputs(NEW.id);
  RETURN NULL;
END
$function$;

CREATE OR REPLACE FUNCTION core_registry.validate_calculated_input_target()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
  PERFORM core_registry.assert_calculated_point_inputs(
    CASE WHEN TG_OP = 'DELETE' THEN OLD.calculated_point_id ELSE NEW.calculated_point_id END
  );
  RETURN NULL;
END
$function$;

DROP TRIGGER IF EXISTS calculated_points_validate_inputs ON core_registry.telemetry_points;
CREATE CONSTRAINT TRIGGER calculated_points_validate_inputs
AFTER INSERT OR UPDATE OF point_kind, formula_revision ON core_registry.telemetry_points
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_calculated_point_row();

DROP TRIGGER IF EXISTS calculated_inputs_validate_target ON core_registry.calculated_point_inputs;
CREATE CONSTRAINT TRIGGER calculated_inputs_validate_target
AFTER INSERT OR UPDATE OR DELETE ON core_registry.calculated_point_inputs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_calculated_input_target();

CREATE INDEX IF NOT EXISTS areas_registry_page_idx
  ON core_registry.areas (organization_id, site_id, parent_area_id, display_name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS equipment_area_bindings_scope_idx
  ON core_registry.equipment_area_bindings (organization_id, site_id, area_id, equipment_id);
CREATE INDEX IF NOT EXISTS device_area_bindings_scope_idx
  ON core_registry.device_area_bindings (organization_id, site_id, area_id, device_id);
CREATE INDEX IF NOT EXISTS sensors_registry_page_idx
  ON core_registry.sensors (organization_id, site_id, display_name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS sensor_subject_bindings_scope_idx
  ON core_registry.sensor_subject_bindings (organization_id, site_id, subject_type, area_id, equipment_id, sensor_id);
CREATE INDEX IF NOT EXISTS telemetry_points_device_key_idx
  ON core_registry.telemetry_points (organization_id, site_id, reporting_device_id, point_key COLLATE "C", id);
CREATE INDEX IF NOT EXISTS point_subject_bindings_scope_idx
  ON core_registry.point_subject_bindings (organization_id, site_id, subject_type, area_id, equipment_id, point_id);

ALTER TABLE core_registry.areas ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.equipment_area_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_area_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensors ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_device_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_area_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_subject_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.telemetry_points ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.point_subject_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.calculated_point_inputs ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.areas FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.equipment_area_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_area_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensors FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_device_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_area_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_subject_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.telemetry_points FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.point_subject_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.calculated_point_inputs FORCE ROW LEVEL SECURITY;

CREATE POLICY areas_runtime_scope ON core_registry.areas
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY equipment_area_bindings_runtime_scope ON core_registry.equipment_area_bindings
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY device_area_bindings_runtime_scope ON core_registry.device_area_bindings
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY sensors_runtime_scope ON core_registry.sensors
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY sensor_device_bindings_runtime_scope ON core_registry.sensor_device_bindings
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY sensor_area_bindings_runtime_scope ON core_registry.sensor_area_bindings
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY sensor_subject_bindings_runtime_scope ON core_registry.sensor_subject_bindings
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY telemetry_points_runtime_scope ON core_registry.telemetry_points
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY point_subject_bindings_runtime_scope ON core_registry.point_subject_bindings
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));
CREATE POLICY calculated_point_inputs_runtime_scope ON core_registry.calculated_point_inputs
  FOR SELECT TO s1_core_runtime
  USING (core_registry.is_authorized_site(organization_id, site_id));

GRANT SELECT ON core_registry.areas, core_registry.equipment_area_bindings,
  core_registry.device_area_bindings, core_registry.sensors,
  core_registry.sensor_device_bindings, core_registry.sensor_area_bindings,
  core_registry.sensor_subject_bindings, core_registry.telemetry_points,
  core_registry.point_subject_bindings, core_registry.calculated_point_inputs TO s1_core_runtime;

REVOKE ALL ON core_registry.areas, core_registry.equipment_area_bindings,
  core_registry.device_area_bindings, core_registry.sensors,
  core_registry.sensor_device_bindings, core_registry.sensor_area_bindings,
  core_registry.sensor_subject_bindings, core_registry.telemetry_points,
  core_registry.point_subject_bindings, core_registry.calculated_point_inputs FROM PUBLIC;

RESET ROLE;
COMMIT;

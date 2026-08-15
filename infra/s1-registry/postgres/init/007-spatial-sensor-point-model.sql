BEGIN;

SET LOCAL ROLE s1_core_migrator;

-- V2 spatial and Point models are scoped directly by Tenant + Site.
-- Organization is intentionally absent from all new canonical data tables.
CREATE TABLE IF NOT EXISTS core_registry.spaces (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  parent_space_id uuid,
  code text NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
  display_name text NOT NULL,
  space_type text NOT NULL CHECK (space_type IN ('CAMPUS', 'BUILDING', 'FLOOR', 'ZONE', 'ROOM', 'PLANT_ROOM', 'ROOFTOP', 'OUTDOOR', 'TENANT_SPACE', 'OTHER')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, code),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, site_id, parent_space_id) REFERENCES core_registry.spaces(tenant_id, site_id, id),
  CHECK (parent_space_id IS NULL OR parent_space_id <> id),
  CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION core_registry.reject_space_cycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.parent_space_id IS NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.parent_space_id = NEW.id THEN
    RAISE EXCEPTION 'space cannot be its own parent' USING ERRCODE = '23514';
  END IF;
  IF EXISTS (
    WITH RECURSIVE descendants AS (
      SELECT space.id
      FROM core_registry.spaces AS space
      WHERE space.tenant_id = NEW.tenant_id
        AND space.site_id = NEW.site_id
        AND space.parent_space_id = NEW.id
      UNION ALL
      SELECT child.id
      FROM core_registry.spaces AS child
      JOIN descendants AS parent ON child.parent_space_id = parent.id
      WHERE child.tenant_id = NEW.tenant_id
        AND child.site_id = NEW.site_id
    )
    SELECT 1 FROM descendants WHERE id = NEW.parent_space_id
  ) THEN
    RAISE EXCEPTION 'space hierarchy cycle is not allowed' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS spaces_reject_cycle ON core_registry.spaces;
CREATE TRIGGER spaces_reject_cycle
BEFORE INSERT OR UPDATE OF tenant_id, site_id, parent_space_id ON core_registry.spaces
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_space_cycle();

CREATE TABLE IF NOT EXISTS core_registry.asset_space_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  asset_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(asset_id)),
  space_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(space_id)),
  binding_role text NOT NULL CHECK (binding_role IN ('INSTALLED_IN', 'SERVES')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, asset_id) REFERENCES core_registry.assets(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, space_id) REFERENCES core_registry.spaces(tenant_id, site_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS asset_space_current_installation_uidx
  ON core_registry.asset_space_bindings (tenant_id, site_id, asset_id)
  WHERE binding_role = 'INSTALLED_IN' AND status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE IF NOT EXISTS core_registry.device_space_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  space_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(space_id)),
  binding_role text NOT NULL CHECK (binding_role IN ('INSTALLED_IN', 'SERVES', 'GATEWAY_FOR', 'SUPERVISES')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, space_id) REFERENCES core_registry.spaces(tenant_id, site_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS device_space_current_installation_uidx
  ON core_registry.device_space_bindings (tenant_id, site_id, device_id)
  WHERE binding_role = 'INSTALLED_IN' AND status = 'ACTIVE' AND valid_to IS NULL;

-- Optional physical probes only. Point remains the canonical data identity.
-- A Sensor row exists only when the real probe needs an independent installation,
-- replacement, calibration, or traceability lifecycle.
CREATE TABLE IF NOT EXISTS core_registry.sensors (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  code text NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
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
  UNIQUE (tenant_id, site_id, code),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (updated_at >= created_at)
);

-- A physical Sensor may report through one Device. It is never required for a Point.
CREATE TABLE IF NOT EXISTS core_registry.sensor_device_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  sensor_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(sensor_id)),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  binding_role text NOT NULL CHECK (binding_role = 'REPORTS_THROUGH'),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, sensor_id) REFERENCES core_registry.sensors(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS sensor_device_current_reporting_uidx
  ON core_registry.sensor_device_bindings (tenant_id, site_id, sensor_id)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

-- Mounting history is physical traceability only. Measurement subject belongs to Point.
CREATE TABLE IF NOT EXISTS core_registry.sensor_space_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  sensor_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(sensor_id)),
  space_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(space_id)),
  binding_role text NOT NULL CHECK (binding_role = 'MOUNTED_IN'),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, sensor_id) REFERENCES core_registry.sensors(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, space_id) REFERENCES core_registry.spaces(tenant_id, site_id, id),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS sensor_space_current_mount_uidx
  ON core_registry.sensor_space_bindings (tenant_id, site_id, sensor_id)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

CREATE TABLE IF NOT EXISTS core_registry.telemetry_points (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  reporting_device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(reporting_device_id)),
  sensor_id uuid,
  point_code text NOT NULL CHECK (point_code ~ '^[a-z][a-z0-9_]{0,127}$'),
  source_key text NOT NULL CHECK (source_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  display_name text NOT NULL,
  point_type text NOT NULL CHECK (point_type IN ('TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND')),
  value_type text NOT NULL CHECK (value_type IN ('BOOLEAN', 'NUMBER', 'STRING', 'JSON')),
  unit text,
  writable boolean NOT NULL DEFAULT false,
  sample_interval_ms integer NOT NULL CHECK (sample_interval_ms BETWEEN 100 AND 86400000),
  publish_interval_ms integer NOT NULL CHECK (publish_interval_ms BETWEEN 100 AND 86400000),
  stale_after_ms integer NOT NULL CHECK (stale_after_ms BETWEEN 100 AND 604800000),
  source_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(source_metadata) = 'object'),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, id),
  UNIQUE (tenant_id, site_id, reporting_device_id, point_code),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, reporting_device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, sensor_id) REFERENCES core_registry.sensors(tenant_id, site_id, id),
  CHECK (point_type <> 'COMMAND' OR writable),
  CHECK (point_type IN ('COMMAND', 'SETTING') OR NOT writable),
  CHECK (publish_interval_ms >= sample_interval_ms),
  CHECK (stale_after_ms >= publish_interval_ms),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.point_subject_bindings (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  point_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(point_id)),
  subject_type text NOT NULL CHECK (subject_type IN ('SITE', 'SPACE', 'ASSET')),
  space_id uuid,
  asset_id uuid,
  binding_role text NOT NULL CHECK (binding_role IN ('DESCRIBES', 'CONTROLS')),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'INACTIVE', 'RETIRED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, point_id) REFERENCES core_registry.telemetry_points(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, space_id) REFERENCES core_registry.spaces(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, asset_id) REFERENCES core_registry.assets(tenant_id, site_id, id),
  CHECK ((subject_type = 'SITE' AND space_id IS NULL AND asset_id IS NULL)
      OR (subject_type = 'SPACE' AND space_id IS NOT NULL AND asset_id IS NULL)
      OR (subject_type = 'ASSET' AND space_id IS NULL AND asset_id IS NOT NULL)),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS spaces_registry_page_idx
  ON core_registry.spaces (tenant_id, site_id, parent_space_id, display_name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS asset_space_bindings_scope_idx
  ON core_registry.asset_space_bindings (tenant_id, site_id, space_id, asset_id);
CREATE INDEX IF NOT EXISTS device_space_bindings_scope_idx
  ON core_registry.device_space_bindings (tenant_id, site_id, space_id, device_id);
CREATE INDEX IF NOT EXISTS sensors_registry_page_idx
  ON core_registry.sensors (tenant_id, site_id, display_name COLLATE "C", id);
CREATE INDEX IF NOT EXISTS telemetry_points_device_key_idx
  ON core_registry.telemetry_points (tenant_id, site_id, reporting_device_id, point_code COLLATE "C", id);
CREATE INDEX IF NOT EXISTS point_subject_bindings_scope_idx
  ON core_registry.point_subject_bindings (tenant_id, site_id, subject_type, space_id, asset_id, point_id);

ALTER TABLE core_registry.spaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.asset_space_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_space_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensors ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_device_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_space_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.telemetry_points ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.point_subject_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.spaces FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.asset_space_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.device_space_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensors FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_device_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.sensor_space_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.telemetry_points FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.point_subject_bindings FORCE ROW LEVEL SECURITY;

CREATE POLICY spaces_runtime_scope ON core_registry.spaces
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY asset_space_bindings_runtime_scope ON core_registry.asset_space_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY device_space_bindings_runtime_scope ON core_registry.device_space_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY sensors_runtime_scope ON core_registry.sensors
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY sensor_device_bindings_runtime_scope ON core_registry.sensor_device_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY sensor_space_bindings_runtime_scope ON core_registry.sensor_space_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY telemetry_points_runtime_scope ON core_registry.telemetry_points
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY point_subject_bindings_runtime_scope ON core_registry.point_subject_bindings
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

GRANT SELECT ON core_registry.spaces, core_registry.asset_space_bindings,
  core_registry.device_space_bindings, core_registry.sensors,
  core_registry.sensor_device_bindings, core_registry.sensor_space_bindings,
  core_registry.telemetry_points, core_registry.point_subject_bindings TO s1_core_runtime;

REVOKE ALL ON core_registry.spaces, core_registry.asset_space_bindings,
  core_registry.device_space_bindings, core_registry.sensors,
  core_registry.sensor_device_bindings, core_registry.sensor_space_bindings,
  core_registry.telemetry_points, core_registry.point_subject_bindings FROM PUBLIC;

RESET ROLE;
COMMIT;

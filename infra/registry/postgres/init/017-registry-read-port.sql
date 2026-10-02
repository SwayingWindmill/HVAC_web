-- Registry read port (ADR 0016): Connectivity reads Gateways, Device Source Keys
-- and Point bindings through these views and never through Registry tables.
BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'core_registry_read_port') THEN
    CREATE ROLE core_registry_read_port NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

SET LOCAL ROLE s1_core_migrator;

-- A Device Source Key names one Device behind one Gateway in that Gateway's messages.
CREATE TABLE core_registry.gateway_device_source_keys (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  gateway_device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(gateway_device_id)),
  source_key text NOT NULL CHECK (source_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  device_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(device_id)),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, site_id, gateway_device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  FOREIGN KEY (tenant_id, site_id, device_id) REFERENCES core_registry.devices(tenant_id, site_id, id),
  CHECK (gateway_device_id <> device_id),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX gateway_device_source_keys_active_key_uidx
  ON core_registry.gateway_device_source_keys (gateway_device_id, source_key)
  WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX gateway_device_source_keys_active_device_uidx
  ON core_registry.gateway_device_source_keys (device_id)
  WHERE status = 'ACTIVE';

-- A Point's source key is how its reporting Device names it on the wire.
CREATE UNIQUE INDEX telemetry_points_active_source_key_uidx
  ON core_registry.telemetry_points (reporting_device_id, source_key)
  WHERE status = 'ACTIVE';

ALTER TABLE core_registry.gateway_device_source_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.gateway_device_source_keys FORCE ROW LEVEL SECURITY;

CREATE POLICY gateway_device_source_keys_runtime_scope ON core_registry.gateway_device_source_keys
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY gateway_device_source_keys_writer_scope ON core_registry.gateway_device_source_keys
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
GRANT SELECT ON core_registry.gateway_device_source_keys TO s1_core_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.gateway_device_source_keys TO s1_core_writer;

-- The read port sees every Gateway (Connectivity resolves a Gateway's Tenant from it)
-- and everything else only inside the session's Tenant.
CREATE POLICY devices_read_port ON core_registry.devices
  FOR SELECT TO core_registry_read_port
  USING (device_type = 'GATEWAY' OR tenant_id = core_registry.current_tenant_id());
CREATE POLICY gateway_device_source_keys_read_port ON core_registry.gateway_device_source_keys
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY telemetry_points_read_port ON core_registry.telemetry_points
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id());
GRANT SELECT ON core_registry.devices, core_registry.gateway_device_source_keys, core_registry.telemetry_points
  TO core_registry_read_port;

CREATE VIEW core_registry.gateway_directory_v1 WITH (security_barrier) AS
SELECT gateway.id AS gateway_id, gateway.tenant_id, gateway.site_id, gateway.status, gateway.revision
FROM core_registry.devices AS gateway
WHERE gateway.device_type = 'GATEWAY';

CREATE VIEW core_registry.gateway_device_source_keys_v1 WITH (security_barrier) AS
SELECT key.tenant_id, key.site_id, key.gateway_device_id AS gateway_id, key.source_key, key.device_id, key.revision
FROM core_registry.gateway_device_source_keys AS key
JOIN core_registry.devices AS gateway
  ON gateway.id = key.gateway_device_id AND gateway.device_type = 'GATEWAY' AND gateway.status = 'ACTIVE'
JOIN core_registry.devices AS device
  ON device.id = key.device_id AND device.status = 'ACTIVE'
WHERE key.status = 'ACTIVE';

CREATE VIEW core_registry.point_bindings_v1 WITH (security_barrier) AS
SELECT point.tenant_id, point.site_id, point.reporting_device_id AS device_id, point.source_key,
       point.id AS point_id, point.point_code, point.point_type, point.value_type, point.unit,
       point.writable, point.revision AS point_revision
FROM core_registry.telemetry_points AS point
JOIN core_registry.devices AS device
  ON device.id = point.reporting_device_id AND device.status = 'ACTIVE'
WHERE point.status = 'ACTIVE';

RESET ROLE;

ALTER VIEW core_registry.gateway_directory_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.gateway_device_source_keys_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.point_bindings_v1 OWNER TO core_registry_read_port;

GRANT USAGE ON SCHEMA core_registry TO connectivity_runtime;
GRANT SELECT ON core_registry.gateway_directory_v1, core_registry.gateway_device_source_keys_v1,
  core_registry.point_bindings_v1 TO connectivity_runtime;

COMMIT;

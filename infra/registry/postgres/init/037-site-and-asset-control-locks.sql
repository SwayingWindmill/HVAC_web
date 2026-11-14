-- Site control mode (REMOTE_AUTO / MANUAL_OVERRIDE / SAFE_SHUTDOWN_EXIT) and per-asset
-- control locks (UNLOCKED / MANUAL_LOCK / MAINTENANCE_LOCK) with tenant/site RLS and
-- read-port views for Connectivity & Optimization enforcement.
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

CREATE TABLE core_registry.site_control_states (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  mode text NOT NULL CHECK (mode IN ('REMOTE_AUTO', 'MANUAL_OVERRIDE', 'SAFE_SHUTDOWN_EXIT')),
  reason text NOT NULL DEFAULT '' CHECK (length(reason) <= 1000),
  updated_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(updated_by)),
  revision bigint NOT NULL CHECK (revision >= 1),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE core_registry.asset_control_locks (
  id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(site_id)),
  asset_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(asset_id)),
  lock_mode text NOT NULL CHECK (lock_mode IN ('UNLOCKED', 'MANUAL_LOCK', 'MAINTENANCE_LOCK')),
  reason text NOT NULL DEFAULT '' CHECK (length(reason) <= 1000),
  updated_by uuid NOT NULL CHECK (core_registry.is_uuid_v7(updated_by)),
  revision bigint NOT NULL CHECK (revision >= 1),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, site_id, asset_id),
  UNIQUE (tenant_id, site_id, id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  FOREIGN KEY (tenant_id, site_id, asset_id) REFERENCES core_registry.assets(tenant_id, site_id, id),
  CHECK (updated_at >= created_at)
);

CREATE INDEX asset_control_locks_site_mode_idx
  ON core_registry.asset_control_locks (tenant_id, site_id, lock_mode, asset_id);

ALTER TABLE core_registry.registry_audit_facts
  DROP CONSTRAINT IF EXISTS registry_audit_facts_resource_type_check;
ALTER TABLE core_registry.registry_audit_facts
  ADD CONSTRAINT registry_audit_facts_resource_type_check
  CHECK (resource_type IN (
    'SITE', 'SPACE', 'ASSET', 'DEVICE', 'SENSOR', 'POINT', 'BINDING',
    'TEMPLATE_REVISION', 'TEMPLATE_ASSIGNMENT', 'IMPORT', 'RETIREMENT',
    'OPERATING_SCHEDULE', 'OPERATING_EXCEPTION',
    'MONTHLY_ELECTRICITY_BUDGET', 'UTILITY_BILL',
    'SITE_CONTROL_STATE', 'ASSET_CONTROL_LOCK'
  ));

ALTER TABLE core_registry.site_control_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.site_control_states FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.asset_control_locks ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.asset_control_locks FORCE ROW LEVEL SECURITY;

CREATE POLICY site_control_states_migrator_all ON core_registry.site_control_states
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);
CREATE POLICY asset_control_locks_migrator_all ON core_registry.asset_control_locks
  FOR ALL TO s1_core_migrator USING (true) WITH CHECK (true);

CREATE POLICY site_control_states_runtime_scope ON core_registry.site_control_states
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY asset_control_locks_runtime_scope ON core_registry.asset_control_locks
  FOR SELECT TO s1_core_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY site_control_states_writer_scope ON core_registry.site_control_states
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY asset_control_locks_writer_scope ON core_registry.asset_control_locks
  FOR ALL TO s1_core_writer
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id))
  WITH CHECK (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY site_control_states_optimization_scope ON core_registry.site_control_states
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));
CREATE POLICY asset_control_locks_optimization_scope ON core_registry.asset_control_locks
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

CREATE POLICY site_control_states_read_port ON core_registry.site_control_states
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY asset_control_locks_read_port ON core_registry.asset_control_locks
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id());
CREATE POLICY device_bindings_read_port ON core_registry.device_bindings
  FOR SELECT TO core_registry_read_port
  USING (tenant_id = core_registry.current_tenant_id());

REVOKE ALL ON core_registry.site_control_states, core_registry.asset_control_locks FROM PUBLIC;
GRANT SELECT ON core_registry.site_control_states, core_registry.asset_control_locks TO s1_core_runtime, optimization_runtime, core_registry_read_port;
GRANT SELECT, INSERT, UPDATE ON core_registry.site_control_states, core_registry.asset_control_locks TO s1_core_writer;
GRANT SELECT (tenant_id, site_id, device_id, asset_id, status, valid_from, valid_to)
  ON core_registry.device_bindings TO core_registry_read_port;

CREATE VIEW core_registry.site_control_states_v1 WITH (security_barrier) AS
SELECT tenant_id, site_id, mode, revision, updated_at
FROM core_registry.site_control_states;

CREATE VIEW core_registry.device_control_locks_v1 WITH (security_barrier) AS
SELECT DISTINCT binding.tenant_id, binding.site_id, binding.device_id, lock.asset_id, lock.lock_mode, lock.revision
FROM core_registry.device_bindings AS binding
JOIN core_registry.asset_control_locks AS lock
  ON lock.tenant_id = binding.tenant_id
 AND lock.site_id = binding.site_id
 AND lock.asset_id = binding.asset_id
WHERE binding.status = 'ACTIVE'
  AND binding.valid_to IS NULL
  AND lock.lock_mode <> 'UNLOCKED';

RESET ROLE;

ALTER VIEW core_registry.site_control_states_v1 OWNER TO core_registry_read_port;
ALTER VIEW core_registry.device_control_locks_v1 OWNER TO core_registry_read_port;
GRANT SELECT ON core_registry.site_control_states_v1, core_registry.device_control_locks_v1 TO connectivity_runtime;

COMMIT;

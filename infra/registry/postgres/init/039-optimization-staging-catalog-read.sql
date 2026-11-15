-- Optimization Staging Evaluator reads site asset, device binding, command point metadata,
-- and interlock graph definitions under tenant/site RLS without write grants.
BEGIN;

SET LOCAL ROLE s1_core_migrator;

DROP POLICY IF EXISTS assets_optimization_scope ON core_registry.assets;
CREATE POLICY assets_optimization_scope ON core_registry.assets
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS devices_optimization_scope ON core_registry.devices;
CREATE POLICY devices_optimization_scope ON core_registry.devices
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS gateway_device_source_keys_optimization_scope ON core_registry.gateway_device_source_keys;
CREATE POLICY gateway_device_source_keys_optimization_scope ON core_registry.gateway_device_source_keys
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS device_bindings_optimization_scope ON core_registry.device_bindings;
CREATE POLICY device_bindings_optimization_scope ON core_registry.device_bindings
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS telemetry_points_optimization_scope ON core_registry.telemetry_points;
CREATE POLICY telemetry_points_optimization_scope ON core_registry.telemetry_points
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

DROP POLICY IF EXISTS point_subject_bindings_optimization_scope ON core_registry.point_subject_bindings;
CREATE POLICY point_subject_bindings_optimization_scope ON core_registry.point_subject_bindings
  FOR SELECT TO optimization_runtime
  USING (tenant_id = core_registry.current_tenant_id() AND core_registry.is_authorized_site(site_id));

GRANT SELECT (tenant_id, site_id, id, code, display_name, asset_type, status, rated_power, rated_capacity)
  ON core_registry.assets TO optimization_runtime;
GRANT SELECT (tenant_id, site_id, id, code, display_name, device_type, status)
  ON core_registry.devices TO optimization_runtime;
GRANT SELECT (tenant_id, site_id, device_id, source_key, status)
  ON core_registry.gateway_device_source_keys TO optimization_runtime;
GRANT SELECT (tenant_id, site_id, asset_id, device_id, binding_role, status, valid_from, valid_to)
  ON core_registry.device_bindings TO optimization_runtime;
GRANT SELECT (tenant_id, site_id, id, reporting_device_id, point_code, source_key, point_type, writable, source_metadata, status)
  ON core_registry.telemetry_points TO optimization_runtime;
GRANT SELECT (tenant_id, site_id, point_id, subject_type, asset_id, binding_role, status, valid_from, valid_to)
  ON core_registry.point_subject_bindings TO optimization_runtime;

RESET ROLE;
COMMIT;

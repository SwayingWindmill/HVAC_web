BEGIN;

SET LOCAL ROLE s1_core_migrator;

DROP INDEX IF EXISTS core_registry.device_bindings_active_device_role_uidx;

ALTER TABLE core_registry.device_bindings
  DROP CONSTRAINT IF EXISTS device_bindings_role_check;
ALTER TABLE core_registry.device_bindings
  ADD CONSTRAINT device_bindings_role_check
  CHECK (binding_role IN ('CONTROLLER', 'METER', 'SENSOR', 'GATEWAY', 'SUPERVISORY_CONTROLLER'));

CREATE UNIQUE INDEX device_bindings_active_relation_uidx
  ON core_registry.device_bindings
  (tenant_id, site_id, device_id, asset_id, binding_role)
  WHERE status = 'ACTIVE' AND valid_to IS NULL;

RESET ROLE;
COMMIT;

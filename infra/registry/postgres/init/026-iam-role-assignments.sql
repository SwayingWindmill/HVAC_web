-- ADR 0022: a Role Assignment (iam.role_bindings) of a Role (iam.role_templates) to the whole
-- Tenant or to one Site is the only authorization fact. Built-in Roles are global rows; the
-- per-domain permission tables are dropped (spec docs/specs/iam-roles-and-users.md).
\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_iam_migrator;

DROP TABLE iam.site_bindings, iam.telemetry_scope_bindings, iam.telemetry_key_bindings,
  iam.alarm_permissions, iam.work_order_permissions, iam.command_permissions CASCADE;
DROP FUNCTION iam.emit_scope_telemetry_revocation(), iam.emit_key_telemetry_revocation(),
  iam.emit_site_binding_telemetry_revocation();

ALTER TABLE iam.reconciliation_events DROP COLUMN site_binding_count;

ALTER TABLE iam.telemetry_revocation_facts DROP CONSTRAINT telemetry_revocation_facts_source_type_check;
ALTER TABLE iam.telemetry_revocation_facts ADD CONSTRAINT telemetry_revocation_facts_source_type_check
  CHECK (source_type IN ('MEMBERSHIP', 'ROLE_BINDING', 'GRANT')) NOT VALID;

-- A Role with no Tenant is built in: shipped here, readable by every Tenant, editable by none.
ALTER TABLE iam.role_templates ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE iam.role_templates DROP CONSTRAINT role_templates_tenant_id_role_key_key;
ALTER TABLE iam.role_templates ADD CONSTRAINT role_templates_tenant_id_role_key_key
  UNIQUE NULLS NOT DISTINCT (tenant_id, role_key);

-- A Role Assignment without a Site covers the whole Tenant, including Sites added later.
ALTER TABLE iam.role_bindings
  ADD COLUMN site_id uuid CHECK (site_id IS NULL OR iam.is_uuid_v7(site_id));
DROP INDEX iam.role_bindings_principal_template_idx;
CREATE UNIQUE INDEX role_bindings_principal_template_idx
  ON iam.role_bindings (tenant_id, principal_id, role_template_id, site_id) NULLS NOT DISTINCT;

-- Earlier migrations updated role_templates as the migrator without a policy, which FORCE RLS
-- silently turned into no-ops.
CREATE POLICY role_templates_migrator_all ON iam.role_templates
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
DROP POLICY role_templates_runtime_read ON iam.role_templates;
CREATE POLICY role_templates_runtime_read ON iam.role_templates
  FOR SELECT TO s1_iam_runtime
  USING (tenant_id IS NULL OR tenant_id = iam.current_tenant_id());
CREATE POLICY role_templates_admin_builtin_read ON iam.role_templates
  FOR SELECT TO s1_iam_admin
  USING (tenant_id IS NULL);

CREATE OR REPLACE FUNCTION iam.emit_role_binding_telemetry_revocation()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $role_revocation$
DECLARE
  row_value iam.role_bindings%ROWTYPE;
BEGIN
  row_value := OLD;
  IF TG_OP <> 'DELETE' THEN
    IF NOT (
      NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
      OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
      OR NEW.role_template_id IS DISTINCT FROM OLD.role_template_id
      OR NEW.site_id IS DISTINCT FROM OLD.site_id
      OR NEW.status IS DISTINCT FROM OLD.status
      OR NEW.valid_from IS DISTINCT FROM OLD.valid_from
      OR NEW.valid_to IS DISTINCT FROM OLD.valid_to
    ) THEN
      RETURN row_value;
    END IF;
  END IF;
  INSERT INTO iam.telemetry_revocation_facts
    (tenant_id, principal_id, source_type, source_id, action, policy_revision, reason_code, occurred_at)
  VALUES
    (row_value.tenant_id, row_value.principal_id, 'ROLE_BINDING', row_value.id, 'telemetry.*',
     COALESCE(iam.active_telemetry_policy_revision(row_value.tenant_id), 'telemetry-access:unavailable'),
     'ROLE_BINDING_CHANGED', clock_timestamp());
  RETURN row_value;
END
$role_revocation$;

UPDATE iam.capability_catalog_revisions SET status = 'RETIRED' WHERE status = 'ACTIVE';
INSERT INTO iam.capability_catalog_revisions (revision, catalog_key, capabilities, status, created_at)
VALUES (6, 's26-role-assignments-v1', ARRAY[
  'registry.read', 'site.list', 'site.read', 'asset.list', 'asset.read', 'device.list', 'device.read',
  'device-binding.list', 'asset-model.read', 'tariff.read',
  'site.write', 'space.write', 'asset.write', 'device.write', 'sensor.write', 'point.write',
  'binding.write', 'template.manage', 'registry.import', 'registry.retire',
  'telemetry.snapshot.read', 'telemetry.batch.read', 'telemetry.subscribe', 'telemetry.history.read',
  'telemetry.resubscribe', 'telemetry.recovery.use', 'telemetry.recovery.checkpoint',
  'analytics.energy-series.read', 'alarm:read', 'alarm:ack', 'alarm:assign',
  'work-order:list', 'work-order:read', 'work-order:create', 'work-order:assign', 'work-order:plan',
  'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete', 'work-order:cancel', 'work-order:reopen',
  'savings.manage', 'rule.manage', 'session.revoke', 'audit.read', 'iam.admin', 'api-credential.manage',
  'command.submit.low', 'command.submit.medium', 'command.submit.high', 'command.submit.critical',
  'command.approve.low', 'command.approve.medium', 'command.approve.high', 'command.approve.critical'
], 'ACTIVE', '2026-10-14T00:00:00Z');

-- The four Built-in Roles. Each list is complete: a higher role repeats what it shares with a
-- lower one, and a command capability names the highest risk it allows.
INSERT INTO iam.role_templates (id, tenant_id, role_key, display_name, capabilities, status, revision, created_at, updated_at)
VALUES
  ('01a00260-0000-7000-8000-000000000001', NULL, 'viewer', '只读', ARRAY[
    'registry.read',
    'telemetry.snapshot.read', 'telemetry.batch.read', 'telemetry.subscribe', 'telemetry.history.read',
    'telemetry.resubscribe', 'telemetry.recovery.use', 'telemetry.recovery.checkpoint',
    'analytics.energy-series.read', 'alarm:read', 'work-order:list', 'work-order:read'
  ], 'ACTIVE', 1, '2026-10-14T00:00:00Z', '2026-10-14T00:00:00Z'),
  ('01a00260-0000-7000-8000-000000000002', NULL, 'operator', '运维人员', ARRAY[
    'registry.read',
    'telemetry.snapshot.read', 'telemetry.batch.read', 'telemetry.subscribe', 'telemetry.history.read',
    'telemetry.resubscribe', 'telemetry.recovery.use', 'telemetry.recovery.checkpoint',
    'analytics.energy-series.read', 'alarm:read', 'work-order:list', 'work-order:read',
    'alarm:ack', 'work-order:create', 'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete',
    'command.submit.medium'
  ], 'ACTIVE', 1, '2026-10-14T00:00:00Z', '2026-10-14T00:00:00Z'),
  ('01a00260-0000-7000-8000-000000000003', NULL, 'site-manager', '运维主管', ARRAY[
    'registry.read',
    'telemetry.snapshot.read', 'telemetry.batch.read', 'telemetry.subscribe', 'telemetry.history.read',
    'telemetry.resubscribe', 'telemetry.recovery.use', 'telemetry.recovery.checkpoint',
    'analytics.energy-series.read', 'alarm:read', 'work-order:list', 'work-order:read',
    'alarm:ack', 'work-order:create', 'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete',
    'alarm:assign', 'work-order:assign', 'work-order:plan', 'work-order:cancel', 'work-order:reopen',
    'savings.manage', 'command.submit.high', 'command.approve.high'
  ], 'ACTIVE', 1, '2026-10-14T00:00:00Z', '2026-10-14T00:00:00Z'),
  ('01a00260-0000-7000-8000-000000000004', NULL, 'admin', '管理员', ARRAY[
    'registry.read',
    'telemetry.snapshot.read', 'telemetry.batch.read', 'telemetry.subscribe', 'telemetry.history.read',
    'telemetry.resubscribe', 'telemetry.recovery.use', 'telemetry.recovery.checkpoint',
    'analytics.energy-series.read', 'alarm:read', 'work-order:list', 'work-order:read',
    'alarm:ack', 'work-order:create', 'work-order:start', 'work-order:block', 'work-order:resume', 'work-order:complete',
    'alarm:assign', 'work-order:assign', 'work-order:plan', 'work-order:cancel', 'work-order:reopen',
    'savings.manage', 'command.submit.critical', 'command.approve.critical',
    'site.write', 'space.write', 'asset.write', 'device.write', 'sensor.write', 'point.write',
    'binding.write', 'template.manage', 'registry.import', 'registry.retire',
    'iam.admin', 'api-credential.manage', 'session.revoke', 'audit.read', 'rule.manage'
  ], 'ACTIVE', 1, '2026-10-14T00:00:00Z', '2026-10-14T00:00:00Z');

RESET ROLE;

COMMIT;

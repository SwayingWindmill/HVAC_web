BEGIN;
SET LOCAL ROLE s3_command_migrator;

-- Tenant cannot be inferred safely from an Organization UUID inside the isolated
-- Command database. Refuse an in-place contract migration when legacy command
-- rows exist; an operator must provide an explicit Organization -> Tenant
-- backfill before applying this migration.
DO $tenant_backfill_guard$
BEGIN
  IF EXISTS (SELECT 1 FROM command_runtime.command_intents LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.device_control_state LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.command_idempotency LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.command_authorization_snapshots LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.command_risk_snapshots LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.command_approval_snapshots LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.command_attempts LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.command_transitions LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.command_dispatch_outbox LIMIT 1)
     OR EXISTS (SELECT 1 FROM command_runtime.command_audit_intents LIMIT 1) THEN
    RAISE EXCEPTION 'S3 tenant scope migration requires an explicit Organization-to-Tenant backfill for existing Command rows';
  END IF;
END
$tenant_backfill_guard$;

CREATE TABLE IF NOT EXISTS command_runtime.organization_tenant_scope (
  organization_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (updated_at >= created_at)
);

ALTER TABLE command_runtime.device_control_state ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_intents ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_idempotency ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_authorization_snapshots ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_risk_snapshots ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_approval_snapshots ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_attempts ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_transitions ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_dispatch_outbox ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE command_runtime.command_audit_intents ADD COLUMN tenant_id uuid NOT NULL;

CREATE OR REPLACE FUNCTION command_runtime.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$$;

CREATE OR REPLACE FUNCTION command_runtime.enforce_write_tenant_scope()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  active_tenant uuid;
  active_organization uuid;
BEGIN
  active_tenant := command_runtime.current_tenant_id();
  active_organization := NULLIF(current_setting('app.organization_id', true), '')::uuid;
  IF active_tenant IS NULL OR active_organization IS NULL THEN
    RAISE EXCEPTION 'command tenant and organization context are required' USING ERRCODE = '42501';
  END IF;
  IF NEW.organization_id <> active_organization THEN
    RAISE EXCEPTION 'command organization context mismatch' USING ERRCODE = '42501';
  END IF;
  IF NEW.tenant_id IS NULL THEN
    NEW.tenant_id := active_tenant;
  ELSIF NEW.tenant_id <> active_tenant THEN
    RAISE EXCEPTION 'command tenant context mismatch' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;

DO $install_tenant_triggers$
DECLARE
  relation_name text;
BEGIN
  FOREACH relation_name IN ARRAY ARRAY[
    'device_control_state',
    'command_intents',
    'command_idempotency',
    'command_authorization_snapshots',
    'command_risk_snapshots',
    'command_approval_snapshots',
    'command_attempts',
    'command_transitions',
    'command_dispatch_outbox',
    'command_audit_intents'
  ]
  LOOP
    EXECUTE format('DROP TRIGGER IF EXISTS %I ON command_runtime.%I', relation_name || '_tenant_scope', relation_name);
    EXECUTE format(
      'CREATE TRIGGER %I BEFORE INSERT OR UPDATE OF organization_id, tenant_id ON command_runtime.%I FOR EACH ROW EXECUTE FUNCTION command_runtime.enforce_write_tenant_scope()',
      relation_name || '_tenant_scope', relation_name
    );
  END LOOP;
END
$install_tenant_triggers$;

ALTER TABLE command_runtime.organization_tenant_scope ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.organization_tenant_scope FORCE ROW LEVEL SECURITY;

CREATE POLICY organization_tenant_scope_migrator_all
  ON command_runtime.organization_tenant_scope FOR ALL TO s3_command_migrator
  USING (true) WITH CHECK (true);
CREATE POLICY organization_tenant_scope_runtime_select
  ON command_runtime.organization_tenant_scope FOR SELECT TO s3_command_runtime
  USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
CREATE POLICY organization_tenant_scope_runtime_insert
  ON command_runtime.organization_tenant_scope FOR INSERT TO s3_command_runtime
  WITH CHECK (
    organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
    AND tenant_id = command_runtime.current_tenant_id()
  );

DO $tenant_runtime_policies$
DECLARE
  relation_name text;
  policy_name text;
BEGIN
  FOREACH relation_name IN ARRAY ARRAY[
    'device_control_state',
    'command_intents',
    'command_idempotency',
    'command_authorization_snapshots',
    'command_risk_snapshots',
    'command_approval_snapshots',
    'command_attempts',
    'command_transitions',
    'command_dispatch_outbox',
    'command_audit_intents'
  ]
  LOOP
    policy_name := relation_name || '_runtime_org';
    EXECUTE format('DROP POLICY IF EXISTS %I ON command_runtime.%I', policy_name, relation_name);
    EXECUTE format(
      'CREATE POLICY %I ON command_runtime.%I FOR ALL TO s3_command_runtime USING (tenant_id = command_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting(''app.organization_id'', true), '''')::uuid) WITH CHECK (tenant_id = command_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting(''app.organization_id'', true), '''')::uuid)',
      policy_name, relation_name
    );
  END LOOP;
END
$tenant_runtime_policies$;

CREATE INDEX command_intents_tenant_device_lane_idx
  ON command_runtime.command_intents (tenant_id, organization_id, device_id, device_command_sequence, status);
CREATE INDEX command_dispatch_outbox_tenant_ready_idx
  ON command_runtime.command_dispatch_outbox (tenant_id, organization_id, available_at, created_at)
  WHERE delivered_at IS NULL;
CREATE INDEX command_audit_intents_tenant_created_idx
  ON command_runtime.command_audit_intents (tenant_id, organization_id, created_at, audit_intent_id);

REVOKE ALL ON command_runtime.organization_tenant_scope FROM PUBLIC;
GRANT SELECT, INSERT ON command_runtime.organization_tenant_scope TO s3_command_runtime;
GRANT EXECUTE ON FUNCTION command_runtime.current_tenant_id() TO s3_command_runtime;
GRANT EXECUTE ON FUNCTION command_runtime.enforce_write_tenant_scope() TO s3_command_runtime;

RESET ROLE;
COMMIT;

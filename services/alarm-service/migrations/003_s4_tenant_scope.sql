BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

DROP POLICY IF EXISTS alarm_current_migrator_all ON alarm_runtime.alarm_current;
CREATE POLICY alarm_current_migrator_all ON alarm_runtime.alarm_current
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS alarm_idempotency_migrator_all ON alarm_runtime.alarm_idempotency;
CREATE POLICY alarm_idempotency_migrator_all ON alarm_runtime.alarm_idempotency
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);

DO $tenant_backfill_guard$
BEGIN
  IF EXISTS (SELECT 1 FROM alarm_runtime.alarm_current LIMIT 1)
     OR EXISTS (SELECT 1 FROM alarm_runtime.alarm_idempotency LIMIT 1) THEN
    RAISE EXCEPTION 'S4 tenant scope migration requires an explicit Organization-to-Tenant backfill for existing Alarm rows';
  END IF;
END
$tenant_backfill_guard$;

CREATE TABLE IF NOT EXISTS alarm_runtime.organization_tenant_scope (
  organization_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (updated_at >= created_at)
);

ALTER TABLE alarm_runtime.alarm_current ADD COLUMN tenant_id uuid NOT NULL;
ALTER TABLE alarm_runtime.alarm_idempotency ADD COLUMN tenant_id uuid NOT NULL;

CREATE OR REPLACE FUNCTION alarm_runtime.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$$;

CREATE OR REPLACE FUNCTION alarm_runtime.enforce_write_tenant_scope()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  active_tenant uuid;
  active_organization uuid;
BEGIN
  active_tenant := alarm_runtime.current_tenant_id();
  active_organization := NULLIF(current_setting('app.organization_id', true), '')::uuid;
  IF active_tenant IS NULL OR active_organization IS NULL THEN
    RAISE EXCEPTION 'alarm tenant and organization context are required' USING ERRCODE = '42501';
  END IF;
  IF NEW.organization_id <> active_organization THEN
    RAISE EXCEPTION 'alarm organization context mismatch' USING ERRCODE = '42501';
  END IF;
  IF NEW.tenant_id IS NULL THEN
    NEW.tenant_id := active_tenant;
  ELSIF NEW.tenant_id <> active_tenant THEN
    RAISE EXCEPTION 'alarm tenant context mismatch' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS alarm_current_tenant_scope ON alarm_runtime.alarm_current;
CREATE TRIGGER alarm_current_tenant_scope
  BEFORE INSERT OR UPDATE OF organization_id, tenant_id ON alarm_runtime.alarm_current
  FOR EACH ROW EXECUTE FUNCTION alarm_runtime.enforce_write_tenant_scope();
DROP TRIGGER IF EXISTS alarm_idempotency_tenant_scope ON alarm_runtime.alarm_idempotency;
CREATE TRIGGER alarm_idempotency_tenant_scope
  BEFORE INSERT OR UPDATE OF organization_id, tenant_id ON alarm_runtime.alarm_idempotency
  FOR EACH ROW EXECUTE FUNCTION alarm_runtime.enforce_write_tenant_scope();

ALTER TABLE alarm_runtime.organization_tenant_scope ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.organization_tenant_scope FORCE ROW LEVEL SECURITY;
CREATE POLICY organization_tenant_scope_migrator_all ON alarm_runtime.organization_tenant_scope
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
CREATE POLICY organization_tenant_scope_runtime_select ON alarm_runtime.organization_tenant_scope
  FOR SELECT TO s4_alarm_runtime
  USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
CREATE POLICY organization_tenant_scope_runtime_insert ON alarm_runtime.organization_tenant_scope
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (
    organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
    AND tenant_id = alarm_runtime.current_tenant_id()
  );

DROP POLICY IF EXISTS alarm_current_runtime_org ON alarm_runtime.alarm_current;
CREATE POLICY alarm_current_runtime_org ON alarm_runtime.alarm_current
  FOR ALL TO s4_alarm_runtime
  USING (
    tenant_id = alarm_runtime.current_tenant_id()
    AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
  )
  WITH CHECK (
    tenant_id = alarm_runtime.current_tenant_id()
    AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
  );

DROP POLICY IF EXISTS alarm_idempotency_runtime_org ON alarm_runtime.alarm_idempotency;
CREATE POLICY alarm_idempotency_runtime_org ON alarm_runtime.alarm_idempotency
  FOR ALL TO s4_alarm_runtime
  USING (
    tenant_id = alarm_runtime.current_tenant_id()
    AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
  )
  WITH CHECK (
    tenant_id = alarm_runtime.current_tenant_id()
    AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
  );

CREATE INDEX alarm_current_tenant_site_activity_idx
  ON alarm_runtime.alarm_current (tenant_id, organization_id, site_id, last_occurred_at DESC, alarm_id ASC);
CREATE INDEX alarm_idempotency_tenant_created_idx
  ON alarm_runtime.alarm_idempotency (tenant_id, organization_id, site_id, created_at DESC);

GRANT SELECT, INSERT ON alarm_runtime.organization_tenant_scope TO s4_alarm_runtime;
GRANT EXECUTE ON FUNCTION alarm_runtime.current_tenant_id() TO s4_alarm_runtime;
GRANT EXECUTE ON FUNCTION alarm_runtime.enforce_write_tenant_scope() TO s4_alarm_runtime;

RESET ROLE;
COMMIT;

BEGIN;
SET LOCAL ROLE s5_work_order_migrator;

DO $migrator_policies$
DECLARE
  table_name text;
BEGIN
  FOREACH table_name IN ARRAY ARRAY[
    'work_order_current',
    'work_order_source_reference',
    'work_order_timeline',
    'work_order_task',
    'work_order_note',
    'work_order_attachment_metadata',
    'work_order_completion_evidence',
    'work_order_idempotency',
    'work_order_mutation_audit'
  ]
  LOOP
    EXECUTE format('DROP POLICY IF EXISTS %I ON work_order_runtime.%I', table_name || '_migrator_all', table_name);
    EXECUTE format(
      'CREATE POLICY %I ON work_order_runtime.%I FOR ALL TO s5_work_order_migrator USING (true) WITH CHECK (true)',
      table_name || '_migrator_all', table_name
    );
  END LOOP;
END
$migrator_policies$;

DO $tenant_backfill_guard$
BEGIN
  IF EXISTS (SELECT 1 FROM work_order_runtime.work_order_current LIMIT 1)
     OR EXISTS (SELECT 1 FROM work_order_runtime.work_order_source_reference LIMIT 1)
     OR EXISTS (SELECT 1 FROM work_order_runtime.work_order_timeline LIMIT 1)
     OR EXISTS (SELECT 1 FROM work_order_runtime.work_order_task LIMIT 1)
     OR EXISTS (SELECT 1 FROM work_order_runtime.work_order_note LIMIT 1)
     OR EXISTS (SELECT 1 FROM work_order_runtime.work_order_attachment_metadata LIMIT 1)
     OR EXISTS (SELECT 1 FROM work_order_runtime.work_order_completion_evidence LIMIT 1)
     OR EXISTS (SELECT 1 FROM work_order_runtime.work_order_idempotency LIMIT 1)
     OR EXISTS (SELECT 1 FROM work_order_runtime.work_order_mutation_audit LIMIT 1) THEN
    RAISE EXCEPTION 'S5 tenant scope migration requires an explicit Organization-to-Tenant backfill for existing Work Order rows';
  END IF;
END
$tenant_backfill_guard$;

CREATE TABLE IF NOT EXISTS work_order_runtime.organization_tenant_scope (
  organization_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (updated_at >= created_at)
);

DO $tenant_columns$
DECLARE
  table_name text;
BEGIN
  FOREACH table_name IN ARRAY ARRAY[
    'work_order_current',
    'work_order_source_reference',
    'work_order_timeline',
    'work_order_task',
    'work_order_note',
    'work_order_attachment_metadata',
    'work_order_completion_evidence',
    'work_order_idempotency',
    'work_order_mutation_audit'
  ]
  LOOP
    EXECUTE format('ALTER TABLE work_order_runtime.%I ADD COLUMN tenant_id uuid NOT NULL', table_name);
  END LOOP;
END
$tenant_columns$;

CREATE OR REPLACE FUNCTION work_order_runtime.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$$;

CREATE OR REPLACE FUNCTION work_order_runtime.enforce_write_tenant_scope()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  active_tenant uuid;
  active_organization uuid;
BEGIN
  IF current_user = 's5_work_order_migrator' THEN
    SELECT scope.tenant_id INTO active_tenant
    FROM work_order_runtime.organization_tenant_scope AS scope
    WHERE scope.organization_id = NEW.organization_id;
    IF active_tenant IS NULL THEN
      RAISE EXCEPTION 'Work Order migrator writes require an Organization-to-Tenant binding' USING ERRCODE = '23503';
    END IF;
    IF NEW.tenant_id IS NULL THEN
      NEW.tenant_id := active_tenant;
    ELSIF NEW.tenant_id <> active_tenant THEN
      RAISE EXCEPTION 'Work Order migrator tenant binding mismatch' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;

  active_tenant := work_order_runtime.current_tenant_id();
  active_organization := NULLIF(current_setting('app.organization_id', true), '')::uuid;
  IF active_tenant IS NULL OR active_organization IS NULL THEN
    RAISE EXCEPTION 'Work Order tenant and organization context are required' USING ERRCODE = '42501';
  END IF;
  IF NEW.organization_id <> active_organization THEN
    RAISE EXCEPTION 'Work Order organization context mismatch' USING ERRCODE = '42501';
  END IF;
  IF NEW.tenant_id IS NULL THEN
    NEW.tenant_id := active_tenant;
  ELSIF NEW.tenant_id <> active_tenant THEN
    RAISE EXCEPTION 'Work Order tenant context mismatch' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;

DO $tenant_triggers$
DECLARE
  table_name text;
BEGIN
  FOREACH table_name IN ARRAY ARRAY[
    'work_order_current',
    'work_order_source_reference',
    'work_order_timeline',
    'work_order_task',
    'work_order_note',
    'work_order_attachment_metadata',
    'work_order_completion_evidence',
    'work_order_idempotency',
    'work_order_mutation_audit'
  ]
  LOOP
    EXECUTE format('DROP TRIGGER IF EXISTS %I ON work_order_runtime.%I', table_name || '_tenant_scope', table_name);
    EXECUTE format(
      'CREATE TRIGGER %I BEFORE INSERT OR UPDATE OF organization_id, tenant_id ON work_order_runtime.%I FOR EACH ROW EXECUTE FUNCTION work_order_runtime.enforce_write_tenant_scope()',
      table_name || '_tenant_scope', table_name
    );
  END LOOP;
END
$tenant_triggers$;

ALTER TABLE work_order_runtime.organization_tenant_scope ENABLE ROW LEVEL SECURITY;
ALTER TABLE work_order_runtime.organization_tenant_scope FORCE ROW LEVEL SECURITY;

CREATE POLICY organization_tenant_scope_migrator_all
  ON work_order_runtime.organization_tenant_scope FOR ALL TO s5_work_order_migrator
  USING (true) WITH CHECK (true);
CREATE POLICY organization_tenant_scope_runtime_select
  ON work_order_runtime.organization_tenant_scope FOR SELECT TO s5_work_order_runtime
  USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
CREATE POLICY organization_tenant_scope_runtime_insert
  ON work_order_runtime.organization_tenant_scope FOR INSERT TO s5_work_order_runtime
  WITH CHECK (
    organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
    AND tenant_id = work_order_runtime.current_tenant_id()
  );
CREATE POLICY organization_tenant_scope_writer_select
  ON work_order_runtime.organization_tenant_scope FOR SELECT TO s5_work_order_writer
  USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
CREATE POLICY organization_tenant_scope_writer_insert
  ON work_order_runtime.organization_tenant_scope FOR INSERT TO s5_work_order_writer
  WITH CHECK (
    organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
    AND tenant_id = work_order_runtime.current_tenant_id()
  );

DO $tenant_read_policies$
DECLARE
  table_name text;
BEGIN
  FOREACH table_name IN ARRAY ARRAY[
    'work_order_current',
    'work_order_source_reference',
    'work_order_timeline',
    'work_order_task',
    'work_order_note',
    'work_order_attachment_metadata',
    'work_order_completion_evidence',
    'work_order_idempotency',
    'work_order_mutation_audit'
  ]
  LOOP
    EXECUTE format('DROP POLICY IF EXISTS %I ON work_order_runtime.%I', table_name || '_runtime_org', table_name);
    EXECUTE format(
      'CREATE POLICY %I ON work_order_runtime.%I FOR SELECT TO s5_work_order_runtime USING (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting(''app.organization_id'', true), '''')::uuid)',
      table_name || '_runtime_org', table_name
    );
    EXECUTE format('DROP POLICY IF EXISTS %I ON work_order_runtime.%I', table_name || '_writer_read_org', table_name);
    EXECUTE format(
      'CREATE POLICY %I ON work_order_runtime.%I FOR SELECT TO s5_work_order_writer USING (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting(''app.organization_id'', true), '''')::uuid)',
      table_name || '_writer_read_org', table_name
    );
  END LOOP;
END
$tenant_read_policies$;

DROP POLICY IF EXISTS work_order_current_writer_insert_org ON work_order_runtime.work_order_current;
CREATE POLICY work_order_current_writer_insert_org ON work_order_runtime.work_order_current
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_current_writer_update_org ON work_order_runtime.work_order_current;
CREATE POLICY work_order_current_writer_update_org ON work_order_runtime.work_order_current
  FOR UPDATE TO s5_work_order_writer
  USING (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid)
  WITH CHECK (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

DROP POLICY IF EXISTS work_order_source_reference_writer_insert_org ON work_order_runtime.work_order_source_reference;
CREATE POLICY work_order_source_reference_writer_insert_org ON work_order_runtime.work_order_source_reference
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_timeline_writer_insert_org ON work_order_runtime.work_order_timeline;
CREATE POLICY work_order_timeline_writer_insert_org ON work_order_runtime.work_order_timeline
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_idempotency_writer_insert_org ON work_order_runtime.work_order_idempotency;
CREATE POLICY work_order_idempotency_writer_insert_org ON work_order_runtime.work_order_idempotency
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_mutation_audit_writer_insert_org ON work_order_runtime.work_order_mutation_audit;
CREATE POLICY work_order_mutation_audit_writer_insert_org ON work_order_runtime.work_order_mutation_audit
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_completion_evidence_writer_insert_org ON work_order_runtime.work_order_completion_evidence;
CREATE POLICY work_order_completion_evidence_writer_insert_org ON work_order_runtime.work_order_completion_evidence
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = work_order_runtime.current_tenant_id() AND organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

CREATE INDEX work_order_current_tenant_site_updated_idx
  ON work_order_runtime.work_order_current (tenant_id, organization_id, site_id, updated_at DESC, work_order_id ASC);
CREATE INDEX work_order_idempotency_tenant_created_idx
  ON work_order_runtime.work_order_idempotency (tenant_id, organization_id, site_id, committed_at DESC);
CREATE INDEX work_order_mutation_audit_tenant_created_idx
  ON work_order_runtime.work_order_mutation_audit (tenant_id, organization_id, site_id, committed_at DESC);

GRANT SELECT, INSERT ON work_order_runtime.organization_tenant_scope TO s5_work_order_runtime;
GRANT SELECT, INSERT ON work_order_runtime.organization_tenant_scope TO s5_work_order_writer;
GRANT EXECUTE ON FUNCTION work_order_runtime.current_tenant_id() TO s5_work_order_runtime, s5_work_order_writer;
GRANT EXECUTE ON FUNCTION work_order_runtime.enforce_write_tenant_scope() TO s5_work_order_writer;

RESET ROLE;
COMMIT;

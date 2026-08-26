BEGIN;
SET LOCAL ROLE s5_work_order_migrator;

ALTER TABLE work_order_runtime.work_order_timeline
  ADD COLUMN IF NOT EXISTS assignee_id text CHECK (assignee_id IS NULL OR length(btrim(assignee_id)) BETWEEN 1 AND 256),
  ADD COLUMN IF NOT EXISTS team_id text CHECK (team_id IS NULL OR length(btrim(team_id)) BETWEEN 1 AND 256);

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_idempotency (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  operation text NOT NULL CHECK (operation IN ('CREATE', 'ASSIGN')),
  resource_key text NOT NULL CHECK (length(btrim(resource_key)) BETWEEN 1 AND 256),
  idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$'),
  request_digest text NOT NULL CHECK (request_digest ~ '^[a-f0-9]{64}$'),
  response_payload jsonb NOT NULL CHECK (octet_length(response_payload::text) <= 65536),
  committed_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, operation, resource_key, idempotency_key)
);

CREATE TABLE IF NOT EXISTS work_order_runtime.work_order_mutation_audit (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  work_order_id uuid NOT NULL,
  operation text NOT NULL CHECK (operation IN ('CREATE', 'ASSIGN')),
  idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$'),
  request_digest text NOT NULL CHECK (request_digest ~ '^[a-f0-9]{64}$'),
  actor_type text NOT NULL CHECK (length(btrim(actor_type)) BETWEEN 1 AND 64),
  actor_id text NOT NULL CHECK (length(btrim(actor_id)) BETWEEN 1 AND 256),
  policy_revision text NOT NULL CHECK (length(btrim(policy_revision)) BETWEEN 1 AND 128),
  correlation_id text NOT NULL CHECK (length(btrim(correlation_id)) BETWEEN 1 AND 256),
  committed_version bigint NOT NULL CHECK (committed_version > 0),
  committed_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, work_order_id, operation, idempotency_key),
  FOREIGN KEY (tenant_id, site_id, work_order_id)
    REFERENCES work_order_runtime.work_order_current (tenant_id, site_id, work_order_id)
    ON DELETE RESTRICT
);

ALTER TABLE work_order_runtime.work_order_idempotency ENABLE ROW LEVEL SECURITY;
ALTER TABLE work_order_runtime.work_order_idempotency FORCE ROW LEVEL SECURITY;
ALTER TABLE work_order_runtime.work_order_mutation_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE work_order_runtime.work_order_mutation_audit FORCE ROW LEVEL SECURITY;

DO $policies$
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
    EXECUTE format('DROP POLICY IF EXISTS %I ON work_order_runtime.%I', table_name || '_writer_read_org', table_name);
    EXECUTE format(
      'CREATE POLICY %I ON work_order_runtime.%I FOR SELECT TO s5_work_order_writer USING (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid)',
      table_name || '_writer_read_org', table_name
    );
  END LOOP;
END
$policies$;

DROP POLICY IF EXISTS work_order_current_writer_insert_org ON work_order_runtime.work_order_current;
CREATE POLICY work_order_current_writer_insert_org ON work_order_runtime.work_order_current
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_current_writer_update_org ON work_order_runtime.work_order_current;
CREATE POLICY work_order_current_writer_update_org ON work_order_runtime.work_order_current
  FOR UPDATE TO s5_work_order_writer
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

DROP POLICY IF EXISTS work_order_source_reference_writer_insert_org ON work_order_runtime.work_order_source_reference;
CREATE POLICY work_order_source_reference_writer_insert_org ON work_order_runtime.work_order_source_reference
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_timeline_writer_insert_org ON work_order_runtime.work_order_timeline;
CREATE POLICY work_order_timeline_writer_insert_org ON work_order_runtime.work_order_timeline
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_idempotency_writer_insert_org ON work_order_runtime.work_order_idempotency;
CREATE POLICY work_order_idempotency_writer_insert_org ON work_order_runtime.work_order_idempotency
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_mutation_audit_writer_insert_org ON work_order_runtime.work_order_mutation_audit;
CREATE POLICY work_order_mutation_audit_writer_insert_org ON work_order_runtime.work_order_mutation_audit
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON SCHEMA work_order_runtime FROM s5_work_order_writer;
REVOKE ALL ON ALL TABLES IN SCHEMA work_order_runtime FROM s5_work_order_writer;
GRANT USAGE ON SCHEMA work_order_runtime TO s5_work_order_writer;
GRANT SELECT ON
  work_order_runtime.work_order_current,
  work_order_runtime.work_order_source_reference,
  work_order_runtime.work_order_timeline,
  work_order_runtime.work_order_task,
  work_order_runtime.work_order_note,
  work_order_runtime.work_order_attachment_metadata,
  work_order_runtime.work_order_completion_evidence,
  work_order_runtime.work_order_idempotency,
  work_order_runtime.work_order_mutation_audit
TO s5_work_order_writer;
GRANT INSERT ON work_order_runtime.work_order_current TO s5_work_order_writer;
GRANT UPDATE (assignee_id, team_id, version, updated_at)
  ON work_order_runtime.work_order_current TO s5_work_order_writer;
GRANT INSERT ON work_order_runtime.work_order_source_reference TO s5_work_order_writer;
GRANT INSERT ON work_order_runtime.work_order_timeline TO s5_work_order_writer;
GRANT INSERT ON work_order_runtime.work_order_idempotency TO s5_work_order_writer;
GRANT INSERT ON work_order_runtime.work_order_mutation_audit TO s5_work_order_writer;

COMMIT;

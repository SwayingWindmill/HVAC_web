BEGIN;
SET LOCAL ROLE s5_work_order_migrator;

ALTER TABLE work_order_runtime.work_order_idempotency
  DROP CONSTRAINT IF EXISTS work_order_idempotency_operation_check;
ALTER TABLE work_order_runtime.work_order_idempotency
  ADD CONSTRAINT work_order_idempotency_operation_check
  CHECK (operation IN ('CREATE', 'ASSIGN', 'LIFECYCLE', 'SCHEDULE', 'START', 'BLOCK', 'RESUME', 'COMPLETE', 'CANCEL', 'REOPEN'));

ALTER TABLE work_order_runtime.work_order_mutation_audit
  DROP CONSTRAINT IF EXISTS work_order_mutation_audit_operation_check;
ALTER TABLE work_order_runtime.work_order_mutation_audit
  ADD CONSTRAINT work_order_mutation_audit_operation_check
  CHECK (operation IN ('CREATE', 'ASSIGN', 'SCHEDULE', 'START', 'BLOCK', 'RESUME', 'COMPLETE', 'CANCEL', 'REOPEN'));

ALTER TABLE work_order_runtime.work_order_completion_evidence
  ADD COLUMN IF NOT EXISTS completion_version bigint;
UPDATE work_order_runtime.work_order_completion_evidence AS evidence
SET completion_version = current.version
FROM work_order_runtime.work_order_current AS current
WHERE evidence.tenant_id = current.tenant_id
  AND evidence.site_id = current.site_id
  AND evidence.work_order_id = current.work_order_id
  AND evidence.completion_version IS NULL;
ALTER TABLE work_order_runtime.work_order_completion_evidence
  ALTER COLUMN completion_version SET NOT NULL;
ALTER TABLE work_order_runtime.work_order_completion_evidence
  DROP CONSTRAINT IF EXISTS work_order_completion_evidence_completion_version_check;
ALTER TABLE work_order_runtime.work_order_completion_evidence
  ADD CONSTRAINT work_order_completion_evidence_completion_version_check CHECK (completion_version > 0);

DROP POLICY IF EXISTS work_order_completion_evidence_writer_insert_org ON work_order_runtime.work_order_completion_evidence;
CREATE POLICY work_order_completion_evidence_writer_insert_org ON work_order_runtime.work_order_completion_evidence
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT UPDATE (status, scheduled_start, due_at, version, updated_at)
  ON work_order_runtime.work_order_current TO s5_work_order_writer;
GRANT INSERT ON work_order_runtime.work_order_completion_evidence TO s5_work_order_writer;

COMMIT;

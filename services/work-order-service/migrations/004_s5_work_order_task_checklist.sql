BEGIN;
SET LOCAL ROLE s5_work_order_migrator;

ALTER TABLE work_order_runtime.work_order_timeline
  ADD COLUMN IF NOT EXISTS task_id uuid;
ALTER TABLE work_order_runtime.work_order_mutation_audit
  ADD COLUMN IF NOT EXISTS task_id uuid;

ALTER TABLE work_order_runtime.work_order_idempotency
  DROP CONSTRAINT IF EXISTS work_order_idempotency_operation_check;
ALTER TABLE work_order_runtime.work_order_idempotency
  ADD CONSTRAINT work_order_idempotency_operation_check
  CHECK (operation IN ('CREATE', 'ASSIGN', 'LIFECYCLE', 'TASK', 'SCHEDULE', 'START', 'BLOCK', 'RESUME', 'COMPLETE', 'CANCEL', 'REOPEN'));

ALTER TABLE work_order_runtime.work_order_mutation_audit
  DROP CONSTRAINT IF EXISTS work_order_mutation_audit_operation_check;
ALTER TABLE work_order_runtime.work_order_mutation_audit
  ADD CONSTRAINT work_order_mutation_audit_operation_check
  CHECK (operation IN ('CREATE', 'ASSIGN', 'SCHEDULE', 'START', 'BLOCK', 'RESUME', 'COMPLETE', 'CANCEL', 'REOPEN', 'TASK_APPEND', 'TASK_STATUS', 'TASK_REORDER'));

ALTER TABLE work_order_runtime.work_order_timeline
  DROP CONSTRAINT IF EXISTS work_order_timeline_operation_check;
ALTER TABLE work_order_runtime.work_order_timeline
  ADD CONSTRAINT work_order_timeline_operation_check
  CHECK (operation IN ('CREATE', 'OPEN', 'ASSIGN', 'UNASSIGN', 'SCHEDULE', 'START', 'BLOCK', 'RESUME', 'COMPLETE', 'CANCEL', 'REOPEN', 'TASK_APPEND', 'TASK_STATUS', 'TASK_REORDER'));

DROP POLICY IF EXISTS work_order_task_writer_insert_org ON work_order_runtime.work_order_task;
CREATE POLICY work_order_task_writer_insert_org ON work_order_runtime.work_order_task
  FOR INSERT TO s5_work_order_writer
  WITH CHECK (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
DROP POLICY IF EXISTS work_order_task_writer_update_org ON work_order_runtime.work_order_task;
CREATE POLICY work_order_task_writer_update_org ON work_order_runtime.work_order_task
  FOR UPDATE TO s5_work_order_writer
  USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid)
  WITH CHECK (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

GRANT UPDATE (task_total, task_completed, task_blocked, version, updated_at)
  ON work_order_runtime.work_order_current TO s5_work_order_writer;
GRANT INSERT ON work_order_runtime.work_order_task TO s5_work_order_writer;
GRANT UPDATE (position, status, version, updated_at)
  ON work_order_runtime.work_order_task TO s5_work_order_writer;

COMMIT;

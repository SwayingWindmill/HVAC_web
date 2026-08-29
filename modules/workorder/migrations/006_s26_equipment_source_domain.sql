BEGIN;
SET LOCAL ROLE s5_work_order_migrator;

ALTER TABLE work_order_runtime.work_order_source_reference
  DROP CONSTRAINT IF EXISTS work_order_source_reference_source_domain_check;
ALTER TABLE work_order_runtime.work_order_source_reference
  ADD CONSTRAINT work_order_source_reference_source_domain_check
  CHECK (source_domain IN ('MANUAL', 'ALARM', 'ASSET', 'EQUIPMENT', 'INVESTIGATION', 'EXTERNAL'));

COMMIT;

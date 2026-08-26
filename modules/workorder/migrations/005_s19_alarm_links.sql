BEGIN;
SET LOCAL ROLE s5_work_order_migrator;

ALTER TABLE work_order_runtime.work_order_source_reference
  DROP CONSTRAINT IF EXISTS work_order_source_reference_source_domain_check;
ALTER TABLE work_order_runtime.work_order_source_reference
  ADD CONSTRAINT work_order_source_reference_source_domain_check
  CHECK (source_domain IN ('MANUAL', 'ALARM', 'ASSET', 'INVESTIGATION', 'EXTERNAL'));

ALTER TABLE work_order_runtime.work_order_source_reference
  DROP CONSTRAINT IF EXISTS work_order_source_reference_authoritative_id_check;
ALTER TABLE work_order_runtime.work_order_source_reference
  ADD CONSTRAINT work_order_source_reference_authoritative_id_check
  CHECK (
    source_domain IN ('MANUAL', 'EXTERNAL')
    OR source_resource_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
  );

CREATE INDEX IF NOT EXISTS work_order_alarm_link_lookup_idx
  ON work_order_runtime.work_order_source_reference (tenant_id, site_id, source_resource_id, work_order_id)
  WHERE source_domain = 'ALARM';

-- Source links belong to the Work Order aggregate. The writer remains INSERT-only
-- on this table, so Work Order lifecycle operations cannot rewrite or delete Alarm
-- links; the Work Order role also has no authority in the Alarm database.

COMMIT;

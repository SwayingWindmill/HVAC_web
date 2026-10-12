-- FDD evaluates a Site's rules on a schedule: the scheduler fires FDD_EVALUATION jobs, and the
-- FDD runtime claims, runs and completes only those (ADR 0020).
BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.schedule_definitions
  DROP CONSTRAINT IF EXISTS schedule_definitions_job_type_check;
ALTER TABLE core_registry.schedule_definitions
  ADD CONSTRAINT schedule_definitions_job_type_check CHECK (job_type IN (
    'METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','FORECAST_RUN','OPTIMIZATION_RUN','FDD_EVALUATION',
    'SETTLEMENT_CALC','SETTLEMENT_RECONCILE','DATA_RETENTION_SCAN','DATA_ARCHIVE','REPORT_GENERATE',
    'EXPORT_GENERATE','CERTIFICATE_EXPIRY_SCAN','OUTBOX_CLEANUP','INBOX_CLEANUP','PROJECTION_REPAIR',
    'DEAD_WORK_DISPOSITION','TENANT_RETIREMENT'
  ));

ALTER TABLE core_registry.job_instances
  DROP CONSTRAINT IF EXISTS job_instances_job_type_check;
ALTER TABLE core_registry.job_instances
  ADD CONSTRAINT job_instances_job_type_check CHECK (job_type IN (
    'METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','FORECAST_RUN','OPTIMIZATION_RUN','FDD_EVALUATION',
    'SETTLEMENT_CALC','SETTLEMENT_RECONCILE','DATA_RETENTION_SCAN','DATA_ARCHIVE','REPORT_GENERATE',
    'EXPORT_GENERATE','CERTIFICATE_EXPIRY_SCAN','OUTBOX_CLEANUP','INBOX_CLEANUP','PROJECTION_REPAIR',
    'DEAD_WORK_DISPOSITION','TENANT_RETIREMENT'
  ));

CREATE POLICY job_instances_fdd_worker_scope ON core_registry.job_instances FOR SELECT TO fdd_runtime
  USING (job_type = 'FDD_EVALUATION');
CREATE POLICY job_instances_fdd_worker_update_scope ON core_registry.job_instances FOR UPDATE TO fdd_runtime
  USING (job_type = 'FDD_EVALUATION') WITH CHECK (job_type = 'FDD_EVALUATION');
CREATE POLICY job_attempts_fdd_worker_scope ON core_registry.job_attempts FOR ALL TO fdd_runtime
  USING (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'FDD_EVALUATION'))
  WITH CHECK (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id = job_attempts.job_id AND j.job_type = 'FDD_EVALUATION'));

GRANT SELECT, UPDATE ON core_registry.job_instances TO fdd_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.job_attempts TO fdd_runtime;

COMMIT;

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_core_migrator;

GRANT INSERT ON core_registry.job_instances TO metric_engine_runtime;

DROP POLICY IF EXISTS job_instances_metric_worker_insert_scope ON core_registry.job_instances;
CREATE POLICY job_instances_metric_worker_insert_scope ON core_registry.job_instances
  FOR INSERT TO metric_engine_runtime
  WITH CHECK (job_type = 'METRIC_RECALC' AND trigger_type = 'BACKFILL');

RESET ROLE;

COMMIT;

BEGIN;
SET LOCAL ROLE s1_core_migrator;

CREATE TABLE IF NOT EXISTS core_registry.schedule_definitions (
  schedule_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(schedule_id)),
  schedule_code text NOT NULL,
  schedule_name text NOT NULL,
  tenant_id uuid NULL CHECK (tenant_id IS NULL OR core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NULL CHECK (site_id IS NULL OR core_registry.is_uuid_v7(site_id)),
  job_type text NOT NULL CHECK (job_type IN (
    'METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','FORECAST_RUN','OPTIMIZATION_RUN',
    'SETTLEMENT_CALC','SETTLEMENT_RECONCILE','DATA_RETENTION_SCAN','DATA_ARCHIVE','REPORT_GENERATE',
    'EXPORT_GENERATE','CERTIFICATE_EXPIRY_SCAN'
  )),
  schedule_type text NOT NULL CHECK (schedule_type IN ('CRON','INTERVAL','ONCE')),
  cron_expression text NULL,
  cron_format_version text NULL,
  interval_seconds bigint NULL CHECK (interval_seconds IS NULL OR interval_seconds > 0),
  timezone text NOT NULL DEFAULT 'UTC',
  misfire_policy text NOT NULL CHECK (misfire_policy IN ('SKIP','FIRE_ONCE','CATCH_UP','CATCH_UP_LIMITED')),
  catch_up_limit integer NULL CHECK (catch_up_limit IS NULL OR catch_up_limit > 0),
  concurrency_policy text NOT NULL DEFAULT 'FORBID' CHECK (concurrency_policy IN ('ALLOW','FORBID','REPLACE')),
  timeout_seconds integer NOT NULL CHECK (timeout_seconds > 0),
  max_attempts integer NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
  retry_policy jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(retry_policy) = 'object'),
  payload_template jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload_template) = 'object'),
  enabled boolean NOT NULL DEFAULT true,
  next_fire_at timestamptz NULL,
  last_fire_at timestamptz NULL,
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, schedule_code),
  CHECK (site_id IS NULL OR tenant_id IS NOT NULL),
  CHECK (
    (schedule_type = 'CRON' AND cron_expression IS NOT NULL AND btrim(cron_expression) <> '' AND cron_format_version = '5-field-v1' AND interval_seconds IS NULL)
    OR (schedule_type = 'INTERVAL' AND cron_expression IS NULL AND cron_format_version IS NULL AND interval_seconds IS NOT NULL)
    OR (schedule_type = 'ONCE' AND cron_expression IS NULL AND cron_format_version IS NULL AND interval_seconds IS NULL)
  ),
  CHECK (misfire_policy <> 'CATCH_UP_LIMITED' OR catch_up_limit IS NOT NULL),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.job_instances (
  job_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(job_id)),
  schedule_id uuid NULL REFERENCES core_registry.schedule_definitions(schedule_id),
  trigger_type text NOT NULL CHECK (trigger_type IN ('SCHEDULE','MANUAL','EVENT','DEPENDENCY','RECOVERY','BACKFILL')),
  job_type text NOT NULL CHECK (job_type IN (
    'METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','FORECAST_RUN','OPTIMIZATION_RUN',
    'SETTLEMENT_CALC','SETTLEMENT_RECONCILE','DATA_RETENTION_SCAN','DATA_ARCHIVE','REPORT_GENERATE',
    'EXPORT_GENERATE','CERTIFICATE_EXPIRY_SCAN'
  )),
  tenant_id uuid NULL CHECK (tenant_id IS NULL OR core_registry.is_uuid_v7(tenant_id)),
  site_id uuid NULL CHECK (site_id IS NULL OR core_registry.is_uuid_v7(site_id)),
  subject_type text NULL,
  subject_id text NULL,
  scheduled_for timestamptz NOT NULL,
  schedule_timezone text NULL,
  priority integer NOT NULL DEFAULT 50 CHECK (priority BETWEEN 0 AND 100),
  dedup_key text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
  state text NOT NULL CHECK (state IN ('PENDING','READY','CLAIMED','RUNNING','RETRY_WAIT','SUCCEEDED','FAILED','CANCELLED','SKIPPED','DEAD')),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  max_attempts integer NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
  lease_owner text NULL,
  lease_until timestamptz NULL,
  next_retry_at timestamptz NULL,
  timeout_seconds integer NOT NULL CHECK (timeout_seconds > 0),
  parent_job_id uuid NULL CHECK (parent_job_id IS NULL OR core_registry.is_uuid_v7(parent_job_id)),
  cancel_requested boolean NOT NULL DEFAULT false,
  started_at timestamptz NULL,
  completed_at timestamptz NULL,
  error_code text NULL,
  error_message text NULL,
  trace_id text NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (dedup_key),
  CHECK (site_id IS NULL OR tenant_id IS NOT NULL),
  CHECK (attempt_count <= max_attempts),
  CHECK ((state IN ('CLAIMED','RUNNING') AND lease_owner IS NOT NULL AND lease_until IS NOT NULL) OR (state NOT IN ('CLAIMED','RUNNING'))),
  CHECK ((state = 'RETRY_WAIT' AND next_retry_at IS NOT NULL) OR state <> 'RETRY_WAIT'),
  CHECK ((state IN ('SUCCEEDED','FAILED','CANCELLED','SKIPPED','DEAD') AND completed_at IS NOT NULL) OR state NOT IN ('SUCCEEDED','FAILED','CANCELLED','SKIPPED','DEAD')),
  CHECK (completed_at IS NULL OR started_at IS NULL OR completed_at >= started_at),
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS core_registry.job_attempts (
  attempt_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(attempt_id)),
  job_id uuid NOT NULL REFERENCES core_registry.job_instances(job_id),
  attempt_no integer NOT NULL CHECK (attempt_no > 0),
  worker_id text NOT NULL,
  started_at timestamptz NOT NULL,
  completed_at timestamptz NULL,
  result_status text NULL CHECK (result_status IS NULL OR result_status IN ('SUCCEEDED','FAILED','RETRY_WAIT','CANCELLED','DEAD')),
  error_code text NULL,
  error_message text NULL,
  duration_ms bigint NULL CHECK (duration_ms IS NULL OR duration_ms >= 0),
  output_summary jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(output_summary) = 'object'),
  UNIQUE (job_id, attempt_no),
  CHECK (completed_at IS NULL OR completed_at >= started_at)
);

CREATE OR REPLACE FUNCTION core_registry.validate_job_instance_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF NEW.job_id <> OLD.job_id
      OR NEW.schedule_id IS DISTINCT FROM OLD.schedule_id
      OR NEW.trigger_type <> OLD.trigger_type
      OR NEW.job_type <> OLD.job_type
      OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
      OR NEW.site_id IS DISTINCT FROM OLD.site_id
      OR NEW.subject_type IS DISTINCT FROM OLD.subject_type
      OR NEW.subject_id IS DISTINCT FROM OLD.subject_id
      OR NEW.scheduled_for <> OLD.scheduled_for
      OR NEW.schedule_timezone IS DISTINCT FROM OLD.schedule_timezone
      OR NEW.dedup_key <> OLD.dedup_key
      OR NEW.payload <> OLD.payload
      OR NEW.max_attempts <> OLD.max_attempts
      OR NEW.timeout_seconds <> OLD.timeout_seconds
      OR NEW.parent_job_id IS DISTINCT FROM OLD.parent_job_id THEN
      RAISE EXCEPTION 'Job Instance identity is immutable' USING ERRCODE = '23514';
    END IF;

    IF OLD.state = NEW.state THEN
      RETURN NEW;
    END IF;

    IF OLD.state = 'PENDING' AND NEW.state NOT IN ('READY','CANCELLED','SKIPPED') THEN
      RAISE EXCEPTION 'PENDING Job can only become READY/CANCELLED/SKIPPED' USING ERRCODE = '23514';
    ELSIF OLD.state = 'READY' AND NEW.state NOT IN ('CLAIMED','CANCELLED','SKIPPED') THEN
      RAISE EXCEPTION 'READY Job can only become CLAIMED/CANCELLED/SKIPPED' USING ERRCODE = '23514';
    ELSIF OLD.state = 'CLAIMED' AND NEW.state NOT IN ('RUNNING','RETRY_WAIT','FAILED','CANCELLED','DEAD') THEN
      RAISE EXCEPTION 'CLAIMED Job transition is invalid' USING ERRCODE = '23514';
    ELSIF OLD.state = 'RUNNING' AND NEW.state NOT IN ('SUCCEEDED','RETRY_WAIT','FAILED','CANCELLED','DEAD') THEN
      RAISE EXCEPTION 'RUNNING Job transition is invalid' USING ERRCODE = '23514';
    ELSIF OLD.state = 'RETRY_WAIT' AND NEW.state NOT IN ('READY','CANCELLED','DEAD') THEN
      RAISE EXCEPTION 'RETRY_WAIT Job can only become READY/CANCELLED/DEAD' USING ERRCODE = '23514';
    ELSIF OLD.state IN ('SUCCEEDED','FAILED','CANCELLED','SKIPPED','DEAD') THEN
      RAISE EXCEPTION 'terminal Job state is immutable' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS job_instances_validate_transition ON core_registry.job_instances;
CREATE TRIGGER job_instances_validate_transition
BEFORE UPDATE ON core_registry.job_instances
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_job_instance_transition();

CREATE INDEX IF NOT EXISTS schedule_definitions_due_idx
  ON core_registry.schedule_definitions (next_fire_at)
  WHERE enabled = true;
CREATE INDEX IF NOT EXISTS job_instances_ready_idx
  ON core_registry.job_instances (priority DESC, scheduled_for)
  WHERE state IN ('READY','RETRY_WAIT');
CREATE INDEX IF NOT EXISTS job_instances_lease_idx
  ON core_registry.job_instances (lease_until)
  WHERE state IN ('CLAIMED','RUNNING');
CREATE INDEX IF NOT EXISTS job_instances_type_state_idx
  ON core_registry.job_instances (job_type, state, priority DESC, scheduled_for);
CREATE INDEX IF NOT EXISTS job_attempts_job_idx
  ON core_registry.job_attempts (job_id, attempt_no);

ALTER TABLE core_registry.schedule_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.schedule_definitions FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.job_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.job_instances FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.job_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.job_attempts FORCE ROW LEVEL SECURITY;

CREATE POLICY schedule_definitions_scheduler_scope ON core_registry.schedule_definitions
  FOR ALL TO scheduler_runtime USING (true) WITH CHECK (true);
CREATE POLICY job_instances_scheduler_scope ON core_registry.job_instances
  FOR ALL TO scheduler_runtime USING (true) WITH CHECK (true);
CREATE POLICY job_attempts_scheduler_scope ON core_registry.job_attempts
  FOR ALL TO scheduler_runtime USING (true) WITH CHECK (true);

CREATE POLICY job_instances_metric_worker_scope ON core_registry.job_instances
  FOR SELECT TO metric_engine_runtime
  USING (job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL'));
CREATE POLICY job_instances_metric_worker_update_scope ON core_registry.job_instances
  FOR UPDATE TO metric_engine_runtime
  USING (job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL'))
  WITH CHECK (job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL'));
CREATE POLICY job_attempts_metric_worker_scope ON core_registry.job_attempts
  FOR ALL TO metric_engine_runtime
  USING (EXISTS (
    SELECT 1 FROM core_registry.job_instances j
    WHERE j.job_id = job_attempts.job_id
      AND j.job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL')
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM core_registry.job_instances j
    WHERE j.job_id = job_attempts.job_id
      AND j.job_type IN ('METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL')
  ));

REVOKE ALL ON core_registry.schedule_definitions, core_registry.job_instances, core_registry.job_attempts FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON core_registry.schedule_definitions, core_registry.job_instances TO scheduler_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.job_attempts TO scheduler_runtime;
GRANT SELECT, UPDATE ON core_registry.job_instances TO metric_engine_runtime;
GRANT SELECT, INSERT, UPDATE ON core_registry.job_attempts TO metric_engine_runtime;

RESET ROLE;
COMMIT;

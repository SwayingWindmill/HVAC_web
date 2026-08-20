BEGIN;

SET LOCAL ROLE s1_core_migrator;

ALTER TABLE core_registry.schedule_definitions
  DROP CONSTRAINT IF EXISTS schedule_definitions_job_type_check;
ALTER TABLE core_registry.schedule_definitions
  ADD CONSTRAINT schedule_definitions_job_type_check CHECK (job_type IN (
    'METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','FORECAST_RUN','OPTIMIZATION_RUN',
    'SETTLEMENT_CALC','SETTLEMENT_RECONCILE','DATA_RETENTION_SCAN','DATA_ARCHIVE','REPORT_GENERATE',
    'EXPORT_GENERATE','CERTIFICATE_EXPIRY_SCAN','OUTBOX_CLEANUP','INBOX_CLEANUP','PROJECTION_REPAIR',
    'DEAD_WORK_DISPOSITION','TENANT_RETIREMENT'
  ));

ALTER TABLE core_registry.job_instances
  DROP CONSTRAINT IF EXISTS job_instances_job_type_check;
ALTER TABLE core_registry.job_instances
  ADD CONSTRAINT job_instances_job_type_check CHECK (job_type IN (
    'METRIC_WINDOW_CALC','METRIC_RECALC','METRIC_BACKFILL','FORECAST_RUN','OPTIMIZATION_RUN',
    'SETTLEMENT_CALC','SETTLEMENT_RECONCILE','DATA_RETENTION_SCAN','DATA_ARCHIVE','REPORT_GENERATE',
    'EXPORT_GENERATE','CERTIFICATE_EXPIRY_SCAN','OUTBOX_CLEANUP','INBOX_CLEANUP','PROJECTION_REPAIR',
    'DEAD_WORK_DISPOSITION','TENANT_RETIREMENT'
  ));

CREATE TABLE core_registry.maintenance_work_types (
  job_type text PRIMARY KEY,
  owner_code text NOT NULL CHECK (owner_code IN ('METRIC','PLATFORM_OPERATIONS','DOMAIN_OWNER')),
  side_effect_class text NOT NULL CHECK (side_effect_class IN ('READ_ONLY','IDEMPOTENT_WRITE','OWNER_PROOF_REQUIRED')),
  lease_recovery text NOT NULL CHECK (lease_recovery IN ('RETRY_SAFE','RECONCILE_REQUIRED')),
  description text NOT NULL CHECK (length(btrim(description)) BETWEEN 1 AND 512)
);

INSERT INTO core_registry.maintenance_work_types(job_type,owner_code,side_effect_class,lease_recovery,description) VALUES
  ('DATA_RETENTION_SCAN','METRIC','IDEMPOTENT_WRITE','RETRY_SAFE','Lifecycle-policy retention guarded by Legal Hold and deletion Tombstone proof.'),
  ('DATA_ARCHIVE','METRIC','IDEMPOTENT_WRITE','RETRY_SAFE','Archive work requires durable Archive Manifest proof before governed deletion.'),
  ('CERTIFICATE_EXPIRY_SCAN','PLATFORM_OPERATIONS','IDEMPOTENT_WRITE','RETRY_SAFE','Find expiring API credentials and mTLS certificates and create actionable maintenance events.'),
  ('OUTBOX_CLEANUP','DOMAIN_OWNER','OWNER_PROOF_REQUIRED','RETRY_SAFE','Owner-scoped cleanup of already-published Outbox records.'),
  ('INBOX_CLEANUP','DOMAIN_OWNER','OWNER_PROOF_REQUIRED','RETRY_SAFE','Owner-scoped cleanup of durable Inbox deduplication records after the governed retention window.'),
  ('PROJECTION_REPAIR','DOMAIN_OWNER','OWNER_PROOF_REQUIRED','RETRY_SAFE','Rebuild or reconcile a derived projection from its durable source authority.'),
  ('DEAD_WORK_DISPOSITION','PLATFORM_OPERATIONS','IDEMPOTENT_WRITE','RETRY_SAFE','Create operator-visible disposition events for DEAD work; never silently requeue terminal work.'),
  ('TENANT_RETIREMENT','PLATFORM_OPERATIONS','OWNER_PROOF_REQUIRED','RETRY_SAFE','Complete Tenant retirement only after every required owner has durable success proof.')
ON CONFLICT (job_type) DO UPDATE SET
  owner_code=EXCLUDED.owner_code,
  side_effect_class=EXCLUDED.side_effect_class,
  lease_recovery=EXCLUDED.lease_recovery,
  description=EXCLUDED.description;

CREATE POLICY schedule_definitions_s23_seed ON core_registry.schedule_definitions
  FOR INSERT TO s1_core_migrator
  WITH CHECK (schedule_id IN (
    '0198d4b8-0000-7000-8000-000000000023'::uuid,
    '0198d4b8-0000-7000-8000-000000000024'::uuid
  ));

INSERT INTO core_registry.schedule_definitions(
  schedule_id,schedule_code,schedule_name,job_type,schedule_type,interval_seconds,timezone,misfire_policy,
  concurrency_policy,timeout_seconds,max_attempts,retry_policy,payload_template,enabled,next_fire_at,created_at,updated_at)
VALUES
  ('0198d4b8-0000-7000-8000-000000000023','platform_credential_expiry_scan','Platform credential expiry scan',
   'CERTIFICATE_EXPIRY_SCAN','INTERVAL',3600,'UTC','FIRE_ONCE','FORBID',60,5,
   '{"strategy":"exponential-jitter"}'::jsonb,'{"horizonHours":720}'::jsonb,true,now(),now(),now()),
  ('0198d4b8-0000-7000-8000-000000000024','platform_dead_work_disposition','Platform DEAD work disposition scan',
   'DEAD_WORK_DISPOSITION','INTERVAL',300,'UTC','FIRE_ONCE','FORBID',30,5,
   '{"strategy":"exponential-jitter"}'::jsonb,'{}'::jsonb,true,now(),now(),now());

DROP POLICY schedule_definitions_s23_seed ON core_registry.schedule_definitions;

CREATE TABLE core_registry.maintenance_events (
  event_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(event_id)),
  tenant_id uuid CHECK (tenant_id IS NULL OR core_registry.is_uuid_v7(tenant_id)),
  event_type text NOT NULL CHECK (event_type IN ('CREDENTIAL_EXPIRY','DEAD_WORK','RETIREMENT_INCOMPLETE')),
  source_type text NOT NULL CHECK (length(btrim(source_type)) BETWEEN 1 AND 128),
  source_id text NOT NULL CHECK (length(btrim(source_id)) BETWEEN 1 AND 512),
  severity text NOT NULL CHECK (severity IN ('INFO','WARNING','CRITICAL')),
  status text NOT NULL CHECK (status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
  action_code text NOT NULL CHECK (length(btrim(action_code)) BETWEEN 1 AND 128),
  dedup_key text NOT NULL UNIQUE CHECK (length(btrim(dedup_key)) BETWEEN 1 AND 1024),
  details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
  detected_at timestamptz NOT NULL,
  acknowledged_at timestamptz,
  resolved_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK (acknowledged_at IS NULL OR acknowledged_at >= detected_at),
  CHECK (resolved_at IS NULL OR resolved_at >= detected_at),
  CHECK ((status='OPEN' AND acknowledged_at IS NULL AND resolved_at IS NULL)
    OR (status='ACKNOWLEDGED' AND acknowledged_at IS NOT NULL AND resolved_at IS NULL)
    OR (status='RESOLVED' AND resolved_at IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE INDEX maintenance_events_open_idx
  ON core_registry.maintenance_events (status, severity, detected_at DESC)
  WHERE status <> 'RESOLVED';
CREATE INDEX maintenance_events_tenant_idx
  ON core_registry.maintenance_events (tenant_id, status, detected_at DESC);

CREATE TABLE core_registry.tenant_retirement_runs (
  retirement_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(retirement_id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  state text NOT NULL CHECK (state IN ('PENDING','RUNNING','INCOMPLETE','COMPLETED')),
  reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2048),
  requested_at timestamptz NOT NULL,
  started_at timestamptz,
  completed_at timestamptz,
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, retirement_id),
  FOREIGN KEY (tenant_id) REFERENCES iam.tenants(id),
  CHECK ((state='PENDING' AND started_at IS NULL AND completed_at IS NULL)
    OR (state IN ('RUNNING','INCOMPLETE') AND started_at IS NOT NULL AND completed_at IS NULL)
    OR (state='COMPLETED' AND started_at IS NOT NULL AND completed_at IS NOT NULL AND completed_at >= started_at)),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX tenant_retirement_one_open_idx
  ON core_registry.tenant_retirement_runs (tenant_id)
  WHERE state IN ('PENDING','RUNNING','INCOMPLETE');

CREATE TABLE core_registry.tenant_retirement_owner_steps (
  retirement_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(retirement_id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  owner_code text NOT NULL CHECK (owner_code IN ('IAM','REGISTRY','TELEMETRY','METRIC','ALARM','OUTBOUND_DELIVERY')),
  state text NOT NULL CHECK (state IN ('PENDING','RUNNING','SUCCEEDED','FAILED')),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  last_error_code text,
  proof jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(proof) = 'object'),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (retirement_id, owner_code),
  FOREIGN KEY (tenant_id, retirement_id) REFERENCES core_registry.tenant_retirement_runs(tenant_id, retirement_id),
  CHECK ((state='SUCCEEDED' AND proof <> '{}'::jsonb AND last_error_code IS NULL)
    OR (state='FAILED' AND last_error_code IS NOT NULL)
    OR state IN ('PENDING','RUNNING'))
);

CREATE TABLE core_registry.tenant_retirement_owner_attempts (
  attempt_id uuid PRIMARY KEY CHECK (core_registry.is_uuid_v7(attempt_id)),
  retirement_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(retirement_id)),
  tenant_id uuid NOT NULL CHECK (core_registry.is_uuid_v7(tenant_id)),
  owner_code text NOT NULL CHECK (owner_code IN ('IAM','REGISTRY','TELEMETRY','METRIC','ALARM','OUTBOUND_DELIVERY')),
  attempt_no integer NOT NULL CHECK (attempt_no > 0),
  result text NOT NULL CHECK (result IN ('SUCCEEDED','FAILED')),
  error_code text,
  proof jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(proof) = 'object'),
  recorded_at timestamptz NOT NULL,
  UNIQUE (retirement_id, owner_code, attempt_no),
  FOREIGN KEY (tenant_id, retirement_id) REFERENCES core_registry.tenant_retirement_runs(tenant_id, retirement_id),
  CHECK ((result='SUCCEEDED' AND proof <> '{}'::jsonb AND error_code IS NULL)
    OR (result='FAILED' AND error_code IS NOT NULL))
);

CREATE OR REPLACE FUNCTION core_registry.reject_tenant_retirement_owner_attempt_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $owner_attempt_immutable$
BEGIN
  RAISE EXCEPTION 'Tenant retirement owner attempts are append-only' USING ERRCODE = '23514';
END
$owner_attempt_immutable$;

CREATE TRIGGER tenant_retirement_owner_attempts_append_only
BEFORE UPDATE OR DELETE ON core_registry.tenant_retirement_owner_attempts
FOR EACH ROW EXECUTE FUNCTION core_registry.reject_tenant_retirement_owner_attempt_mutation();

CREATE OR REPLACE FUNCTION core_registry.validate_tenant_retirement_run_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $retirement_transition$
BEGIN
  IF OLD.tenant_id <> NEW.tenant_id OR OLD.retirement_id <> NEW.retirement_id OR OLD.reason <> NEW.reason OR OLD.requested_at <> NEW.requested_at THEN
    RAISE EXCEPTION 'Tenant retirement identity is immutable' USING ERRCODE = '23514';
  END IF;
  IF OLD.state = 'PENDING' AND NEW.state NOT IN ('PENDING','RUNNING','INCOMPLETE') THEN
    RAISE EXCEPTION 'PENDING Tenant retirement must start before completion' USING ERRCODE = '23514';
  ELSIF OLD.state = 'RUNNING' AND NEW.state NOT IN ('RUNNING','INCOMPLETE','COMPLETED') THEN
    RAISE EXCEPTION 'RUNNING Tenant retirement transition is invalid' USING ERRCODE = '23514';
  ELSIF OLD.state = 'INCOMPLETE' AND NEW.state NOT IN ('INCOMPLETE','RUNNING','COMPLETED') THEN
    RAISE EXCEPTION 'INCOMPLETE Tenant retirement may only retry or complete' USING ERRCODE = '23514';
  ELSIF OLD.state = 'COMPLETED' AND NEW.state <> 'COMPLETED' THEN
    RAISE EXCEPTION 'completed Tenant retirement is terminal' USING ERRCODE = '23514';
  END IF;
  IF NEW.state = 'COMPLETED' AND OLD.state <> 'COMPLETED' THEN
    IF (SELECT count(*) FROM core_registry.tenant_retirement_owner_steps s
        WHERE s.retirement_id=NEW.retirement_id AND s.tenant_id=NEW.tenant_id AND s.state='SUCCEEDED') <> 6 THEN
      RAISE EXCEPTION 'Tenant retirement cannot complete without all owner success proofs' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END
$retirement_transition$;

CREATE TRIGGER tenant_retirement_runs_validate_transition
BEFORE UPDATE ON core_registry.tenant_retirement_runs
FOR EACH ROW EXECUTE FUNCTION core_registry.validate_tenant_retirement_run_transition();

RESET ROLE;
GRANT SELECT ON iam.tenants TO s1_core_migrator;
SET LOCAL ROLE s1_core_migrator;

CREATE VIEW core_registry.tenant_policy_usage_view WITH (security_invoker = true) AS
SELECT
  tenant.id AS tenant_id,
  tenant.status AS tenant_status,
  tenant.revision AS tenant_revision,
  COALESCE(policy_counts.lifecycle_policy_count,0) AS lifecycle_policy_count,
  COALESCE(hold_counts.active_legal_hold_count,0) AS active_legal_hold_count,
  COALESCE(job_counts.ready_count,0) AS ready_work_count,
  COALESCE(job_counts.running_count,0) AS running_work_count,
  COALESCE(job_counts.retry_count,0) AS retry_work_count,
  COALESCE(job_counts.dead_count,0) AS dead_work_count,
  COALESCE(event_counts.open_event_count,0) AS open_maintenance_event_count
FROM iam.tenants tenant
LEFT JOIN LATERAL (
  SELECT count(*) AS lifecycle_policy_count FROM core_registry.data_lifecycle_policies p
  WHERE p.tenant_id=tenant.id AND p.status='RELEASED'
) policy_counts ON true
LEFT JOIN LATERAL (
  SELECT count(*) AS active_legal_hold_count FROM core_registry.legal_holds h
  WHERE h.tenant_id=tenant.id AND h.status='ACTIVE' AND h.effective_from <= now() AND (h.effective_to IS NULL OR now() < h.effective_to)
) hold_counts ON true
LEFT JOIN LATERAL (
  SELECT
    count(*) FILTER (WHERE j.state='READY') AS ready_count,
    count(*) FILTER (WHERE j.state IN ('CLAIMED','RUNNING')) AS running_count,
    count(*) FILTER (WHERE j.state='RETRY_WAIT') AS retry_count,
    count(*) FILTER (WHERE j.state='DEAD') AS dead_count
  FROM core_registry.job_instances j WHERE j.tenant_id=tenant.id
) job_counts ON true
LEFT JOIN LATERAL (
  SELECT count(*) AS open_event_count FROM core_registry.maintenance_events e
  WHERE e.tenant_id=tenant.id AND e.status <> 'RESOLVED'
) event_counts ON true;

CREATE POLICY job_instances_maintenance_worker_select ON core_registry.job_instances
  FOR SELECT TO maintenance_runtime
  USING (true);
CREATE POLICY job_instances_maintenance_worker_update ON core_registry.job_instances
  FOR UPDATE TO maintenance_runtime
  USING (job_type IN ('CERTIFICATE_EXPIRY_SCAN','DEAD_WORK_DISPOSITION','TENANT_RETIREMENT'))
  WITH CHECK (job_type IN ('CERTIFICATE_EXPIRY_SCAN','DEAD_WORK_DISPOSITION','TENANT_RETIREMENT'));
CREATE POLICY job_attempts_maintenance_worker ON core_registry.job_attempts
  FOR ALL TO maintenance_runtime
  USING (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id=job_attempts.job_id AND j.job_type IN ('CERTIFICATE_EXPIRY_SCAN','DEAD_WORK_DISPOSITION','TENANT_RETIREMENT')))
  WITH CHECK (EXISTS (SELECT 1 FROM core_registry.job_instances j WHERE j.job_id=job_attempts.job_id AND j.job_type IN ('CERTIFICATE_EXPIRY_SCAN','DEAD_WORK_DISPOSITION','TENANT_RETIREMENT')));

ALTER TABLE core_registry.maintenance_work_types ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.maintenance_work_types FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.maintenance_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.maintenance_events FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tenant_retirement_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tenant_retirement_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tenant_retirement_owner_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tenant_retirement_owner_steps FORCE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tenant_retirement_owner_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE core_registry.tenant_retirement_owner_attempts FORCE ROW LEVEL SECURITY;

CREATE POLICY data_lifecycle_policies_maintenance ON core_registry.data_lifecycle_policies FOR SELECT TO maintenance_runtime USING (true);
CREATE POLICY legal_holds_maintenance ON core_registry.legal_holds FOR SELECT TO maintenance_runtime USING (true);
CREATE POLICY maintenance_work_types_worker ON core_registry.maintenance_work_types FOR SELECT TO maintenance_runtime USING (true);
CREATE POLICY maintenance_events_worker ON core_registry.maintenance_events FOR ALL TO maintenance_runtime USING (true) WITH CHECK (true);
CREATE POLICY tenant_retirement_runs_worker ON core_registry.tenant_retirement_runs FOR ALL TO maintenance_runtime USING (true) WITH CHECK (true);
CREATE POLICY tenant_retirement_runs_iam_finalize ON core_registry.tenant_retirement_runs FOR SELECT TO s1_iam_migrator USING (true);
CREATE POLICY tenant_retirement_owner_steps_worker ON core_registry.tenant_retirement_owner_steps FOR ALL TO maintenance_runtime USING (true) WITH CHECK (true);
CREATE POLICY tenant_retirement_owner_attempts_worker ON core_registry.tenant_retirement_owner_attempts FOR ALL TO maintenance_runtime USING (true) WITH CHECK (true);

REVOKE ALL ON core_registry.maintenance_work_types, core_registry.maintenance_events,
  core_registry.tenant_retirement_runs, core_registry.tenant_retirement_owner_steps,
  core_registry.tenant_retirement_owner_attempts FROM PUBLIC;
GRANT SELECT ON core_registry.maintenance_work_types, core_registry.tenant_policy_usage_view TO maintenance_runtime;
GRANT SELECT,INSERT,UPDATE ON core_registry.maintenance_events, core_registry.tenant_retirement_runs,
  core_registry.tenant_retirement_owner_steps TO maintenance_runtime;
GRANT SELECT,INSERT ON core_registry.tenant_retirement_owner_attempts TO maintenance_runtime;
GRANT SELECT,UPDATE ON core_registry.job_instances TO maintenance_runtime;
GRANT SELECT,INSERT,UPDATE ON core_registry.job_attempts TO maintenance_runtime;
GRANT SELECT ON core_registry.legal_holds, core_registry.data_lifecycle_policies TO maintenance_runtime;

RESET ROLE;

GRANT SELECT ON iam.tenants, iam.api_credentials TO maintenance_runtime;
CREATE POLICY tenants_maintenance_retirement ON iam.tenants
  FOR SELECT TO maintenance_runtime USING (true);
CREATE POLICY tenants_iam_migrator_finalize ON iam.tenants
  FOR UPDATE TO s1_iam_migrator USING (true) WITH CHECK (true);
CREATE POLICY api_credentials_maintenance_expiry ON iam.api_credentials
  FOR SELECT TO maintenance_runtime USING (true);

-- Connectivity is installed into the same hvac_s1 database during Phase 1. The
-- maintenance identity receives read-only certificate metadata, never Secret material.
GRANT USAGE ON SCHEMA connectivity TO maintenance_runtime;
GRANT SELECT (id,tenant_id,credential_kind,status,valid_from,valid_until,certificate_fingerprint_sha256) ON connectivity.credential_refs TO maintenance_runtime;
CREATE POLICY connectivity_credential_refs_maintenance_expiry ON connectivity.credential_refs
  FOR SELECT TO maintenance_runtime USING (true);

GRANT USAGE ON SCHEMA core_registry TO s1_iam_migrator;
GRANT SELECT ON core_registry.tenant_retirement_runs TO s1_iam_migrator;
SET LOCAL ROLE s1_iam_migrator;
CREATE OR REPLACE FUNCTION iam.finalize_tenant_retirement(check_tenant_id uuid, check_retirement_id uuid, completed_at timestamptz)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam, core_registry
AS $finalize_tenant$
DECLARE
  next_revision bigint;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM core_registry.tenant_retirement_runs r
    WHERE r.tenant_id=check_tenant_id AND r.retirement_id=check_retirement_id AND r.state='COMPLETED'
  ) THEN
    RAISE EXCEPTION 'Tenant retirement run is not complete' USING ERRCODE = '23514';
  END IF;
  UPDATE iam.tenants
  SET status='RETIRED', revision=revision+1, updated_at=completed_at
  WHERE id=check_tenant_id AND status <> 'RETIRED'
  RETURNING revision INTO next_revision;
  IF next_revision IS NULL THEN
    SELECT revision INTO next_revision FROM iam.tenants WHERE id=check_tenant_id AND status='RETIRED';
  END IF;
  RETURN next_revision;
END
$finalize_tenant$;
REVOKE ALL ON FUNCTION iam.finalize_tenant_retirement(uuid,uuid,timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION iam.finalize_tenant_retirement(uuid,uuid,timestamptz) TO maintenance_runtime;

RESET ROLE;
COMMIT;

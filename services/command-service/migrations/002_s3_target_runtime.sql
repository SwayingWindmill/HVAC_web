BEGIN;
SET LOCAL ROLE s3_command_migrator;

CREATE TABLE IF NOT EXISTS command_runtime.connector_evidence (
  attempt_id uuid NOT NULL REFERENCES command_runtime.command_attempts(attempt_id),
  execution_fence bigint NOT NULL CHECK (execution_fence > 0),
  command_id uuid NOT NULL REFERENCES command_runtime.command_intents(command_id),
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  external_device_id text NOT NULL,
  payload_hash text NOT NULL,
  mapping_revision text NOT NULL,
  binding_revision text NOT NULL,
  provider_endpoint text NOT NULL,
  provider_method text NOT NULL,
  request_sha256 text NOT NULL,
  prepared_at timestamptz NOT NULL,
  provider_status_code integer,
  response_sha256 text,
  request_written boolean,
  connector_phase text CHECK (connector_phase IN ('PRE_SEND_REJECTED', 'REQUEST_COMMITTED', 'ACKNOWLEDGED')),
  failure_code text,
  completed_at timestamptz,
  PRIMARY KEY (attempt_id, execution_fence),
  UNIQUE (organization_id, device_id, execution_fence)
);

CREATE INDEX IF NOT EXISTS connector_evidence_command_idx
  ON command_runtime.connector_evidence (organization_id, command_id, prepared_at);

CREATE TABLE IF NOT EXISTS command_runtime.command_grant_uses (
  token_id text PRIMARY KEY,
  grant_id text NOT NULL,
  organization_id uuid NOT NULL,
  policy_revision text NOT NULL,
  emergency_revocation_revision bigint NOT NULL CHECK (emergency_revocation_revision >= 0),
  used_at timestamptz NOT NULL
);

ALTER TABLE command_runtime.connector_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.connector_evidence FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_grant_uses ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_grant_uses FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS connector_evidence_migrator_all ON command_runtime.connector_evidence;
CREATE POLICY connector_evidence_migrator_all ON command_runtime.connector_evidence
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS connector_evidence_runtime_org ON command_runtime.connector_evidence;
CREATE POLICY connector_evidence_runtime_org ON command_runtime.connector_evidence
  FOR ALL TO s3_command_runtime
  USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid)
  WITH CHECK (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

DROP POLICY IF EXISTS command_grant_uses_migrator_all ON command_runtime.command_grant_uses;
CREATE POLICY command_grant_uses_migrator_all ON command_runtime.command_grant_uses
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);

DROP POLICY IF EXISTS command_grant_uses_runtime_org ON command_runtime.command_grant_uses;
CREATE POLICY command_grant_uses_runtime_org ON command_runtime.command_grant_uses
  FOR ALL TO s3_command_runtime
  USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid)
  WITH CHECK (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON command_runtime.connector_evidence TO s3_command_runtime;
GRANT SELECT, INSERT ON command_runtime.command_grant_uses TO s3_command_runtime;

UPDATE command_runtime.capability_profiles
SET status = 'VERIFIED',
    maximum_delta = 1,
    risk_level = 'LOW',
    approval_policy = 'NONE',
    retry_policy = 'PRE_SEND_ONLY',
    connector_kind = 'THINGSBOARD_CE_4.3.1.3'
WHERE capability_name = 'SET_TEMPERATURE_SETPOINT'
  AND capability_revision = 'capability:set-temperature-setpoint:v1';

COMMIT;

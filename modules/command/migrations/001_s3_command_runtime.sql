BEGIN;
SET LOCAL ROLE s3_command_migrator;

-- The bootstrap identity migration creates and owns command_runtime.
-- Runtime migrations intentionally do not need database-level CREATE privilege.

CREATE TABLE IF NOT EXISTS command_runtime.capability_profiles (
  capability_name text NOT NULL,
  capability_revision text NOT NULL,
  status text NOT NULL CHECK (status IN ('DRAFT', 'VERIFIED', 'RETIRED')),
  canonical_unit text NOT NULL,
  minimum_value double precision NOT NULL,
  maximum_value double precision NOT NULL,
  maximum_delta double precision NOT NULL,
  risk_level text NOT NULL,
  approval_policy text NOT NULL,
  retry_policy text NOT NULL,
  connector_kind text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (capability_name, capability_revision)
);

CREATE TABLE IF NOT EXISTS command_runtime.device_control_state (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  next_command_sequence bigint NOT NULL DEFAULT 1 CHECK (next_command_sequence > 0),
  active_execution_fence bigint NOT NULL DEFAULT 0 CHECK (active_execution_fence >= 0),
  frozen_control_groups jsonb NOT NULL DEFAULT '[]'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, device_id)
);

CREATE TABLE IF NOT EXISTS command_runtime.command_intents (
  command_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  principal_id uuid NOT NULL,
  idempotency_key text NOT NULL,
  capability_name text NOT NULL,
  capability_revision text NOT NULL,
  risk_level text NOT NULL,
  risk_rule_revision text NOT NULL,
  approval_policy text NOT NULL,
  retry_policy text NOT NULL,
  canonical_parameters jsonb NOT NULL,
  verification_point_key text NOT NULL,
  payload_hash text NOT NULL,
  snapshot_revision bigint NOT NULL CHECK (snapshot_revision > 0),
  device_command_sequence bigint NOT NULL CHECK (device_command_sequence > 0),
  status text NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  active_execution_fence bigint NOT NULL DEFAULT 0 CHECK (active_execution_fence >= 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, device_id, device_command_sequence),
  FOREIGN KEY (capability_name, capability_revision)
    REFERENCES command_runtime.capability_profiles (capability_name, capability_revision)
);

CREATE TABLE IF NOT EXISTS command_runtime.command_idempotency (
  tenant_id uuid NOT NULL,
  device_id uuid NOT NULL,
  idempotency_key text NOT NULL,
  payload_hash text NOT NULL,
  command_id uuid NOT NULL REFERENCES command_runtime.command_intents(command_id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, device_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS command_runtime.command_authorization_snapshots (
  command_id uuid PRIMARY KEY REFERENCES command_runtime.command_intents(command_id),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  principal_id uuid NOT NULL,
  grant_id text NOT NULL,
  policy_revision text NOT NULL,
  authorization_purpose text NOT NULL,
  capability_name text NOT NULL,
  capability_revision text NOT NULL,
  maximum_risk text NOT NULL,
  emergency_revocation_revision bigint NOT NULL CHECK (emergency_revocation_revision >= 0),
  issued_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS command_runtime.command_risk_snapshots (
  command_id uuid PRIMARY KEY REFERENCES command_runtime.command_intents(command_id),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  risk_level text NOT NULL,
  rule_revision text NOT NULL,
  reasons jsonb NOT NULL,
  evaluated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS command_runtime.command_approval_snapshots (
  approval_id uuid PRIMARY KEY,
  command_id uuid NOT NULL REFERENCES command_runtime.command_intents(command_id),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  approver_id uuid NOT NULL,
  approver_role text NOT NULL,
  approval_policy text NOT NULL,
  payload_hash text NOT NULL,
  capability_revision text NOT NULL,
  risk_level text NOT NULL,
  risk_rule_revision text NOT NULL,
  authorization_grant_id text NOT NULL,
  authorization_policy_revision text NOT NULL,
  authorization_purpose text NOT NULL,
  authorization_maximum_risk text NOT NULL,
  authorization_emergency_revocation_revision bigint NOT NULL CHECK (authorization_emergency_revocation_revision >= 0),
  authorization_issued_at timestamptz NOT NULL,
  authorization_expires_at timestamptz NOT NULL,
  issued_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (command_id, approver_id)
);

CREATE TABLE IF NOT EXISTS command_runtime.command_attempts (
  attempt_id uuid PRIMARY KEY,
  command_id uuid NOT NULL REFERENCES command_runtime.command_intents(command_id),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  attempt_number integer NOT NULL CHECK (attempt_number > 0),
  status text NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  execution_fence bigint NOT NULL CHECK (execution_fence > 0),
  payload_hash text NOT NULL,
  lease_owner text NOT NULL,
  lease_until timestamptz NOT NULL,
  connector_evidence_id text,
  acknowledged_at timestamptz,
  verification_deadline timestamptz,
  verification_lease_owner text,
  verification_lease_until timestamptz,
  verification_evidence_id text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (command_id, attempt_number),
  UNIQUE (tenant_id, device_id, execution_fence)
);

ALTER TABLE command_runtime.command_attempts ADD COLUMN IF NOT EXISTS acknowledged_at timestamptz;
ALTER TABLE command_runtime.command_attempts ADD COLUMN IF NOT EXISTS verification_deadline timestamptz;
ALTER TABLE command_runtime.command_attempts ADD COLUMN IF NOT EXISTS verification_lease_owner text;
ALTER TABLE command_runtime.command_attempts ADD COLUMN IF NOT EXISTS verification_lease_until timestamptz;
ALTER TABLE command_runtime.command_attempts ADD COLUMN IF NOT EXISTS verification_evidence_id text;

CREATE TABLE IF NOT EXISTS command_runtime.command_transitions (
  transition_id uuid PRIMARY KEY,
  command_id uuid NOT NULL REFERENCES command_runtime.command_intents(command_id),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  command_version bigint NOT NULL CHECK (command_version > 0),
  from_status text,
  to_status text NOT NULL,
  reason text NOT NULL,
  actor_type text NOT NULL,
  actor_id text NOT NULL,
  causation_id text NOT NULL,
  evidence_id text,
  occurred_at timestamptz NOT NULL,
  UNIQUE (command_id, command_version)
);

CREATE TABLE IF NOT EXISTS command_runtime.command_dispatch_outbox (
  outbox_id uuid PRIMARY KEY,
  command_id uuid NOT NULL REFERENCES command_runtime.command_intents(command_id),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  command_version bigint NOT NULL,
  available_at timestamptz NOT NULL,
  lease_owner text,
  lease_until timestamptz,
  delivered_at timestamptz,
  attempt_count integer NOT NULL DEFAULT 0,
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (command_id, command_version)
);

CREATE TABLE IF NOT EXISTS command_runtime.command_audit_intents (
  audit_intent_id uuid PRIMARY KEY,
  command_id uuid NOT NULL REFERENCES command_runtime.command_intents(command_id),
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  event_kind text NOT NULL,
  payload_hash text NOT NULL,
  redacted_payload jsonb NOT NULL,
  created_at timestamptz NOT NULL,
  relayed_at timestamptz
);

CREATE INDEX IF NOT EXISTS command_dispatch_outbox_ready_idx
  ON command_runtime.command_dispatch_outbox (tenant_id, available_at, created_at)
  WHERE delivered_at IS NULL;
CREATE INDEX IF NOT EXISTS command_attempts_prepared_lease_idx
  ON command_runtime.command_attempts (tenant_id, lease_until, command_id)
  WHERE status = 'PREPARED';
CREATE INDEX IF NOT EXISTS command_intents_device_lane_idx
  ON command_runtime.command_intents (tenant_id, device_id, device_command_sequence, status);
CREATE INDEX IF NOT EXISTS command_attempts_verification_ready_idx
  ON command_runtime.command_attempts (tenant_id, verification_deadline, verification_lease_until, command_id)
  WHERE status = 'ACKNOWLEDGED';

INSERT INTO command_runtime.capability_profiles (
  capability_name, capability_revision, status, canonical_unit,
  minimum_value, maximum_value, maximum_delta, risk_level,
  approval_policy, retry_policy, connector_kind
) VALUES
  ('START', 'capability:start:v1', 'DRAFT', 'NONE', 0, 0, 0, 'MEDIUM', 'SINGLE_APPROVER', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('STOP', 'capability:stop:v1', 'DRAFT', 'NONE', 0, 0, 0, 'MEDIUM', 'SINGLE_APPROVER', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('RESET_FAULT', 'capability:reset-fault:v1', 'DRAFT', 'NONE', 0, 0, 0, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('SET_TEMPERATURE_SETPOINT', 'capability:set-temperature-setpoint:v1', 'DRAFT', 'CELSIUS', 16, 30, 3, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('SET_CHILLED_WATER_TEMPERATURE_SETPOINT', 'capability:set-chilled-water-temperature-setpoint:v1', 'DRAFT', 'CELSIUS', 5, 12, 3, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('SET_FREQUENCY', 'capability:set-frequency:v1', 'DRAFT', 'HERTZ', 20, 50, 10, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('SET_FAN_SPEED', 'capability:set-fan-speed:v1', 'DRAFT', 'PERCENT', 20, 100, 30, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('SET_LOAD_LIMIT', 'capability:set-load-limit:v1', 'DRAFT', 'PERCENT', 20, 100, 30, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('SET_OPENING', 'capability:set-opening:v1', 'DRAFT', 'PERCENT', 0, 100, 30, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY')
ON CONFLICT (capability_name, capability_revision) DO NOTHING;

ALTER TABLE command_runtime.device_control_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.device_control_state FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_intents FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_idempotency ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_idempotency FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_authorization_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_authorization_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_risk_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_risk_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_approval_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_approval_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_attempts FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_transitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_transitions FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_dispatch_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_dispatch_outbox FORCE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_audit_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.command_audit_intents FORCE ROW LEVEL SECURITY;

CREATE POLICY device_control_state_migrator_all ON command_runtime.device_control_state
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_intents_migrator_all ON command_runtime.command_intents
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_idempotency_migrator_all ON command_runtime.command_idempotency
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_authorization_snapshots_migrator_all ON command_runtime.command_authorization_snapshots
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_risk_snapshots_migrator_all ON command_runtime.command_risk_snapshots
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_approval_snapshots_migrator_all ON command_runtime.command_approval_snapshots
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_attempts_migrator_all ON command_runtime.command_attempts
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_transitions_migrator_all ON command_runtime.command_transitions
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_dispatch_outbox_migrator_all ON command_runtime.command_dispatch_outbox
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);
CREATE POLICY command_audit_intents_migrator_all ON command_runtime.command_audit_intents
  FOR ALL TO s3_command_migrator USING (true) WITH CHECK (true);

CREATE POLICY device_control_state_runtime_org ON command_runtime.device_control_state
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_intents_runtime_org ON command_runtime.command_intents
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_idempotency_runtime_org ON command_runtime.command_idempotency
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_authorization_snapshots_runtime_org ON command_runtime.command_authorization_snapshots
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_risk_snapshots_runtime_org ON command_runtime.command_risk_snapshots
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_approval_snapshots_runtime_org ON command_runtime.command_approval_snapshots
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_attempts_runtime_org ON command_runtime.command_attempts
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_transitions_runtime_org ON command_runtime.command_transitions
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_dispatch_outbox_runtime_org ON command_runtime.command_dispatch_outbox
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY command_audit_intents_runtime_org ON command_runtime.command_audit_intents
  FOR ALL TO s3_command_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON SCHEMA command_runtime FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA command_runtime FROM PUBLIC;
GRANT USAGE ON SCHEMA command_runtime TO s3_command_runtime;
GRANT SELECT ON command_runtime.capability_profiles TO s3_command_runtime;
GRANT SELECT, INSERT, UPDATE ON command_runtime.device_control_state TO s3_command_runtime;
GRANT SELECT, INSERT, UPDATE ON command_runtime.command_intents TO s3_command_runtime;
GRANT SELECT, INSERT ON command_runtime.command_idempotency TO s3_command_runtime;
GRANT SELECT, INSERT ON command_runtime.command_authorization_snapshots TO s3_command_runtime;
GRANT SELECT, INSERT ON command_runtime.command_risk_snapshots TO s3_command_runtime;
GRANT SELECT, INSERT ON command_runtime.command_approval_snapshots TO s3_command_runtime;
GRANT SELECT, INSERT, UPDATE ON command_runtime.command_attempts TO s3_command_runtime;
GRANT SELECT, INSERT ON command_runtime.command_transitions TO s3_command_runtime;
GRANT SELECT, INSERT, UPDATE ON command_runtime.command_dispatch_outbox TO s3_command_runtime;
GRANT SELECT, INSERT, UPDATE ON command_runtime.command_audit_intents TO s3_command_runtime;

COMMIT;

BEGIN;
SET LOCAL ROLE s1_iam_migrator;

CREATE TABLE iam.command_permissions (
  id uuid PRIMARY KEY CHECK (iam.is_uuid_v7(id)),
  principal_id uuid NOT NULL REFERENCES iam.principals(id),
  tenant_id uuid NOT NULL CHECK (iam.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (iam.is_uuid_v7(site_id)),
  device_id uuid NOT NULL CHECK (iam.is_uuid_v7(device_id)),
  capability text NOT NULL CHECK (capability IN ('START','STOP','RESET_FAULT','SET_TEMPERATURE_SETPOINT','SET_CHILLED_WATER_TEMPERATURE_SETPOINT','SET_FREQUENCY','SET_FAN_SPEED','SET_LOAD_LIMIT','SET_OPENING')),
  capability_revision text NOT NULL CHECK (char_length(btrim(capability_revision)) BETWEEN 1 AND 256),
  purpose text NOT NULL CHECK (purpose IN ('COMMAND_SUBMIT','COMMAND_APPROVE')),
  maximum_risk text NOT NULL CHECK (maximum_risk IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  effect text NOT NULL CHECK (effect IN ('ALLOW','DENY')),
  status text NOT NULL CHECK (status IN ('ACTIVE','SUSPENDED','REVOKED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (principal_id, tenant_id, site_id, device_id, capability, capability_revision, purpose, effect),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (updated_at >= created_at)
);

ALTER TABLE iam.command_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE iam.command_permissions FORCE ROW LEVEL SECURITY;
CREATE POLICY command_permissions_runtime_scope ON iam.command_permissions
  FOR SELECT TO s1_iam_runtime
  USING (principal_id = iam.current_principal_id() AND tenant_id = iam.current_tenant_id());
CREATE POLICY command_permissions_migrator_all ON iam.command_permissions
  FOR ALL TO s1_iam_migrator USING (true) WITH CHECK (true);
GRANT SELECT ON iam.command_permissions TO s1_iam_runtime;
REVOKE ALL ON iam.command_permissions FROM PUBLIC;
COMMIT;

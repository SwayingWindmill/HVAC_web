BEGIN;
SET LOCAL ROLE s3_command_migrator;

UPDATE command_runtime.capability_profiles
SET risk_level = 'HIGH',
    approval_policy = 'NONE'
WHERE capability_name IN ('START', 'STOP')
  AND capability_revision IN ('capability:start:v1', 'capability:stop:v1');

INSERT INTO command_runtime.capability_profiles (
  capability_name, capability_revision, status, canonical_unit,
  minimum_value, maximum_value, maximum_delta, risk_level,
  approval_policy, retry_policy, connector_kind
) VALUES
  ('START_UNIT_SEQUENCE', 'capability:start-unit-sequence:v1', 'DRAFT', 'NONE', 0, 0, 0, 'HIGH', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'),
  ('STOP_UNIT_SEQUENCE', 'capability:stop-unit-sequence:v1', 'DRAFT', 'NONE', 0, 0, 0, 'HIGH', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY')
ON CONFLICT (capability_name, capability_revision) DO UPDATE
SET status = EXCLUDED.status,
    canonical_unit = EXCLUDED.canonical_unit,
    minimum_value = EXCLUDED.minimum_value,
    maximum_value = EXCLUDED.maximum_value,
    maximum_delta = EXCLUDED.maximum_delta,
    risk_level = EXCLUDED.risk_level,
    approval_policy = EXCLUDED.approval_policy,
    retry_policy = EXCLUDED.retry_policy,
    connector_kind = EXCLUDED.connector_kind;

RESET ROLE;
COMMIT;

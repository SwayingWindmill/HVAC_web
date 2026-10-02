BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

-- Last IAM telemetry revocation fact applied per tenant.
CREATE TABLE IF NOT EXISTS telemetry_runtime.iam_revocation_cursors (
  tenant_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(tenant_id)),
  last_sequence bigint NOT NULL CHECK (last_sequence >= 0),
  updated_at timestamptz NOT NULL
);

ALTER TABLE telemetry_runtime.iam_revocation_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.iam_revocation_cursors FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS iam_revocation_cursors_migrator_all ON telemetry_runtime.iam_revocation_cursors;
CREATE POLICY iam_revocation_cursors_migrator_all ON telemetry_runtime.iam_revocation_cursors
  FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
DROP POLICY IF EXISTS iam_revocation_cursors_runtime_all ON telemetry_runtime.iam_revocation_cursors;
CREATE POLICY iam_revocation_cursors_runtime_all ON telemetry_runtime.iam_revocation_cursors
  FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE ON telemetry_runtime.iam_revocation_cursors TO s2_telemetry_runtime;

RESET ROLE;
COMMIT;

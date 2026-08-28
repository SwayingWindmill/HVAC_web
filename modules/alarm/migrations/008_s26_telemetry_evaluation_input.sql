BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

CREATE TABLE alarm_runtime.telemetry_evaluation_input (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid NOT NULL,
  business_revision bigint NOT NULL CHECK (business_revision > 0),
  event_id uuid NOT NULL,
  evaluated_at timestamptz NOT NULL,
  snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, site_id, device_id)
);

CREATE INDEX alarm_telemetry_evaluation_input_site_idx
  ON alarm_runtime.telemetry_evaluation_input (tenant_id, site_id, device_id);

ALTER TABLE alarm_runtime.telemetry_evaluation_input ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.telemetry_evaluation_input FORCE ROW LEVEL SECURITY;

CREATE POLICY alarm_telemetry_evaluation_input_migrator_all
  ON alarm_runtime.telemetry_evaluation_input
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
CREATE POLICY alarm_telemetry_evaluation_input_runtime_all
  ON alarm_runtime.telemetry_evaluation_input
  FOR ALL TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON alarm_runtime.telemetry_evaluation_input FROM PUBLIC, s4_alarm_runtime;
GRANT SELECT, INSERT, UPDATE ON alarm_runtime.telemetry_evaluation_input TO s4_alarm_runtime;

RESET ROLE;
COMMIT;

BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

-- V2.1.2 MQTT State/Event/Heartbeat are IoT Runtime evidence. They are not
-- Device master-data state and reported online never directly owns Cloud Presence.
CREATE TABLE IF NOT EXISTS telemetry_runtime.mqtt_gateway_evidence (
  message_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(message_id)),
  integration_instance_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(integration_instance_id)),
  tenant_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(site_id)),
  gateway_id text NOT NULL CHECK (length(btrim(gateway_id)) BETWEEN 1 AND 128),
  evidence_type text NOT NULL CHECK (evidence_type IN ('STATE','HEARTBEAT','SESSION','LWT')),
  observed_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL,
  source_sequence bigint NOT NULL CHECK (source_sequence >= 0),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  created_at timestamptz NOT NULL,
  CHECK (received_at >= observed_at - interval '24 hours')
);
CREATE INDEX IF NOT EXISTS mqtt_gateway_evidence_scope_idx
  ON telemetry_runtime.mqtt_gateway_evidence (tenant_id, site_id, gateway_id, observed_at DESC, message_id);

CREATE TABLE IF NOT EXISTS telemetry_runtime.mqtt_runtime_events (
  message_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(message_id)),
  integration_instance_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(integration_instance_id)),
  tenant_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(site_id)),
  gateway_id text NOT NULL CHECK (length(btrim(gateway_id)) BETWEEN 1 AND 128),
  event_type text NOT NULL CHECK (length(btrim(event_type)) BETWEEN 1 AND 128),
  source_type text NOT NULL CHECK (length(btrim(source_type)) BETWEEN 1 AND 64),
  source_id text NOT NULL CHECK (length(btrim(source_id)) BETWEEN 1 AND 256),
  event_time timestamptz NOT NULL,
  severity text NOT NULL CHECK (severity IN ('INFO','WARNING','CRITICAL')),
  source_sequence bigint NOT NULL CHECK (source_sequence >= 0),
  data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object'),
  received_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS mqtt_runtime_events_scope_idx
  ON telemetry_runtime.mqtt_runtime_events (tenant_id, site_id, event_time DESC, message_id);

ALTER TABLE telemetry_runtime.mqtt_gateway_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.mqtt_gateway_evidence FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.mqtt_runtime_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.mqtt_runtime_events FORCE ROW LEVEL SECURITY;
CREATE POLICY mqtt_gateway_evidence_migrator_all ON telemetry_runtime.mqtt_gateway_evidence FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY mqtt_gateway_evidence_runtime_all ON telemetry_runtime.mqtt_gateway_evidence FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY mqtt_runtime_events_migrator_all ON telemetry_runtime.mqtt_runtime_events FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY mqtt_runtime_events_runtime_all ON telemetry_runtime.mqtt_runtime_events FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
GRANT SELECT, INSERT ON telemetry_runtime.mqtt_gateway_evidence, telemetry_runtime.mqtt_runtime_events TO s2_telemetry_runtime;
REVOKE ALL ON telemetry_runtime.mqtt_gateway_evidence, telemetry_runtime.mqtt_runtime_events FROM PUBLIC;

RESET ROLE;
COMMIT;

BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

CREATE TABLE alarm_runtime.notification_outbox (
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  source_event_id uuid NOT NULL,
  alarm_id uuid NOT NULL,
  alarm_version bigint NOT NULL CHECK (alarm_version > 0),
  incident_correlation_id uuid NOT NULL,
  action text NOT NULL CHECK (action IN ('CREATED','SEVERITY_CHANGED','ACKNOWLEDGED','CLEARED')),
  current_severity text NOT NULL CHECK (current_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  peak_severity text NOT NULL CHECK (peak_severity IN ('INFO','WARNING','MINOR','MAJOR','CRITICAL')),
  condition text NOT NULL CHECK (condition IN ('ACTIVE','CLEARED')),
  attributes jsonb NOT NULL CHECK (jsonb_typeof(attributes) = 'object'),
  occurred_at timestamptz NOT NULL,
  state text NOT NULL CHECK (state IN ('READY','LEASED','DELIVERED')),
  lease_owner text,
  lease_until timestamptz,
  lease_fence bigint NOT NULL DEFAULT 0 CHECK (lease_fence >= 0),
  delivered_at timestamptz,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, source_event_id),
  UNIQUE (tenant_id, alarm_id, alarm_version, action),
  FOREIGN KEY (tenant_id, site_id, alarm_id) REFERENCES alarm_runtime.alarm_current (tenant_id, site_id, alarm_id),
  CHECK ((state = 'LEASED') = (lease_owner IS NOT NULL AND lease_until IS NOT NULL)),
  CHECK ((state = 'DELIVERED') = (delivered_at IS NOT NULL))
);

CREATE INDEX alarm_notification_outbox_ready_idx
  ON alarm_runtime.notification_outbox (state, lease_until, created_at, source_event_id)
  WHERE state IN ('READY','LEASED');

ALTER TABLE alarm_runtime.notification_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.notification_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY alarm_notification_outbox_runtime_tenant ON alarm_runtime.notification_outbox
  FOR INSERT TO s4_alarm_runtime
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_notification_outbox_runtime_read ON alarm_runtime.notification_outbox
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY alarm_notification_outbox_relay_all ON alarm_runtime.notification_outbox
  FOR ALL TO s4_alarm_notification_relay
  USING (true)
  WITH CHECK (true);

REVOKE ALL ON alarm_runtime.notification_outbox FROM PUBLIC, s4_alarm_runtime, s4_alarm_notification_relay;
GRANT USAGE ON SCHEMA alarm_runtime TO s4_alarm_notification_relay;
GRANT SELECT, INSERT ON alarm_runtime.notification_outbox TO s4_alarm_runtime;
GRANT SELECT, UPDATE ON alarm_runtime.notification_outbox TO s4_alarm_notification_relay;

RESET ROLE;
COMMIT;

BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

CREATE TABLE alarm_runtime.events (
  event_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid,
  point_id uuid,
  event_type text NOT NULL CHECK (length(btrim(event_type)) BETWEEN 1 AND 128),
  severity text NOT NULL CHECK (length(btrim(severity)) BETWEEN 1 AND 32),
  message text NOT NULL CHECK (length(btrim(message)) BETWEEN 1 AND 2048),
  start_time timestamptz NOT NULL,
  end_time timestamptz,
  status text NOT NULL CHECK (length(btrim(status)) BETWEEN 1 AND 64),
  UNIQUE (tenant_id, site_id, event_id),
  CHECK (end_time IS NULL OR end_time >= start_time)
);

CREATE INDEX event_site_time_idx
  ON alarm_runtime.events (tenant_id, site_id, start_time DESC, event_id);
CREATE INDEX event_device_time_idx
  ON alarm_runtime.events (tenant_id, site_id, device_id, start_time DESC)
  WHERE device_id IS NOT NULL;
CREATE INDEX event_point_time_idx
  ON alarm_runtime.events (tenant_id, site_id, point_id, start_time DESC)
  WHERE point_id IS NOT NULL;

ALTER TABLE alarm_runtime.events ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.events FORCE ROW LEVEL SECURITY;
CREATE POLICY events_migrator_all ON alarm_runtime.events
  FOR ALL TO s4_alarm_migrator USING (true) WITH CHECK (true);
CREATE POLICY events_runtime_scope ON alarm_runtime.events
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON alarm_runtime.events FROM PUBLIC;
GRANT SELECT ON alarm_runtime.events TO s4_alarm_runtime;

ALTER TABLE alarm_runtime.alarm_current
  ADD COLUMN event_id uuid,
  ADD COLUMN point_id uuid,
  ADD COLUMN alarm_type text;

ALTER TABLE alarm_runtime.alarm_current
  ADD CONSTRAINT alarm_current_alarm_type_nonempty
    CHECK (alarm_type IS NULL OR length(btrim(alarm_type)) BETWEEN 1 AND 128),
  ADD CONSTRAINT alarm_current_event_scope_fk
    FOREIGN KEY (tenant_id, site_id, event_id)
    REFERENCES alarm_runtime.events (tenant_id, site_id, event_id);

CREATE INDEX alarm_current_event_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, event_id)
  WHERE event_id IS NOT NULL;
CREATE INDEX alarm_current_point_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, point_id, last_occurred_at DESC)
  WHERE point_id IS NOT NULL;

RESET ROLE;
COMMIT;

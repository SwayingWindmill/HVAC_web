BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

CREATE TABLE IF NOT EXISTS alarm_runtime.alarm_current (
  alarm_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  site_id uuid NOT NULL,
  device_id uuid,
  source_type text NOT NULL CHECK (source_type IN ('DEVICE_RULE', 'SITE_RULE', 'EXTERNAL')),
  source_reference text NOT NULL CHECK (length(btrim(source_reference)) > 0),
  title text NOT NULL CHECK (length(btrim(title)) > 0),
  summary text NOT NULL CHECK (length(btrim(summary)) > 0),
  severity text NOT NULL CHECK (severity IN ('INFO', 'WARNING', 'MAJOR', 'CRITICAL')),
  status text NOT NULL CHECK (status IN ('OPEN', 'ACKNOWLEDGED', 'SUPPRESSED', 'CLOSED')),
  occurrence_count bigint NOT NULL CHECK (occurrence_count > 0),
  first_occurred_at timestamptz NOT NULL,
  last_occurred_at timestamptz NOT NULL CHECK (last_occurred_at >= first_occurred_at),
  evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'array'),
  transitions jsonb NOT NULL CHECK (jsonb_typeof(transitions) = 'array' AND jsonb_array_length(transitions) > 0),
  version bigint NOT NULL CHECK (version > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL CHECK (updated_at >= created_at AND updated_at >= last_occurred_at),
  UNIQUE (tenant_id, site_id, alarm_id)
);

CREATE INDEX IF NOT EXISTS alarm_current_site_activity_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, last_occurred_at DESC, alarm_id ASC);
CREATE INDEX IF NOT EXISTS alarm_current_site_status_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, status, last_occurred_at DESC);
CREATE INDEX IF NOT EXISTS alarm_current_site_severity_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, severity, last_occurred_at DESC);
CREATE INDEX IF NOT EXISTS alarm_current_device_idx
  ON alarm_runtime.alarm_current (tenant_id, site_id, device_id, last_occurred_at DESC)
  WHERE device_id IS NOT NULL;

ALTER TABLE alarm_runtime.alarm_current ENABLE ROW LEVEL SECURITY;
ALTER TABLE alarm_runtime.alarm_current FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS alarm_current_runtime_tenant ON alarm_runtime.alarm_current;
CREATE POLICY alarm_current_runtime_tenant ON alarm_runtime.alarm_current
  FOR SELECT TO s4_alarm_runtime
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON SCHEMA alarm_runtime FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA alarm_runtime FROM PUBLIC;
GRANT USAGE ON SCHEMA alarm_runtime TO s4_alarm_runtime;
GRANT SELECT ON alarm_runtime.alarm_current TO s4_alarm_runtime;

COMMIT;

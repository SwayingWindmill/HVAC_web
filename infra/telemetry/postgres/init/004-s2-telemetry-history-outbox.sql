BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

CREATE TABLE IF NOT EXISTS telemetry_runtime.telemetry_history_outbox (
  event_id uuid PRIMARY KEY REFERENCES telemetry_runtime.source_observations(observation_id) ON DELETE CASCADE
    CHECK (telemetry_runtime.is_uuid_v7(event_id)),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  outbox_payload_sha256 text NOT NULL CHECK (outbox_payload_sha256 ~ '^[a-f0-9]{64}$'),
  delivery_state text NOT NULL CHECK (delivery_state IN ('PENDING', 'IN_FLIGHT', 'PUBLISHED', 'DEAD')),
  available_at timestamptz NOT NULL,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error_code text,
  lease_id uuid CHECK (lease_id IS NULL OR telemetry_runtime.is_uuid_v7(lease_id)),
  leased_until timestamptz,
  published_at timestamptz,
  created_at timestamptz NOT NULL,
  CHECK ((delivery_state = 'IN_FLIGHT') = (lease_id IS NOT NULL AND leased_until IS NOT NULL)),
  CHECK ((delivery_state = 'PUBLISHED' AND published_at IS NOT NULL) OR (delivery_state <> 'PUBLISHED' AND published_at IS NULL)),
  CHECK (last_error_code IS NULL OR char_length(last_error_code) BETWEEN 1 AND 128)
);

CREATE INDEX IF NOT EXISTS telemetry_history_outbox_pending_idx
  ON telemetry_runtime.telemetry_history_outbox (available_at, event_id)
  WHERE delivery_state = 'PENDING';
CREATE INDEX IF NOT EXISTS telemetry_history_outbox_expired_lease_idx
  ON telemetry_runtime.telemetry_history_outbox (leased_until, event_id)
  WHERE delivery_state = 'IN_FLIGHT';

ALTER TABLE telemetry_runtime.telemetry_history_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.telemetry_history_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY telemetry_history_outbox_migrator_all
  ON telemetry_runtime.telemetry_history_outbox FOR ALL TO s2_telemetry_migrator
  USING (true) WITH CHECK (true);
CREATE POLICY telemetry_history_outbox_runtime_insert
  ON telemetry_runtime.telemetry_history_outbox FOR INSERT TO s2_telemetry_runtime
  WITH CHECK (delivery_state = 'PENDING' AND attempts = 0 AND lease_id IS NULL AND leased_until IS NULL AND published_at IS NULL);
CREATE POLICY telemetry_history_outbox_history_select
  ON telemetry_runtime.telemetry_history_outbox FOR SELECT TO s2_telemetry_history
  USING (true);
CREATE POLICY telemetry_history_outbox_history_update
  ON telemetry_runtime.telemetry_history_outbox FOR UPDATE TO s2_telemetry_history
  USING (true) WITH CHECK (true);

GRANT INSERT ON telemetry_runtime.telemetry_history_outbox TO s2_telemetry_runtime;
GRANT SELECT ON telemetry_runtime.telemetry_history_outbox TO s2_telemetry_history;
GRANT UPDATE (delivery_state, available_at, attempts, last_error_code, lease_id, leased_until, published_at)
  ON telemetry_runtime.telemetry_history_outbox TO s2_telemetry_history;
REVOKE ALL ON telemetry_runtime.telemetry_history_outbox FROM PUBLIC;

RESET ROLE;
COMMIT;

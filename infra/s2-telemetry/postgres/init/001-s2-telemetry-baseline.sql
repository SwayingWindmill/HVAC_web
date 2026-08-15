BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

CREATE OR REPLACE FUNCTION telemetry_runtime.is_uuid_v7(value uuid)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
  SELECT substring(value::text FROM 15 FOR 1) = '7'
     AND substring(value::text FROM 20 FOR 1) IN ('8', '9', 'a', 'b')
$$;

REVOKE ALL ON FUNCTION telemetry_runtime.is_uuid_v7(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION telemetry_runtime.is_uuid_v7(uuid) TO s2_telemetry_runtime, s2_telemetry_relay, s2_telemetry_history;

CREATE TABLE IF NOT EXISTS telemetry_runtime.registry_device_bindings (
  device_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(device_id)),
  tenant_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(integration_instance_id)),
  external_entity_type text NOT NULL CHECK (external_entity_type IN ('DEVICE', 'ASSET')),
  external_id text NOT NULL CHECK (char_length(external_id) BETWEEN 1 AND 512),
  binding_status text NOT NULL CHECK (binding_status IN ('ACTIVE', 'QUARANTINED', 'RETIRED')),
  binding_revision bigint NOT NULL CHECK (binding_revision >= 1),
  source_registry_revision bigint NOT NULL CHECK (source_registry_revision >= 1),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  updated_at timestamptz NOT NULL,
  CHECK (valid_to IS NULL OR valid_to > valid_from)
);

CREATE UNIQUE INDEX IF NOT EXISTS registry_device_bindings_active_external_key_uidx
  ON telemetry_runtime.registry_device_bindings (integration_instance_id, external_entity_type, external_id)
  WHERE binding_status = 'ACTIVE' AND valid_to IS NULL;
CREATE INDEX IF NOT EXISTS registry_device_bindings_tenant_idx
  ON telemetry_runtime.registry_device_bindings (tenant_id, site_id, device_id);

CREATE TABLE IF NOT EXISTS telemetry_runtime.iam_scope_projections (
  projection_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(projection_id)),
  tenant_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(tenant_id)),
  principal_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(principal_id)),
  site_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(site_id)),
  device_id uuid NOT NULL REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  telemetry_key text CHECK (telemetry_key IS NULL OR telemetry_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  action text NOT NULL CHECK (action IN ('SNAPSHOT_READ', 'BATCH_READ', 'SUBSCRIBE', 'CURSOR_USE', 'CURSOR_CHECKPOINT')),
  decision text NOT NULL CHECK (decision IN ('ALLOW', 'DENY')),
  policy_revision bigint NOT NULL CHECK (policy_revision >= 1),
  source_event_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(source_event_id)),
  valid_until timestamptz NOT NULL,
  revoked_at timestamptz,
  updated_at timestamptz NOT NULL,
  CHECK (revoked_at IS NULL OR revoked_at <= valid_until)
);

CREATE UNIQUE INDEX IF NOT EXISTS iam_scope_projections_identity_uidx
  ON telemetry_runtime.iam_scope_projections
  (tenant_id, principal_id, device_id, telemetry_key, action, policy_revision) NULLS NOT DISTINCT;
CREATE INDEX IF NOT EXISTS iam_scope_projections_lookup_idx
  ON telemetry_runtime.iam_scope_projections
  (tenant_id, principal_id, device_id, action, valid_until)
  WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS telemetry_runtime.presence_policies (
  device_id uuid PRIMARY KEY REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  policy_revision bigint NOT NULL CHECK (policy_revision >= 1),
  online_within_seconds integer NOT NULL CHECK (online_within_seconds BETWEEN 1 AND 86400),
  offline_after_seconds integer NOT NULL CHECK (offline_after_seconds > online_within_seconds AND offline_after_seconds <= 604800),
  coverage_required boolean NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS telemetry_runtime.freshness_policies (
  device_id uuid NOT NULL REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  telemetry_key text NOT NULL CHECK (telemetry_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  policy_revision bigint NOT NULL CHECK (policy_revision >= 1),
  fresh_within_seconds integer NOT NULL CHECK (fresh_within_seconds BETWEEN 1 AND 604800),
  configured boolean NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (device_id, telemetry_key)
);

CREATE TABLE IF NOT EXISTS telemetry_runtime.source_positions (
  integration_instance_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(integration_instance_id)),
  source_partition text NOT NULL CHECK (char_length(source_partition) BETWEEN 1 AND 256),
  source_offset bigint NOT NULL CHECK (source_offset >= 0),
  source_event_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(source_event_id)),
  observed_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (integration_instance_id, source_partition),
  UNIQUE (integration_instance_id, source_event_id)
);

CREATE TABLE IF NOT EXISTS telemetry_runtime.source_observations (
  observation_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(observation_id)),
  integration_instance_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(integration_instance_id)),
  source_event_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(source_event_id)),
  source_partition text NOT NULL CHECK (char_length(source_partition) BETWEEN 1 AND 256),
  source_offset bigint NOT NULL CHECK (source_offset >= 0),
  device_id uuid REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  telemetry_key text NOT NULL CHECK (telemetry_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  value jsonb,
  value_type text CHECK (value_type IS NULL OR value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')),
  unit text CHECK (unit IS NULL OR char_length(unit) BETWEEN 1 AND 64),
  sampled_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL,
  acceptance_status text NOT NULL CHECK (acceptance_status IN ('ACCEPTED', 'REJECTED', 'QUARANTINED', 'DUPLICATE', 'OUT_OF_ORDER')),
  quality_reasons text[] NOT NULL DEFAULT '{}',
  payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[a-f0-9]{64}$'),
  created_at timestamptz NOT NULL,
  UNIQUE (integration_instance_id, source_event_id),
  UNIQUE (integration_instance_id, source_partition, source_offset),
  CHECK (received_at >= sampled_at - interval '24 hours')
);

CREATE INDEX IF NOT EXISTS source_observations_device_key_time_idx
  ON telemetry_runtime.source_observations (device_id, telemetry_key, sampled_at DESC, observation_id)
  WHERE device_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS telemetry_runtime.ingest_quarantine (
  quarantine_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(quarantine_id)),
  integration_instance_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(integration_instance_id)),
  external_entity_type text NOT NULL CHECK (external_entity_type IN ('DEVICE', 'ASSET')),
  external_id text NOT NULL CHECK (char_length(external_id) BETWEEN 1 AND 512),
  device_id uuid REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  telemetry_key text CHECK (telemetry_key IS NULL OR telemetry_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  reason_code text NOT NULL CHECK (reason_code IN ('MAPPING_NOT_FOUND', 'MAPPING_CONFLICT', 'MAPPING_QUARANTINED', 'MAPPING_RETIRED', 'SOURCE_UNTRUSTED', 'TYPE_MISMATCH', 'UNIT_MISMATCH', 'OUT_OF_RANGE', 'CLOCK_AHEAD', 'CLOCK_BEHIND')),
  evidence jsonb NOT NULL,
  detected_at timestamptz NOT NULL,
  resolved_at timestamptz,
  resolution text,
  CHECK ((resolved_at IS NULL AND resolution IS NULL) OR (resolved_at IS NOT NULL AND resolution IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS ingest_quarantine_open_idx
  ON telemetry_runtime.ingest_quarantine (integration_instance_id, external_entity_type, external_id, detected_at)
  WHERE resolved_at IS NULL;

CREATE TABLE IF NOT EXISTS telemetry_runtime.latest_accepted_telemetry (
  device_id uuid NOT NULL REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  telemetry_key text NOT NULL CHECK (telemetry_key ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
  business_revision bigint NOT NULL CHECK (business_revision >= 1),
  value jsonb NOT NULL,
  value_type text NOT NULL CHECK (value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')),
  unit text CHECK (unit IS NULL OR char_length(unit) BETWEEN 1 AND 64),
  sampled_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL,
  freshness text NOT NULL CHECK (freshness IN ('FRESH', 'STALE')),
  quality text NOT NULL CHECK (quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL', 'STALE', 'INVALID')),
  quality_reasons text[] NOT NULL DEFAULT '{}',
  policy_revision bigint NOT NULL CHECK (policy_revision >= 1),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (device_id, telemetry_key),
  CHECK (received_at >= sampled_at - interval '24 hours')
);

CREATE TABLE IF NOT EXISTS telemetry_runtime.device_presence (
  device_id uuid PRIMARY KEY REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  business_revision bigint NOT NULL CHECK (business_revision >= 1),
  applicability text NOT NULL CHECK (applicability IN ('APPLICABLE', 'NOT_APPLICABLE')),
  current_state text CHECK (current_state IS NULL OR current_state IN ('ONLINE', 'OFFLINE', 'UNKNOWN')),
  last_seen_at timestamptz,
  evaluated_at timestamptz NOT NULL,
  policy_revision bigint CHECK (policy_revision IS NULL OR policy_revision >= 1),
  last_known jsonb,
  updated_at timestamptz NOT NULL,
  CHECK ((applicability = 'NOT_APPLICABLE' AND current_state IS NULL) OR applicability = 'APPLICABLE')
);

CREATE TABLE IF NOT EXISTS telemetry_runtime.device_observation_snapshots (
  device_id uuid PRIMARY KEY REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  business_revision bigint NOT NULL CHECK (business_revision >= 1),
  evaluated_at timestamptz NOT NULL,
  evaluation_availability text NOT NULL CHECK (evaluation_availability IN ('AVAILABLE', 'UNAVAILABLE')),
  availability_reasons text[] NOT NULL DEFAULT '{}',
  telemetry_readiness text NOT NULL CHECK (telemetry_readiness IN ('CURRENT', 'DEGRADED', 'INCOMPLETE', 'NOT_APPLICABLE')),
  display_state text CHECK (display_state IS NULL OR display_state IN ('ONLINE', 'OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE')),
  snapshot jsonb NOT NULL,
  snapshot_sha256 text NOT NULL CHECK (snapshot_sha256 ~ '^[a-f0-9]{64}$'),
  updated_at timestamptz NOT NULL,
  UNIQUE (device_id, business_revision)
);

CREATE TABLE IF NOT EXISTS telemetry_runtime.telemetry_subscriptions (
  subscription_id text PRIMARY KEY CHECK (char_length(subscription_id) BETWEEN 16 AND 256 AND subscription_id ~ '^[A-Za-z0-9_-]+$'),
  client_subscription_id text NOT NULL CHECK (char_length(client_subscription_id) BETWEEN 1 AND 128 AND client_subscription_id ~ '^[A-Za-z0-9_.:-]+$'),
  principal_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(principal_id)),
  tenant_id uuid NOT NULL CHECK (telemetry_runtime.is_uuid_v7(tenant_id)),
  device_id uuid NOT NULL REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  keys jsonb NOT NULL CHECK (jsonb_typeof(keys) = 'array'),
  scope_sha256 text NOT NULL CHECK (scope_sha256 ~ '^[a-f0-9]{64}$'),
  policy_revision bigint NOT NULL CHECK (policy_revision >= 1),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'REVOKED', 'EXPIRED')),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (principal_id, client_subscription_id),
  CHECK (expires_at > created_at),
  CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX IF NOT EXISTS telemetry_subscriptions_active_device_idx
  ON telemetry_runtime.telemetry_subscriptions (device_id, expires_at, subscription_id)
  WHERE status = 'ACTIVE';

CREATE TABLE IF NOT EXISTS telemetry_runtime.recovery_cursors (
  cursor_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(cursor_id)),
  subscription_id text NOT NULL REFERENCES telemetry_runtime.telemetry_subscriptions(subscription_id),
  business_revision bigint NOT NULL CHECK (business_revision >= 1),
  transport_epoch text NOT NULL CHECK (char_length(transport_epoch) BETWEEN 1 AND 128),
  transport_offset bigint NOT NULL CHECK (transport_offset >= 0),
  scope_sha256 text NOT NULL CHECK (scope_sha256 ~ '^[a-f0-9]{64}$'),
  cursor_sha256 text NOT NULL UNIQUE CHECK (cursor_sha256 ~ '^[a-f0-9]{64}$'),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL,
  CHECK (expires_at > created_at),
  CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX IF NOT EXISTS recovery_cursors_subscription_expiry_idx
  ON telemetry_runtime.recovery_cursors (subscription_id, expires_at DESC)
  WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS telemetry_runtime.telemetry_publication_outbox (
  event_id uuid PRIMARY KEY CHECK (telemetry_runtime.is_uuid_v7(event_id)),
  device_id uuid NOT NULL REFERENCES telemetry_runtime.registry_device_bindings(device_id),
  business_revision bigint NOT NULL CHECK (business_revision >= 1),
  subscription_id text REFERENCES telemetry_runtime.telemetry_subscriptions(subscription_id),
  event_family text NOT NULL CHECK (event_family = 'hvac.telemetry.device-snapshot.v1'),
  payload jsonb NOT NULL,
  payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[a-f0-9]{64}$'),
  delivery_state text NOT NULL CHECK (delivery_state IN ('PENDING', 'PUBLISHED', 'DEAD')),
  available_at timestamptz NOT NULL,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error_code text,
  published_at timestamptz,
  created_at timestamptz NOT NULL,
  UNIQUE NULLS NOT DISTINCT (device_id, business_revision, subscription_id),
  CHECK ((delivery_state = 'PUBLISHED' AND published_at IS NOT NULL) OR delivery_state <> 'PUBLISHED')
);

CREATE INDEX IF NOT EXISTS telemetry_publication_outbox_pending_idx
  ON telemetry_runtime.telemetry_publication_outbox (available_at, event_id)
  WHERE delivery_state = 'PENDING';

ALTER TABLE telemetry_runtime.registry_device_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.registry_device_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.iam_scope_projections ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.iam_scope_projections FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.presence_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.presence_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.freshness_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.freshness_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.source_positions ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.source_positions FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.source_observations ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.source_observations FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.ingest_quarantine ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.ingest_quarantine FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.latest_accepted_telemetry ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.latest_accepted_telemetry FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.device_presence ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.device_presence FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.device_observation_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.device_observation_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.telemetry_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.telemetry_subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.recovery_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.recovery_cursors FORCE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE telemetry_runtime.telemetry_publication_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY registry_device_bindings_migrator_all ON telemetry_runtime.registry_device_bindings FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY registry_device_bindings_runtime_all ON telemetry_runtime.registry_device_bindings FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY iam_scope_projections_migrator_all ON telemetry_runtime.iam_scope_projections FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY iam_scope_projections_runtime_all ON telemetry_runtime.iam_scope_projections FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY presence_policies_migrator_all ON telemetry_runtime.presence_policies FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY presence_policies_runtime_all ON telemetry_runtime.presence_policies FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY freshness_policies_migrator_all ON telemetry_runtime.freshness_policies FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY freshness_policies_runtime_all ON telemetry_runtime.freshness_policies FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY source_positions_migrator_all ON telemetry_runtime.source_positions FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY source_positions_runtime_all ON telemetry_runtime.source_positions FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY source_observations_migrator_all ON telemetry_runtime.source_observations FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY source_observations_runtime_all ON telemetry_runtime.source_observations FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY ingest_quarantine_migrator_all ON telemetry_runtime.ingest_quarantine FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY ingest_quarantine_runtime_all ON telemetry_runtime.ingest_quarantine FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY latest_accepted_telemetry_migrator_all ON telemetry_runtime.latest_accepted_telemetry FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY latest_accepted_telemetry_runtime_all ON telemetry_runtime.latest_accepted_telemetry FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY device_presence_migrator_all ON telemetry_runtime.device_presence FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY device_presence_runtime_all ON telemetry_runtime.device_presence FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY device_observation_snapshots_migrator_all ON telemetry_runtime.device_observation_snapshots FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY device_observation_snapshots_runtime_all ON telemetry_runtime.device_observation_snapshots FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY telemetry_subscriptions_migrator_all ON telemetry_runtime.telemetry_subscriptions FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY telemetry_subscriptions_runtime_all ON telemetry_runtime.telemetry_subscriptions FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY recovery_cursors_migrator_all ON telemetry_runtime.recovery_cursors FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY recovery_cursors_runtime_all ON telemetry_runtime.recovery_cursors FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY telemetry_publication_outbox_migrator_all ON telemetry_runtime.telemetry_publication_outbox FOR ALL TO s2_telemetry_migrator USING (true) WITH CHECK (true);
CREATE POLICY telemetry_publication_outbox_runtime_all ON telemetry_runtime.telemetry_publication_outbox FOR ALL TO s2_telemetry_runtime USING (true) WITH CHECK (true);
CREATE POLICY telemetry_publication_outbox_relay_select ON telemetry_runtime.telemetry_publication_outbox FOR SELECT TO s2_telemetry_relay USING (true);
CREATE POLICY telemetry_publication_outbox_relay_update ON telemetry_runtime.telemetry_publication_outbox FOR UPDATE TO s2_telemetry_relay USING (true) WITH CHECK (true);

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA telemetry_runtime TO s2_telemetry_runtime;
GRANT SELECT ON telemetry_runtime.telemetry_publication_outbox TO s2_telemetry_relay;
GRANT UPDATE (delivery_state, available_at, attempts, last_error_code, published_at)
  ON telemetry_runtime.telemetry_publication_outbox TO s2_telemetry_relay;
REVOKE ALL ON ALL TABLES IN SCHEMA telemetry_runtime FROM PUBLIC;

RESET ROLE;
COMMIT;

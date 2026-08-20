\set ON_ERROR_STOP on

BEGIN;
SET LOCAL ROLE connectivity_migrator;

CREATE TABLE connectivity.edge_nodes (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  site_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(site_id)),
  integration_instance_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(integration_instance_id)),
  edge_external_id text NOT NULL CHECK (length(btrim(edge_external_id)) BETWEEN 1 AND 128),
  hardware_identity_sha256 text NOT NULL CHECK (hardware_identity_sha256 ~ '^[a-f0-9]{64}$'),
  status text NOT NULL CHECK (status IN ('ACTIVE','READ_ONLY','UPGRADE_REQUIRED','SUSPENDED','RETIRED')),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, integration_instance_id),
  UNIQUE (tenant_id, site_id, edge_external_id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES core_registry.sites(tenant_id, id),
  FOREIGN KEY (tenant_id, integration_instance_id) REFERENCES connectivity.integration_instances(tenant_id, id),
  CHECK (updated_at >= created_at)
);

CREATE TABLE connectivity.edge_enrollments (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  hardware_identity_sha256 text NOT NULL CHECK (hardware_identity_sha256 ~ '^[a-f0-9]{64}$'),
  challenge_hash_sha256 text NOT NULL CHECK (challenge_hash_sha256 ~ '^[a-f0-9]{64}$'),
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  credential_ref_id uuid,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  FOREIGN KEY (tenant_id, credential_ref_id) REFERENCES connectivity.credential_refs(tenant_id, id),
  CHECK (expires_at > created_at),
  CHECK (consumed_at IS NULL OR consumed_at <= expires_at),
  CHECK ((consumed_at IS NULL AND credential_ref_id IS NULL) OR (consumed_at IS NOT NULL AND credential_ref_id IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX connectivity_open_edge_enrollment_uidx
  ON connectivity.edge_enrollments (edge_node_id)
  WHERE consumed_at IS NULL;

CREATE TABLE connectivity.edge_identity_bindings (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  credential_ref_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(credential_ref_id)),
  identity_revision bigint NOT NULL CHECK (identity_revision > 0),
  valid_from timestamptz NOT NULL,
  valid_until timestamptz,
  status text NOT NULL CHECK (status IN ('ACTIVE','ROTATED','REVOKED','EXPIRED')),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (edge_node_id, identity_revision),
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  FOREIGN KEY (tenant_id, credential_ref_id) REFERENCES connectivity.credential_refs(tenant_id, id),
  CHECK (valid_until IS NULL OR valid_until > valid_from)
);

CREATE UNIQUE INDEX connectivity_active_edge_identity_uidx
  ON connectivity.edge_identity_bindings (edge_node_id)
  WHERE status = 'ACTIVE' AND valid_until IS NULL;

CREATE TABLE connectivity.edge_handshakes (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  session_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(session_id)),
  runtime_version text NOT NULL CHECK (length(btrim(runtime_version)) BETWEEN 1 AND 64),
  protocol_schema_version integer NOT NULL CHECK (protocol_schema_version > 0),
  capabilities jsonb NOT NULL CHECK (jsonb_typeof(capabilities) = 'array'),
  max_payload_bytes integer NOT NULL CHECK (max_payload_bytes > 0),
  negotiated_max_payload_bytes integer CHECK (negotiated_max_payload_bytes > 0),
  status text NOT NULL CHECK (status IN ('ACCEPTED','READ_ONLY','UPGRADE_REQUIRED','REJECTED')),
  reason text,
  occurred_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  FOREIGN KEY (tenant_id, session_id) REFERENCES connectivity.sessions(tenant_id, id)
);

CREATE INDEX connectivity_edge_handshake_history_idx
  ON connectivity.edge_handshakes (edge_node_id, occurred_at DESC);

CREATE TABLE connectivity.edge_releases (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  release_digest_sha256 text NOT NULL CHECK (release_digest_sha256 ~ '^[a-f0-9]{64}$'),
  signer_key_id text NOT NULL CHECK (length(btrim(signer_key_id)) BETWEEN 1 AND 256),
  signature_ed25519_hex text NOT NULL CHECK (signature_ed25519_hex ~ '^[a-f0-9]{128}$'),
  runtime_revision text NOT NULL CHECK (length(btrim(runtime_revision)) BETWEEN 1 AND 128),
  manifest_revision text NOT NULL CHECK (length(btrim(manifest_revision)) BETWEEN 1 AND 128),
  registry_projection_revision bigint NOT NULL CHECK (registry_projection_revision > 0),
  driver_revision text NOT NULL CHECK (length(btrim(driver_revision)) BETWEEN 1 AND 128),
  rule_revision text,
  schedule_revision text,
  safety_policy_revision text NOT NULL CHECK (length(btrim(safety_policy_revision)) BETWEEN 1 AND 128),
  desired_config_revision bigint NOT NULL CHECK (desired_config_revision > 0),
  min_runtime_version text NOT NULL CHECK (length(btrim(min_runtime_version)) BETWEEN 1 AND 64),
  max_runtime_version text NOT NULL CHECK (length(btrim(max_runtime_version)) BETWEEN 1 AND 64),
  required_capabilities jsonb NOT NULL CHECK (jsonb_typeof(required_capabilities) = 'array'),
  published_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, release_digest_sha256)
);

CREATE TABLE connectivity.desired_edge_states (
  edge_node_id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(edge_node_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  release_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(release_id)),
  desired_revision bigint NOT NULL CHECK (desired_revision > 0),
  snapshot_revision bigint NOT NULL CHECK (snapshot_revision > 0),
  revision bigint NOT NULL CHECK (revision > 0),
  published_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  FOREIGN KEY (tenant_id, release_id) REFERENCES connectivity.edge_releases(tenant_id, id),
  CHECK (updated_at >= published_at)
);

CREATE TABLE connectivity.observed_edge_states (
  edge_node_id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(edge_node_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  active_release_id uuid,
  staged_release_id uuid,
  previous_release_id uuid,
  active_snapshot_revision bigint NOT NULL DEFAULT 0 CHECK (active_snapshot_revision >= 0),
  desired_revision bigint NOT NULL DEFAULT 0 CHECK (desired_revision >= 0),
  delivery_cursor bigint NOT NULL DEFAULT 0 CHECK (delivery_cursor >= 0),
  reported_config_revision bigint NOT NULL DEFAULT 0 CHECK (reported_config_revision >= 0),
  runtime_version text NOT NULL CHECK (length(btrim(runtime_version)) BETWEEN 1 AND 64),
  protocol_schema_version integer NOT NULL CHECK (protocol_schema_version > 0),
  manifest_digest_sha256 text CHECK (manifest_digest_sha256 IS NULL OR manifest_digest_sha256 ~ '^[a-f0-9]{64}$'),
  health text NOT NULL CHECK (health IN ('HEALTHY','DEGRADED','UNHEALTHY','UNKNOWN')),
  capacity_state text NOT NULL CHECK (capacity_state IN ('NORMAL','PRESSURE','CRITICAL','READ_ONLY_SAFETY')),
  drift_status text NOT NULL CHECK (drift_status IN ('CONVERGED','STAGING','DRIFTED','REJECTED','ROLLED_BACK','UNKNOWN')),
  drift_reason text,
  backlog_bytes bigint NOT NULL DEFAULT 0 CHECK (backlog_bytes >= 0),
  quarantine_count bigint NOT NULL DEFAULT 0 CHECK (quarantine_count >= 0),
  last_seen_at timestamptz NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  FOREIGN KEY (tenant_id, active_release_id) REFERENCES connectivity.edge_releases(tenant_id, id),
  FOREIGN KEY (tenant_id, staged_release_id) REFERENCES connectivity.edge_releases(tenant_id, id),
  FOREIGN KEY (tenant_id, previous_release_id) REFERENCES connectivity.edge_releases(tenant_id, id),
  CHECK (updated_at >= last_seen_at)
);

CREATE TABLE connectivity.edge_snapshots (
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  snapshot_revision bigint NOT NULL CHECK (snapshot_revision > 0),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  release_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(release_id)),
  desired_revision bigint NOT NULL CHECK (desired_revision > 0),
  base_delivery_cursor bigint NOT NULL CHECK (base_delivery_cursor >= 0),
  chunk_count integer NOT NULL CHECK (chunk_count >= 0),
  final_digest_sha256 text NOT NULL CHECK (final_digest_sha256 ~ '^[a-f0-9]{64}$'),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (edge_node_id, snapshot_revision),
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  FOREIGN KEY (tenant_id, release_id) REFERENCES connectivity.edge_releases(tenant_id, id)
);

CREATE TABLE connectivity.edge_snapshot_chunks (
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  snapshot_revision bigint NOT NULL CHECK (snapshot_revision > 0),
  chunk_index integer NOT NULL CHECK (chunk_index >= 0),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  chunk_digest_sha256 text NOT NULL CHECK (chunk_digest_sha256 ~ '^[a-f0-9]{64}$'),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'array'),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (edge_node_id, snapshot_revision, chunk_index),
  FOREIGN KEY (edge_node_id, snapshot_revision) REFERENCES connectivity.edge_snapshots(edge_node_id, snapshot_revision),
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id)
);

CREATE TABLE connectivity.edge_sync_sessions (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  connectivity_session_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(connectivity_session_id)),
  status text NOT NULL CHECK (status IN ('ACTIVE','CLOSED','READ_ONLY','UPGRADE_REQUIRED')),
  snapshot_revision bigint,
  snapshot_resume_chunk integer CHECK (snapshot_resume_chunk IS NULL OR snapshot_resume_chunk >= 0),
  delivery_cursor bigint NOT NULL DEFAULT 0 CHECK (delivery_cursor >= 0),
  opened_at timestamptz NOT NULL,
  closed_at timestamptz,
  close_reason text,
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  FOREIGN KEY (tenant_id, connectivity_session_id) REFERENCES connectivity.sessions(tenant_id, id),
  CHECK ((status = 'CLOSED') = (closed_at IS NOT NULL)),
  CHECK (updated_at >= opened_at)
);

CREATE UNIQUE INDEX connectivity_open_edge_sync_session_uidx
  ON connectivity.edge_sync_sessions (edge_node_id)
  WHERE closed_at IS NULL;

CREATE TABLE connectivity.edge_delivery_cursors (
  edge_node_id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(edge_node_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  committed_cursor bigint NOT NULL DEFAULT 0 CHECK (committed_cursor >= 0),
  retained_floor bigint NOT NULL DEFAULT 0 CHECK (retained_floor >= 0),
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  CHECK (retained_floor <= committed_cursor + 1)
);

CREATE TABLE connectivity.edge_delivery_items (
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  delivery_cursor bigint NOT NULL CHECK (delivery_cursor > 0),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  delivery_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(delivery_id)),
  owner_domain text NOT NULL CHECK (owner_domain IN ('REGISTRY','PROFILE','RULE','SCHEDULE','SAFETY_POLICY','DRIVER_CONFIG')),
  ordering_key text NOT NULL CHECK (length(btrim(ordering_key)) BETWEEN 1 AND 512),
  source_revision bigint NOT NULL CHECK (source_revision > 0),
  payload_digest_sha256 text NOT NULL CHECK (payload_digest_sha256 ~ '^[a-f0-9]{64}$'),
  payload jsonb,
  tombstone boolean NOT NULL DEFAULT false,
  priority text NOT NULL CHECK (priority IN ('CONFIG_CRITICAL','CONFIG_NORMAL')),
  state text NOT NULL CHECK (state IN ('PENDING','IN_FLIGHT','ACKED','QUARANTINED','DISPOSED')),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  applied_owner_revision bigint,
  failure_reason text,
  disposition_evidence_sha256 text CHECK (disposition_evidence_sha256 IS NULL OR disposition_evidence_sha256 ~ '^[a-f0-9]{64}$'),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (edge_node_id, delivery_cursor),
  UNIQUE (tenant_id, delivery_id),
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  CHECK ((tombstone AND payload IS NULL) OR (NOT tombstone AND jsonb_typeof(payload) IS NOT NULL)),
  CHECK (updated_at >= created_at)
);

CREATE INDEX connectivity_edge_delivery_work_idx
  ON connectivity.edge_delivery_items (edge_node_id, state, delivery_cursor)
  WHERE state IN ('PENDING','IN_FLIGHT','QUARANTINED');

CREATE TABLE connectivity.edge_ota_artifacts (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  version text NOT NULL CHECK (length(btrim(version)) BETWEEN 1 AND 64),
  package_ref text NOT NULL CHECK (left(package_ref, 11) = 'artifact://' AND length(package_ref) BETWEEN 16 AND 512 AND position('?' in package_ref) = 0 AND position('#' in package_ref) = 0 AND position('@' in package_ref) = 0),
  package_sha256 text NOT NULL CHECK (length(package_sha256) = 64 AND package_sha256 !~ '[^a-f0-9]'),
  artifact_digest_sha256 text NOT NULL CHECK (artifact_digest_sha256 ~ '^[a-f0-9]{64}$'),
  signer_key_id text NOT NULL CHECK (length(btrim(signer_key_id)) BETWEEN 1 AND 256),
  signature_ed25519_hex text NOT NULL CHECK (signature_ed25519_hex ~ '^[a-f0-9]{128}$'),
  min_runtime_version text NOT NULL CHECK (length(btrim(min_runtime_version)) BETWEEN 1 AND 64),
  max_runtime_version text NOT NULL CHECK (length(btrim(max_runtime_version)) BETWEEN 1 AND 64),
  required_capabilities jsonb NOT NULL CHECK (jsonb_typeof(required_capabilities) = 'array'),
  rollback_artifact_id uuid NOT NULL,
  published_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, artifact_digest_sha256),
  FOREIGN KEY (tenant_id, rollback_artifact_id) REFERENCES connectivity.edge_ota_artifacts(tenant_id, id)
);

CREATE TABLE connectivity.edge_ota_campaigns (
  id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  artifact_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(artifact_id)),
  status text NOT NULL CHECK (status IN ('DRAFT','RUNNING','PAUSED','COMPLETED','ABORTED')),
  waves jsonb NOT NULL CHECK (jsonb_typeof(waves) = 'array'),
  wave_index integer NOT NULL DEFAULT 0 CHECK (wave_index >= 0),
  campaign_window_start timestamptz NOT NULL,
  campaign_window_end timestamptz NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, artifact_id) REFERENCES connectivity.edge_ota_artifacts(tenant_id, id),
  CHECK (campaign_window_end > campaign_window_start),
  CHECK (updated_at >= created_at)
);

CREATE TABLE connectivity.edge_ota_assignments (
  campaign_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(campaign_id)),
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  status text NOT NULL CHECK (status IN ('PENDING','STAGING','PREFLIGHT_REJECTED','ACTIVATING','VERIFYING','SUCCEEDED','ROLLED_BACK','QUARANTINED')),
  target_wave integer NOT NULL CHECK (target_wave >= 0),
  staged_at timestamptz,
  completed_at timestamptz,
  evidence_digest_sha256 text CHECK (evidence_digest_sha256 IS NULL OR evidence_digest_sha256 ~ '^[a-f0-9]{64}$'),
  reason text,
  revision bigint NOT NULL CHECK (revision > 0),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (campaign_id, edge_node_id),
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES connectivity.edge_ota_campaigns(tenant_id, id),
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id)
);

CREATE TABLE connectivity.edge_fleet_events (
  event_id uuid PRIMARY KEY CHECK (connectivity.is_uuid_v7(event_id)),
  tenant_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(tenant_id)),
  edge_node_id uuid NOT NULL CHECK (connectivity.is_uuid_v7(edge_node_id)),
  event_type text NOT NULL CHECK (event_type IN (
    'EDGE_ENROLLED','EDGE_IDENTITY_ROTATED','HANDSHAKE_ACCEPTED','HANDSHAKE_REJECTED',
    'SNAPSHOT_STAGED','SNAPSHOT_ACTIVATED','DELIVERY_QUARANTINED','DELIVERY_DISPOSED',
    'RELEASE_STAGED','RELEASE_ACTIVATED','RELEASE_ROLLED_BACK','CAPACITY_CHANGED',
    'OTA_STAGED','OTA_PREFLIGHT_REJECTED','OTA_ACTIVATED','OTA_ROLLED_BACK'
  )),
  subject_id text NOT NULL CHECK (length(btrim(subject_id)) BETWEEN 1 AND 256),
  evidence_digest_sha256 text NOT NULL CHECK (evidence_digest_sha256 ~ '^[a-f0-9]{64}$'),
  evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'object'),
  occurred_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, edge_node_id) REFERENCES connectivity.edge_nodes(tenant_id, id),
  CHECK (NOT (evidence ?| ARRAY['password','secret','token','privateKey','credentialValue']))
);

CREATE OR REPLACE FUNCTION connectivity.reject_edge_fleet_immutable_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'edge fleet immutable record cannot be mutated';
END
$$;
REVOKE ALL ON FUNCTION connectivity.reject_edge_fleet_immutable_mutation() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION connectivity.reject_edge_fleet_immutable_mutation() TO connectivity_runtime;

CREATE TRIGGER edge_releases_immutable
  BEFORE UPDATE ON connectivity.edge_releases
  FOR EACH ROW EXECUTE FUNCTION connectivity.reject_edge_fleet_immutable_mutation();
CREATE TRIGGER edge_snapshots_immutable
  BEFORE UPDATE ON connectivity.edge_snapshots
  FOR EACH ROW EXECUTE FUNCTION connectivity.reject_edge_fleet_immutable_mutation();
CREATE TRIGGER edge_snapshot_chunks_immutable
  BEFORE UPDATE ON connectivity.edge_snapshot_chunks
  FOR EACH ROW EXECUTE FUNCTION connectivity.reject_edge_fleet_immutable_mutation();
CREATE TRIGGER edge_ota_artifacts_immutable
  BEFORE UPDATE ON connectivity.edge_ota_artifacts
  FOR EACH ROW EXECUTE FUNCTION connectivity.reject_edge_fleet_immutable_mutation();
CREATE TRIGGER edge_fleet_events_immutable
  BEFORE UPDATE ON connectivity.edge_fleet_events
  FOR EACH ROW EXECUTE FUNCTION connectivity.reject_edge_fleet_immutable_mutation();

ALTER TABLE connectivity.edge_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_nodes FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_enrollments FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_identity_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_identity_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_handshakes ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_handshakes FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_releases ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_releases FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.desired_edge_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.desired_edge_states FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.observed_edge_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.observed_edge_states FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_snapshot_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_snapshot_chunks FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_sync_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_sync_sessions FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_delivery_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_delivery_cursors FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_delivery_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_delivery_items FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_ota_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_ota_artifacts FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_ota_campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_ota_campaigns FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_ota_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_ota_assignments FORCE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_fleet_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectivity.edge_fleet_events FORCE ROW LEVEL SECURITY;

CREATE POLICY edge_nodes_tenant_policy ON connectivity.edge_nodes USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_enrollments_tenant_policy ON connectivity.edge_enrollments USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_identity_bindings_tenant_policy ON connectivity.edge_identity_bindings USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_handshakes_tenant_policy ON connectivity.edge_handshakes USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_releases_tenant_policy ON connectivity.edge_releases USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY desired_edge_states_tenant_policy ON connectivity.desired_edge_states USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY observed_edge_states_tenant_policy ON connectivity.observed_edge_states USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_snapshots_tenant_policy ON connectivity.edge_snapshots USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_snapshot_chunks_tenant_policy ON connectivity.edge_snapshot_chunks USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_sync_sessions_tenant_policy ON connectivity.edge_sync_sessions USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_delivery_cursors_tenant_policy ON connectivity.edge_delivery_cursors USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_delivery_items_tenant_policy ON connectivity.edge_delivery_items USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_ota_artifacts_tenant_policy ON connectivity.edge_ota_artifacts USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_ota_campaigns_tenant_policy ON connectivity.edge_ota_campaigns USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_ota_assignments_tenant_policy ON connectivity.edge_ota_assignments USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY edge_fleet_events_tenant_policy ON connectivity.edge_fleet_events USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON connectivity.edge_nodes, connectivity.edge_enrollments, connectivity.edge_identity_bindings,
  connectivity.edge_handshakes, connectivity.desired_edge_states, connectivity.observed_edge_states,
  connectivity.edge_sync_sessions, connectivity.edge_delivery_cursors, connectivity.edge_delivery_items,
  connectivity.edge_ota_campaigns, connectivity.edge_ota_assignments TO connectivity_runtime;
GRANT SELECT, INSERT ON connectivity.edge_releases, connectivity.edge_snapshots, connectivity.edge_snapshot_chunks,
  connectivity.edge_ota_artifacts, connectivity.edge_fleet_events TO connectivity_runtime;
REVOKE DELETE ON connectivity.edge_nodes, connectivity.edge_enrollments, connectivity.edge_identity_bindings,
  connectivity.edge_handshakes, connectivity.edge_releases, connectivity.desired_edge_states, connectivity.observed_edge_states,
  connectivity.edge_snapshots, connectivity.edge_snapshot_chunks, connectivity.edge_sync_sessions, connectivity.edge_delivery_cursors,
  connectivity.edge_delivery_items, connectivity.edge_ota_artifacts, connectivity.edge_ota_campaigns, connectivity.edge_ota_assignments,
  connectivity.edge_fleet_events FROM connectivity_runtime;

COMMIT;

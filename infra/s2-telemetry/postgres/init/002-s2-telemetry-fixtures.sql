BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;

INSERT INTO telemetry_runtime.registry_device_bindings (
  device_id, owning_organization_id, site_id, integration_instance_id,
  external_entity_type, external_id, binding_status, binding_revision,
  source_registry_revision, valid_from, valid_to, updated_at
) VALUES
  ('018f2e00-3000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000001', '018f2e00-1000-7000-8000-000000000001', '018f2e00-6000-7000-8000-000000000001', 'DEVICE', 'tb-device-org-a-site-1', 'ACTIVE', 1, 11, '2026-07-23T00:00:00Z', NULL, '2026-07-23T00:00:00Z'),
  ('018f2e00-3000-7000-8000-000000000002', '018f2e00-0000-7000-8000-000000000001', '018f2e00-1000-7000-8000-000000000002', '018f2e00-6000-7000-8000-000000000001', 'DEVICE', 'tb-device-org-a-site-2', 'ACTIVE', 1, 11, '2026-07-23T00:00:00Z', NULL, '2026-07-23T00:00:00Z'),
  ('018f2e00-3000-7000-8000-000000000003', '018f2e00-0000-7000-8000-000000000002', '018f2e00-1000-7000-8000-000000000003', '018f2e00-6000-7000-8000-000000000002', 'DEVICE', 'tb-device-org-b-site-1', 'ACTIVE', 1, 7, '2026-07-23T00:00:00Z', NULL, '2026-07-23T00:00:00Z'),
  ('018f2e00-3000-7000-8000-000000000004', '018f2e00-0000-7000-8000-000000000001', '018f2e00-1000-7000-8000-000000000001', '018f2e00-6000-7000-8000-000000000001', 'ASSET', 'tb-conflicted-asset', 'QUARANTINED', 1, 11, '2026-07-23T00:00:00Z', NULL, '2026-07-23T00:00:00Z');

INSERT INTO telemetry_runtime.iam_scope_projections (
  projection_id, principal_id, acting_organization_id, owning_organization_id,
  site_id, device_id, telemetry_key, action, decision, policy_revision,
  source_event_id, valid_until, revoked_at, updated_at
) VALUES
  ('018f2e00-7000-7000-8000-000000000001', '018f2e00-2000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000001', '018f2e00-1000-7000-8000-000000000001', '018f2e00-3000-7000-8000-000000000001', 'zone.temperature', 'SNAPSHOT_READ', 'ALLOW', 3, '018f2e00-7100-7000-8000-000000000001', '2026-07-24T00:00:00Z', NULL, '2026-07-23T00:00:00Z'),
  ('018f2e00-7000-7000-8000-000000000002', '018f2e00-2000-7000-8000-000000000002', '018f2e00-0000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000002', '018f2e00-1000-7000-8000-000000000003', '018f2e00-3000-7000-8000-000000000003', 'zone.temperature', 'SNAPSHOT_READ', 'ALLOW', 4, '018f2e00-7100-7000-8000-000000000002', '2026-07-24T00:00:00Z', NULL, '2026-07-23T00:00:00Z'),
  ('018f2e00-7000-7000-8000-000000000003', '018f2e00-2000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000001', '018f2e00-1000-7000-8000-000000000002', '018f2e00-3000-7000-8000-000000000002', 'zone.temperature', 'SNAPSHOT_READ', 'DENY', 3, '018f2e00-7100-7000-8000-000000000003', '2026-07-24T00:00:00Z', NULL, '2026-07-23T00:00:00Z'),
  ('018f2e00-7000-7000-8000-000000000004', '018f2e00-2000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000001', '018f2e00-1000-7000-8000-000000000001', '018f2e00-3000-7000-8000-000000000001', 'zone.temperature', 'SUBSCRIBE', 'ALLOW', 3, '018f2e00-7100-7000-8000-000000000004', '2026-07-24T00:00:00Z', NULL, '2026-07-23T00:00:00Z');

INSERT INTO telemetry_runtime.presence_policies (
  device_id, policy_revision, online_within_seconds, offline_after_seconds,
  coverage_required, updated_at
) VALUES
  ('018f2e00-3000-7000-8000-000000000001', 2, 60, 180, true, '2026-07-23T00:00:00Z'),
  ('018f2e00-3000-7000-8000-000000000002', 2, 60, 180, true, '2026-07-23T00:00:00Z'),
  ('018f2e00-3000-7000-8000-000000000003', 2, 60, 180, true, '2026-07-23T00:00:00Z');

INSERT INTO telemetry_runtime.freshness_policies (
  device_id, telemetry_key, policy_revision, fresh_within_seconds, configured, updated_at
) VALUES
  ('018f2e00-3000-7000-8000-000000000001', 'zone.temperature', 5, 300, true, '2026-07-23T00:00:00Z'),
  ('018f2e00-3000-7000-8000-000000000001', 'zone.humidity', 5, 300, true, '2026-07-23T00:00:00Z'),
  ('018f2e00-3000-7000-8000-000000000001', 'duct.pressure', 5, 120, true, '2026-07-23T00:00:00Z'),
  ('018f2e00-3000-7000-8000-000000000003', 'zone.temperature', 2, 300, true, '2026-07-23T00:00:00Z');

INSERT INTO telemetry_runtime.source_positions (
  integration_instance_id, source_partition, source_offset, source_event_id,
  observed_at, updated_at
) VALUES
  ('018f2e00-6000-7000-8000-000000000001', 'tb-telemetry-0', 42, '018f2e00-8000-7000-8000-000000000001', '2026-07-23T00:00:02Z', '2026-07-23T00:00:02Z');

INSERT INTO telemetry_runtime.source_observations (
  observation_id, integration_instance_id, source_event_id, source_partition,
  source_offset, device_id, telemetry_key, value, value_type, unit, sampled_at,
  received_at, acceptance_status, quality_reasons, payload_sha256, created_at
) VALUES
  ('018f2e00-8100-7000-8000-000000000001', '018f2e00-6000-7000-8000-000000000001', '018f2e00-8000-7000-8000-000000000001', 'tb-telemetry-0', 42, '018f2e00-3000-7000-8000-000000000001', 'zone.temperature', '23.5'::jsonb, 'NUMBER', 'Cel', '2026-07-23T00:00:00Z', '2026-07-23T00:00:02Z', 'ACCEPTED', '{}', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '2026-07-23T00:00:02Z'),
  ('018f2e00-8100-7000-8000-000000000002', '018f2e00-6000-7000-8000-000000000001', '018f2e00-8000-7000-8000-000000000002', 'tb-telemetry-0', 43, '018f2e00-3000-7000-8000-000000000001', 'duct.pressure', '"invalid"'::jsonb, 'STRING', 'Pa', '2026-07-23T00:00:01Z', '2026-07-23T00:00:03Z', 'REJECTED', ARRAY['TYPE_MISMATCH'], 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', '2026-07-23T00:00:03Z');

INSERT INTO telemetry_runtime.ingest_quarantine (
  quarantine_id, integration_instance_id, external_entity_type, external_id,
  device_id, telemetry_key, reason_code, evidence, detected_at, resolved_at, resolution
) VALUES
  ('018f2e00-8200-7000-8000-000000000001', '018f2e00-6000-7000-8000-000000000001', 'ASSET', 'tb-conflicted-asset', NULL, 'zone.temperature', 'MAPPING_CONFLICT', '{"candidateDeviceIds":["018f2e00-3000-7000-8000-000000000001","018f2e00-3000-7000-8000-000000000002"],"source":"fixture"}'::jsonb, '2026-07-23T00:00:04Z', NULL, NULL);

INSERT INTO telemetry_runtime.latest_accepted_telemetry (
  device_id, telemetry_key, business_revision, value, value_type, unit, sampled_at,
  received_at, freshness, quality, quality_reasons, policy_revision, updated_at
) VALUES
  ('018f2e00-3000-7000-8000-000000000001', 'zone.temperature', 1, '23.5'::jsonb, 'NUMBER', 'Cel', '2026-07-23T00:00:00Z', '2026-07-23T00:00:02Z', 'FRESH', 'GOOD', '{}', 5, '2026-07-23T00:00:05Z');

INSERT INTO telemetry_runtime.device_presence (
  device_id, business_revision, applicability, current_state, last_seen_at,
  evaluated_at, policy_revision, last_known, updated_at
) VALUES
  ('018f2e00-3000-7000-8000-000000000001', 1, 'APPLICABLE', 'ONLINE', '2026-07-23T00:00:00Z', '2026-07-23T00:00:05Z', 2, '{"state":"ONLINE","lastSeenAt":"2026-07-23T00:00:00.000Z","evaluatedAt":"2026-07-23T00:00:05.000Z","policyRevision":2}'::jsonb, '2026-07-23T00:00:05Z'),
  ('018f2e00-3000-7000-8000-000000000002', 1, 'APPLICABLE', 'UNKNOWN', NULL, '2026-07-23T00:00:05Z', 2, NULL, '2026-07-23T00:00:05Z'),
  ('018f2e00-3000-7000-8000-000000000003', 1, 'APPLICABLE', 'UNKNOWN', NULL, '2026-07-23T00:00:05Z', 2, NULL, '2026-07-23T00:00:05Z');

INSERT INTO telemetry_runtime.device_observation_snapshots (
  device_id, business_revision, evaluated_at, evaluation_availability,
  availability_reasons, telemetry_readiness, display_state, snapshot,
  snapshot_sha256, updated_at
) VALUES
  (
    '018f2e00-3000-7000-8000-000000000001', 1, '2026-07-23T00:00:05Z', 'AVAILABLE', '{}', 'INCOMPLETE', 'ONLINE',
    '{
      "schemaVersion":1,
      "deviceId":"018f2e00-3000-7000-8000-000000000001",
      "owningOrganizationId":"018f2e00-0000-7000-8000-000000000001",
      "siteId":"018f2e00-1000-7000-8000-000000000001",
      "businessRevision":1,
      "evaluatedAt":"2026-07-23T00:00:05.000Z",
      "evaluationAvailability":"AVAILABLE",
      "availabilityReasons":[],
      "presence":{"applicability":"APPLICABLE","currentState":"ONLINE","lastSeenAt":"2026-07-23T00:00:00.000Z","policyRevision":2,"lastKnown":{"state":"ONLINE","lastSeenAt":"2026-07-23T00:00:00.000Z","evaluatedAt":"2026-07-23T00:00:05.000Z","policyRevision":2}},
      "telemetryReadiness":"INCOMPLETE",
      "displayState":"ONLINE",
      "values":[
        {"key":"zone.temperature","state":"PRESENT","value":23.5,"valueType":"NUMBER","unit":"Cel","sampledAt":"2026-07-23T00:00:00.000Z","receivedAt":"2026-07-23T00:00:02.000Z","freshness":"FRESH","quality":"GOOD","qualityReasons":[],"policyRevision":5},
        {"key":"zone.humidity","state":"MISSING","freshness":"MISSING","missingReason":"NEVER_OBSERVED","policyRevision":5},
        {"key":"duct.pressure","state":"MISSING","freshness":"MISSING","missingReason":"ONLY_REJECTED_CANDIDATES","policyRevision":5}
      ]
    }'::jsonb,
    'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',
    '2026-07-23T00:00:05Z'
  );

INSERT INTO telemetry_runtime.telemetry_subscriptions (
  subscription_id, client_subscription_id, principal_id, acting_organization_id,
  device_id, keys, scope_sha256, policy_revision, status, expires_at,
  revoked_at, created_at, updated_at
) VALUES
  ('subscription_owner_a1_0001', 'device-detail-a1', '018f2e00-2000-7000-8000-000000000001', '018f2e00-0000-7000-8000-000000000001', '018f2e00-3000-7000-8000-000000000001', '["zone.temperature","zone.humidity","duct.pressure"]'::jsonb, 'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd', 3, 'ACTIVE', '2026-07-23T00:05:00Z', NULL, '2026-07-23T00:00:05Z', '2026-07-23T00:00:05Z');

INSERT INTO telemetry_runtime.recovery_cursors (
  cursor_id, subscription_id, business_revision, transport_epoch, transport_offset,
  scope_sha256, cursor_sha256, expires_at, revoked_at, created_at
) VALUES
  ('018f2e00-8300-7000-8000-000000000001', 'subscription_owner_a1_0001', 1, 'epoch-fixture-a', 9, 'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd', 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee', '2026-07-23T00:02:05Z', NULL, '2026-07-23T00:00:05Z');

INSERT INTO telemetry_runtime.telemetry_publication_outbox (
  event_id, device_id, business_revision, subscription_id, event_family,
  payload, payload_sha256, delivery_state, available_at, attempts,
  last_error_code, published_at, created_at
) VALUES
  (
    '018f2e00-8400-7000-8000-000000000001',
    '018f2e00-3000-7000-8000-000000000001',
    1,
    'subscription_owner_a1_0001',
    'hvac.telemetry.device-snapshot.v1',
    '{"schemaVersion":1,"kind":"DEVICE_OBSERVATION_DELTA","eventId":"018f2e00-8400-7000-8000-000000000001","subscriptionId":"subscription_owner_a1_0001","deviceId":"018f2e00-3000-7000-8000-000000000001","previousRevision":0,"revision":1,"evaluatedAt":"2026-07-23T00:00:05.000Z","publishedAt":"2026-07-23T00:00:06.000Z","evaluationAvailability":"AVAILABLE","availabilityReasons":[],"presence":{"applicability":"APPLICABLE","currentState":"ONLINE","lastSeenAt":"2026-07-23T00:00:00.000Z","policyRevision":2,"lastKnown":null},"telemetryReadiness":"INCOMPLETE","displayState":"ONLINE","telemetryChanges":[]}'::jsonb,
    'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff',
    'PENDING',
    '2026-07-23T00:00:06Z',
    0,
    NULL,
    NULL,
    '2026-07-23T00:00:06Z'
  );

RESET ROLE;
COMMIT;

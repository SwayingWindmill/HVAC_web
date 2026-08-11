BEGIN;

INSERT INTO iam.tenants (id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at) VALUES
  ('018f1d00-0000-7000-8000-000000000001', 'tenant-a', 'Tenant A', 'Asia/Shanghai', 'CNY', 'CN', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1d00-0000-7000-8000-000000000002', 'tenant-b', 'Tenant B', 'Europe/London', 'GBP', 'GB', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO iam.principals (id, external_issuer, external_subject, display_name, email, status, revision, created_at, updated_at) VALUES
  ('018f1e00-2000-7000-8000-000000000001', 'https://identity.example.test/oidc', 'owner-a', 'Owner A', 'owner-a@example.test', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2000-7000-8000-000000000002', 'https://identity.example.test/oidc', 'delegated', 'Delegated User', 'delegated@example.test', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2000-7000-8000-000000000003', 'https://identity.example.test/oidc', 'denied', 'Denied User', 'denied@example.test', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2000-7000-8000-000000000004', 'https://identity.example.test/oidc', 'no-access', 'No Access User', 'no-access@example.test', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO iam.organization_memberships (id, tenant_id, organization_id, principal_id, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ('018f1e00-2100-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000001', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2100-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000003', '018f1e00-2000-7000-8000-000000000002', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2100-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000003', '018f1e00-2000-7000-8000-000000000003', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO iam.role_bindings (id, tenant_id, organization_id, principal_id, role_key, actions, effect, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ('018f1e00-2200-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000001', 'registry-reader', ARRAY['registry.read'], 'ALLOW', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2200-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000003', '018f1e00-2000-7000-8000-000000000002', 'registry-reader', ARRAY['registry.read'], 'ALLOW', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2200-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000003', '018f1e00-2000-7000-8000-000000000003', 'registry-reader', ARRAY['registry.read'], 'ALLOW', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO iam.site_bindings (id, tenant_id, acting_organization_id, owning_organization_id, site_id, principal_id, actions, effect, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ('018f1e00-2300-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000001', ARRAY['analytics.energy-series.read'], 'ALLOW', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2300-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000003', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000002', ARRAY['registry.read'], 'ALLOW', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2300-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000003', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000003', ARRAY['registry.read'], 'ALLOW', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO iam.explicit_denies (id, tenant_id, acting_organization_id, owning_organization_id, site_id, principal_id, action, reason_code, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ('018f1e00-2400-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000003', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-2000-7000-8000-000000000003', 'registry.read', 'EXPLICIT_TEST_DENY', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO iam.policies (id, tenant_id, organization_id, policy_key, policy_revision, status, document, created_at, updated_at) VALUES
  ('018f1e00-2500-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', 'registry-read', 1, 'ACTIVE', '{"denyPrecedence":true,"action":"registry.read"}', '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-2500-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000003', 'registry-read', 1, 'ACTIVE', '{"denyPrecedence":true,"action":"registry.read"}', '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.organizations (id, tenant_id, code, display_name, status, revision, created_at, updated_at) VALUES
  ('018f1e00-0000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', 'owner-a', 'Owner Organization A', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-0000-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000002', 'owner-b', 'Owner Organization B', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-0000-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000001', 'acting', 'Acting Organization', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.sites (id, tenant_id, organization_id, code, display_name, timezone, status, revision, created_at, updated_at) VALUES
  ('018f1e00-1000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', 'owner-a-site-1', 'Owner A Site 1', 'Asia/Shanghai', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-1000-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', 'owner-a-site-2', 'Owner A Site 2', 'Asia/Singapore', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-1000-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000002', '018f1e00-0000-7000-8000-000000000002', 'owner-b-site-1', 'Owner B Site 1', 'Europe/London', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.equipment (id, tenant_id, organization_id, site_id, code, display_name, equipment_type, status, revision, created_at, updated_at) VALUES
  ('018f1e00-3000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'ahu-1', 'Air Handling Unit 1', 'AHU', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-3000-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000002', 'chiller-1', 'Chiller 1', 'CHILLER', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-3000-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000002', '018f1e00-0000-7000-8000-000000000002', '018f1e00-1000-7000-8000-000000000003', 'ahu-b1', 'Owner B AHU', 'AHU', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.devices (id, tenant_id, organization_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at) VALUES
  ('018f1e00-4000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'controller-1', 'Controller 1', 'CONTROLLER', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-4000-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000002', 'gateway-1', 'Gateway 1', 'GATEWAY', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-4000-7000-8000-000000000003', '018f1d00-0000-7000-8000-000000000002', '018f1e00-0000-7000-8000-000000000002', '018f1e00-1000-7000-8000-000000000003', 'controller-b1', 'Owner B Controller', 'CONTROLLER', 'ACTIVE', 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.device_bindings (id, tenant_id, organization_id, site_id, device_id, equipment_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ('018f1e00-5000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-4000-7000-8000-000000000001', '018f1e00-3000-7000-8000-000000000001', 'CONTROLLER', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.external_bindings (id, tenant_id, organization_id, site_id, integration_instance_id, provider, external_entity_type, external_id, binding_status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ('018f1e00-6000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', '018f1e00-6100-7000-8000-000000000001', 'thingsboard', 'DEVICE', 'tb-device-owner-a-1', 'ACTIVE', '2026-07-21T00:00:00Z', NULL, 1, '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.legacy_resource_maps (id, tenant_id, organization_id, site_id, source_system, source_table, source_key, target_resource_type, target_resource_id, mapping_state, transformation_version, batch_id, source_watermark, source_row_hash, relation_evidence, created_at, updated_at) VALUES
  ('018f1e00-7000-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'legacy-hvac-backend', 'device', 'legacy-device-1', 'DEVICE', '018f1e00-4000-7000-8000-000000000001', 'VERIFIED', 's1-v1', 'fixture-batch-1', 'fixture-watermark-1', '1111111111111111111111111111111111111111111111111111111111111111', '{"sourceType":"device","verifiedRelation":true}', '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z'),
  ('018f1e00-7000-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-1000-7000-8000-000000000001', 'legacy-hvac-backend', 'asset', 'ambiguous-asset-1', 'EQUIPMENT', NULL, 'QUARANTINED', 's1-v1', 'fixture-batch-1', 'fixture-watermark-1', '2222222222222222222222222222222222222222222222222222222222222222', '{"sourceType":"asset","verifiedRelation":false}', '2026-07-21T00:00:00Z', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.migration_provenance (id, tenant_id, organization_id, legacy_resource_map_id, source_system, source_table, source_key, target_resource_type, target_resource_id, transformation_version, batch_id, source_watermark, source_row_hash, result, created_at) VALUES
  ('018f1e00-7100-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-7000-7000-8000-000000000001', 'legacy-hvac-backend', 'device', 'legacy-device-1', 'DEVICE', '018f1e00-4000-7000-8000-000000000001', 's1-v1', 'fixture-batch-1', 'fixture-watermark-1', '1111111111111111111111111111111111111111111111111111111111111111', 'VERIFIED', '2026-07-21T00:00:00Z'),
  ('018f1e00-7100-7000-8000-000000000002', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', '018f1e00-7000-7000-8000-000000000002', 'legacy-hvac-backend', 'asset', 'ambiguous-asset-1', 'EQUIPMENT', NULL, 's1-v1', 'fixture-batch-1', 'fixture-watermark-1', '2222222222222222222222222222222222222222222222222222222222222222', 'QUARANTINED', '2026-07-21T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO core_registry.migration_quarantine (id, tenant_id, organization_id, source_system, source_table, source_key, reason_code, source_row_hash, payload_metadata, detected_at, resolved_at, resolution) VALUES
  ('018f1e00-7200-7000-8000-000000000001', '018f1d00-0000-7000-8000-000000000001', '018f1e00-0000-7000-8000-000000000001', 'legacy-hvac-backend', 'asset', 'ambiguous-asset-1', 'AMBIGUOUS_ASSET_EQUIPMENT_RELATION', '2222222222222222222222222222222222222222222222222222222222222222', '{"sourceType":"asset","candidateTypes":["EQUIPMENT","GROUP"]}', '2026-07-21T00:00:00Z', NULL, NULL)
ON CONFLICT DO NOTHING;

COMMIT;

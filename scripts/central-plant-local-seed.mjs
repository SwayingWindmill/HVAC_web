import {
  analyticsActions,
  centralPlantDevices,
  centralPlantIdentity,
  localUUID,
  sqlLiteral,
  telemetryActions,
} from './central-plant-local-contract.mjs';

export function buildS1SeedSQL({ oidcIssuer, pointKeysByDevice }) {
  const { organizationId, siteId, principalId } = centralPlantIdentity;
  const actions = `ARRAY[${telemetryActions.map(sqlLiteral).join(',')}]`;
  const analytics = `ARRAY[${analyticsActions.map(sqlLiteral).join(',')}]`;
  let sequence = 1;
  const nextID = () => localUUID(sequence++);
  const deviceRows = centralPlantDevices.map((device) => `(${sqlLiteral(device.platformDeviceId)},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(device.slug)},${sqlLiteral(device.name)},${sqlLiteral(device.type)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const scopeRows = centralPlantDevices.map((device) => `(${sqlLiteral(nextID())},${sqlLiteral(principalId)},${sqlLiteral(organizationId)},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(device.platformDeviceId)},${actions},'ALLOW','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const keyRows = centralPlantDevices.flatMap((device) => (pointKeysByDevice.get(device.platformDeviceId) ?? []).map((key) => `(${sqlLiteral(nextID())},${sqlLiteral(principalId)},${sqlLiteral(organizationId)},${sqlLiteral(device.platformDeviceId)},${sqlLiteral(key)},${actions},'ALLOW','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`)).join(',\n  ');

  return `BEGIN;
INSERT INTO core_registry.organizations (id, code, display_name, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(organizationId)}, 'central-plant-local', '中央机房本地验证组织', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.sites (id, organization_id, code, display_name, timezone, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(siteId)}, ${sqlLiteral(organizationId)}, 'central-plant', '中央机房', 'Asia/Shanghai', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.devices (id, organization_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at) VALUES
  ${deviceRows}
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, device_type=EXCLUDED.device_type, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO iam.principals (id, external_issuer, external_subject, display_name, email, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(principalId)}, ${sqlLiteral(oidcIssuer)}, 'fixture-user', 'Central Plant Operator', 'operator@example.test', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET external_issuer=EXCLUDED.external_issuer, external_subject=EXCLUDED.external_subject, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO iam.organization_memberships (id, organization_id, principal_id, status, valid_from, valid_to, revision, created_at, updated_at)
VALUES (${sqlLiteral(nextID())}, ${sqlLiteral(organizationId)}, ${sqlLiteral(principalId)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()) ON CONFLICT DO NOTHING;
INSERT INTO iam.role_bindings (id, organization_id, principal_id, role_key, actions, effect, valid_from, valid_to, revision, created_at, updated_at) VALUES
  (${sqlLiteral(nextID())}, ${sqlLiteral(organizationId)}, ${sqlLiteral(principalId)}, 'registry-reader', ARRAY['registry.read'], 'ALLOW', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()),
  (${sqlLiteral(nextID())}, ${sqlLiteral(organizationId)}, ${sqlLiteral(principalId)}, 'telemetry-reader', ${actions}, 'ALLOW', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()) ON CONFLICT DO NOTHING;
INSERT INTO iam.site_bindings (id, acting_organization_id, owning_organization_id, site_id, principal_id, actions, effect, valid_from, valid_to, revision, created_at, updated_at)
VALUES (${sqlLiteral(nextID())}, ${sqlLiteral(organizationId)}, ${sqlLiteral(organizationId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(principalId)}, ${analytics}, 'ALLOW', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()) ON CONFLICT DO NOTHING;
INSERT INTO iam.policies (id, organization_id, policy_key, policy_revision, status, document, created_at, updated_at) VALUES
  (${sqlLiteral(nextID())}, ${sqlLiteral(organizationId)}, 'registry-read', 1, 'ACTIVE', '{"denyPrecedence":true,"action":"registry.read"}', clock_timestamp(), clock_timestamp()),
  (${sqlLiteral(nextID())}, ${sqlLiteral(organizationId)}, 'telemetry-access', 1, 'ACTIVE', '{"denyPrecedence":true,"exactDeviceKeyScope":true}', clock_timestamp(), clock_timestamp()) ON CONFLICT DO NOTHING;
INSERT INTO iam.telemetry_scope_bindings (id, principal_id, acting_organization_id, owning_organization_id, site_id, device_id, actions, effect, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${scopeRows} ON CONFLICT DO NOTHING;
INSERT INTO iam.telemetry_key_bindings (id, principal_id, acting_organization_id, device_id, telemetry_key, actions, effect, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${keyRows} ON CONFLICT DO NOTHING;
COMMIT;
`;
}

export function buildS2SeedSQL({ pointsByDevice }) {
  const { organizationId, siteId, integrationInstanceId } = centralPlantIdentity;
  const bindingRows = centralPlantDevices.map((device) => `(${sqlLiteral(device.platformDeviceId)},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(integrationInstanceId)},'DEVICE',${sqlLiteral(device.platformDeviceId)},'ACTIVE',1,1,clock_timestamp(),NULL,clock_timestamp())`).join(',\n  ');
  const presenceRows = centralPlantDevices.map((device) => `(${sqlLiteral(device.platformDeviceId)},1,30,120,true,ARRAY['SOURCE_ACTIVITY']::text[],15,120,clock_timestamp())`).join(',\n  ');
  const freshnessRows = centralPlantDevices.flatMap((device) => (pointsByDevice.get(device.platformDeviceId) ?? []).map((point) => `(${sqlLiteral(device.platformDeviceId)},${sqlLiteral(point.telemetryKey)},1,30,true,5,${sqlLiteral(point.valueType)},${point.unit ? sqlLiteral(point.unit) : 'NULL'},NULL,NULL,clock_timestamp())`)).join(',\n  ');
  const coverageRows = centralPlantDevices.map((device) => `(${sqlLiteral(device.platformDeviceId)},true,clock_timestamp(),NULL,1,clock_timestamp())`).join(',\n  ');
  return `BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;
DELETE FROM telemetry_runtime.telemetry_publication_outbox;
INSERT INTO telemetry_runtime.registry_device_bindings (device_id, owning_organization_id, site_id, integration_instance_id, external_entity_type, external_id, binding_status, binding_revision, source_registry_revision, valid_from, valid_to, updated_at) VALUES
  ${bindingRows} ON CONFLICT (device_id) DO UPDATE SET external_id=EXCLUDED.external_id, binding_status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO telemetry_runtime.presence_policies (device_id, policy_revision, online_within_seconds, offline_after_seconds, coverage_required, accepted_signal_types, max_future_clock_skew_seconds, max_source_lag_seconds, updated_at) VALUES
  ${presenceRows} ON CONFLICT (device_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision, updated_at=clock_timestamp();
INSERT INTO telemetry_runtime.freshness_policies (device_id, telemetry_key, policy_revision, fresh_within_seconds, configured, expected_sample_interval_seconds, value_type, expected_unit, minimum_number, maximum_number, updated_at) VALUES
  ${freshnessRows} ON CONFLICT (device_id, telemetry_key) DO UPDATE SET value_type=EXCLUDED.value_type, expected_unit=EXCLUDED.expected_unit, configured=true, updated_at=clock_timestamp();
INSERT INTO telemetry_runtime.observation_coverage (device_id, available, continuous_since, reason_code, source_revision, updated_at) VALUES
  ${coverageRows} ON CONFLICT (device_id) DO UPDATE SET available=true, reason_code=NULL, updated_at=clock_timestamp();
RESET ROLE;
COMMIT;
`;
}

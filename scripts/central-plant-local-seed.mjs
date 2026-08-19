import {
  analyticsActions,
  centralPlantDevices,
  centralPlantIdentity,
  localUUID,
  sqlLiteral,
  telemetryActions,
} from './central-plant-local-contract.mjs';
import {
  centralPlantAreas as centralPlantSpaces,
  centralPlantDeviceEndpoints,
  centralPlantEquipment as centralPlantAssets,
  centralPlantSensors,
} from './central-plant-spatial-model.mjs';

const sqlJSON = (value) => sqlLiteral(JSON.stringify(value));

function durationMilliseconds(value) {
  const match = /^(\d+)(ms|s|m|h)$/.exec(value);
  if (!match) throw new Error(`unsupported bounded duration ${value}`);
  const magnitude = Number(match[1]);
  const multiplier = { ms: 1, s: 1_000, m: 60_000, h: 3_600_000 }[match[2]];
  return magnitude * multiplier;
}

function bindingRoleForDeviceType(deviceType) {
  if (deviceType === 'HVAC_POWER_METER' || deviceType === 'BTU_METER') return 'METER';
  if (deviceType === 'WEATHER_STATION') return 'SENSOR';
  return 'CONTROLLER';
}

const canonicalSubjectType = (value) => ({ AREA: 'SPACE', EQUIPMENT: 'ASSET' })[value] ?? value;

export function buildCentralPlantSpatialIdentities(spatialPoints = []) {
  let sequence = 1;
  const nextID = () => localUUID(sequence++);
  const spaceIdByKey = new Map(centralPlantSpaces.map((space) => [space.id, nextID()]));
  const assetIdByKey = new Map(centralPlantAssets.map((asset) => [asset.id, nextID()]));
  const sensorIdByKey = new Map(centralPlantSensors.map((sensor) => [sensor.id, nextID()]));
  const pointIdByRef = new Map(spatialPoints.map((point) => [`${point.deviceId}/${point.telemetryKey}`, nextID()]));
  return { spaceIdByKey, assetIdByKey, sensorIdByKey, pointIdByRef, nextID };
}

export function buildS1SeedSQL({ oidcIssuer, principalSubject = 'fixture-user', pointKeysByDevice, spatialPoints = [] }) {
  const { tenantId, siteId, principalId } = centralPlantIdentity;
  const actions = `ARRAY[${telemetryActions.map(sqlLiteral).join(',')}]`;
  const analytics = `ARRAY[${analyticsActions.map(sqlLiteral).join(',')}]`;
  const { spaceIdByKey, assetIdByKey, sensorIdByKey, pointIdByRef, nextID } = buildCentralPlantSpatialIdentities(spatialPoints);
  const platformDeviceByName = new Map(centralPlantDevices.map((device) => [device.name, device]));

  const spaceRows = centralPlantSpaces.map((space) => `(${sqlLiteral(spaceIdByKey.get(space.id))},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${space.parentId ? sqlLiteral(spaceIdByKey.get(space.parentId)) : 'NULL'},${sqlLiteral(space.code)},${sqlLiteral(space.name)},${sqlLiteral(space.type)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const assetRows = centralPlantAssets.map((asset) => `(${sqlLiteral(assetIdByKey.get(asset.id))},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(asset.code)},${sqlLiteral(asset.name)},${sqlLiteral(asset.type)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const deviceRows = centralPlantDevices.map((device) => `(${sqlLiteral(device.platformDeviceId)},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(device.slug)},${sqlLiteral(device.name)},${sqlLiteral(device.type)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const assetSpaceRows = centralPlantAssets.map((asset) => `(${sqlLiteral(nextID())},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(assetIdByKey.get(asset.id))},${sqlLiteral(spaceIdByKey.get(asset.areaId))},'INSTALLED_IN','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const deviceSpaceRows = centralPlantDeviceEndpoints.map((device) => `(${sqlLiteral(nextID())},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(device.platformDeviceId)},${sqlLiteral(spaceIdByKey.get(device.areaId))},'INSTALLED_IN','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const deviceBindingRows = centralPlantDeviceEndpoints.flatMap((endpoint) => endpoint.equipmentIds.map((assetKey) => {
    const contract = platformDeviceByName.get(endpoint.id);
    return `(${sqlLiteral(nextID())},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(endpoint.platformDeviceId)},${sqlLiteral(assetIdByKey.get(assetKey))},${sqlLiteral(bindingRoleForDeviceType(contract.type))},'ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`;
  })).join(',\n  ');

  const sensorRows = centralPlantSensors.map((sensor) => `(${sqlLiteral(sensorIdByKey.get(sensor.id))},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(sensor.id)},${sqlLiteral(sensor.name)},${sqlLiteral(sensor.type)},NULL,NULL,${sqlLiteral(sensor.serialNumber)},${sqlLiteral(sensor.calibrationDueAt)},${sqlJSON({ physicalTraceability: true })},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const sensorDeviceRows = centralPlantSensors.map((sensor) => {
    const device = platformDeviceByName.get(sensor.deviceId);
    return `(${sqlLiteral(nextID())},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(sensorIdByKey.get(sensor.id))},${sqlLiteral(device.platformDeviceId)},'REPORTS_THROUGH','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`;
  }).join(',\n  ');
  const sensorSpaceRows = centralPlantSensors.map((sensor) => `(${sqlLiteral(nextID())},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(sensorIdByKey.get(sensor.id))},${sqlLiteral(spaceIdByKey.get(sensor.mountedAreaId))},'MOUNTED_IN','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');

  const pointRows = spatialPoints.map((point) => {
    const platformDevice = platformDeviceByName.get(point.deviceId);
    const metadata = {
      ...(point.sourceMetadata ?? {}),
      ...(point.sourceProtocol ? { protocol: point.sourceProtocol, address: point.sourceAddress } : {}),
    };
    return `(${sqlLiteral(pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`))},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(platformDevice.platformDeviceId)},${point.sensorId ? sqlLiteral(sensorIdByKey.get(point.sensorId)) : 'NULL'},${sqlLiteral(point.pointCode)},${sqlLiteral(point.sourceKey)},${sqlLiteral(point.name)},${sqlLiteral(point.pointType)},${sqlLiteral(point.valueType)},${point.unit ? sqlLiteral(point.unit) : 'NULL'},${point.writable ? 'true' : 'false'},${durationMilliseconds(point.sampleInterval)},${durationMilliseconds(point.publishInterval)},${durationMilliseconds(point.staleAfter)},${sqlJSON(metadata)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`;
  }).join(',\n  ');
  const pointSubjectRows = spatialPoints.map((point) => {
    const subjectType = canonicalSubjectType(point.subjectType);
    const spaceID = subjectType === 'SPACE' ? sqlLiteral(spaceIdByKey.get(point.subjectId)) : 'NULL';
    const assetID = subjectType === 'ASSET' ? sqlLiteral(assetIdByKey.get(point.subjectId)) : 'NULL';
    return `(${sqlLiteral(nextID())},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`))},${sqlLiteral(subjectType)},${spaceID},${assetID},${sqlLiteral(point.pointType === 'COMMAND' ? 'CONTROLS' : 'DESCRIBES')},'ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`;
  }).join(',\n  ');

  const scopeRows = centralPlantDevices.map((device) => `(${sqlLiteral(nextID())},${sqlLiteral(tenantId)},${sqlLiteral(principalId)},${sqlLiteral(siteId)},${sqlLiteral(device.platformDeviceId)},${actions},'ALLOW','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const keyRows = centralPlantDevices.flatMap((device) => (pointKeysByDevice.get(device.platformDeviceId) ?? []).map((key) => `(${sqlLiteral(nextID())},${sqlLiteral(tenantId)},${sqlLiteral(principalId)},${sqlLiteral(device.platformDeviceId)},${sqlLiteral(key)},${actions},'ALLOW','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`)).join(',\n  ');

  return `BEGIN;
INSERT INTO iam.tenants (id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(tenantId)}, 'central-plant-local', '中央机房本地验证租户', 'Asia/Shanghai', 'CNY', 'CN', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.sites (id, tenant_id, code, display_name, timezone, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(siteId)}, ${sqlLiteral(tenantId)}, 'central-plant', '中央机房', 'Asia/Shanghai', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, display_name=EXCLUDED.display_name, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.spaces (id, tenant_id, site_id, parent_space_id, code, display_name, space_type, status, revision, created_at, updated_at) VALUES
  ${spaceRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, parent_space_id=EXCLUDED.parent_space_id, display_name=EXCLUDED.display_name, space_type=EXCLUDED.space_type, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.assets (id, tenant_id, site_id, code, display_name, asset_type, status, revision, created_at, updated_at) VALUES
  ${assetRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, display_name=EXCLUDED.display_name, asset_type=EXCLUDED.asset_type, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.devices (id, tenant_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at) VALUES
  ${deviceRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, display_name=EXCLUDED.display_name, device_type=EXCLUDED.device_type, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.asset_space_bindings (id, tenant_id, site_id, asset_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${assetSpaceRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.device_space_bindings (id, tenant_id, site_id, device_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${deviceSpaceRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.device_bindings (id, tenant_id, site_id, device_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${deviceBindingRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.sensors (id, tenant_id, site_id, code, display_name, sensor_type, manufacturer, model, serial_number, calibration_due_at, metadata, status, revision, created_at, updated_at) VALUES
  ${sensorRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, display_name=EXCLUDED.display_name, sensor_type=EXCLUDED.sensor_type, serial_number=EXCLUDED.serial_number, calibration_due_at=EXCLUDED.calibration_due_at, metadata=EXCLUDED.metadata, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.sensor_device_bindings (id, tenant_id, site_id, sensor_id, device_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${sensorDeviceRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.sensor_space_bindings (id, tenant_id, site_id, sensor_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${sensorSpaceRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.telemetry_points (id, tenant_id, site_id, reporting_device_id, sensor_id, point_code, source_key, display_name, point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, source_metadata, status, revision, created_at, updated_at) VALUES
  ${pointRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, display_name=EXCLUDED.display_name, point_type=EXCLUDED.point_type, value_type=EXCLUDED.value_type, unit=EXCLUDED.unit, sample_interval_ms=EXCLUDED.sample_interval_ms, publish_interval_ms=EXCLUDED.publish_interval_ms, stale_after_ms=EXCLUDED.stale_after_ms, source_metadata=EXCLUDED.source_metadata, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${pointSubjectRows} ON CONFLICT DO NOTHING;
INSERT INTO iam.principals (id, external_issuer, external_subject, display_name, email, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(principalId)}, ${sqlLiteral(oidcIssuer)}, ${sqlLiteral(principalSubject)}, 'Central Plant Operator', 'operator@example.test', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET external_issuer=EXCLUDED.external_issuer, external_subject=EXCLUDED.external_subject, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO iam.tenant_memberships (id, tenant_id, principal_id, status, valid_from, valid_to, revision, created_at, updated_at)
VALUES (${sqlLiteral(nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(principalId)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()) ON CONFLICT DO NOTHING;
INSERT INTO iam.role_bindings (id, tenant_id, principal_id, role_key, actions, effect, valid_from, valid_to, revision, created_at, updated_at) VALUES
  (${sqlLiteral(nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(principalId)}, 'registry-reader', ARRAY['registry.read'], 'ALLOW', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()),
  (${sqlLiteral(nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(principalId)}, 'telemetry-reader', ${actions}, 'ALLOW', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()) ON CONFLICT DO NOTHING;
INSERT INTO iam.site_bindings (id, tenant_id, site_id, principal_id, actions, effect, valid_from, valid_to, revision, created_at, updated_at)
VALUES (${sqlLiteral(nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(principalId)}, ${analytics}, 'ALLOW', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()) ON CONFLICT DO NOTHING;
INSERT INTO iam.policies (id, tenant_id, policy_key, policy_revision, status, document, created_at, updated_at) VALUES
  (${sqlLiteral(nextID())}, ${sqlLiteral(tenantId)}, 'registry-read', 1, 'ACTIVE', '{"denyPrecedence":true,"action":"registry.read"}', clock_timestamp(), clock_timestamp()),
  (${sqlLiteral(nextID())}, ${sqlLiteral(tenantId)}, 'telemetry-access', 1, 'ACTIVE', '{"denyPrecedence":true,"exactDeviceKeyScope":true}', clock_timestamp(), clock_timestamp()) ON CONFLICT DO NOTHING;
INSERT INTO iam.telemetry_scope_bindings (id, tenant_id, principal_id, site_id, device_id, actions, effect, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${scopeRows} ON CONFLICT DO NOTHING;
INSERT INTO iam.telemetry_key_bindings (id, tenant_id, principal_id, device_id, telemetry_key, actions, effect, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${keyRows} ON CONFLICT DO NOTHING;
COMMIT;
`;
}

export function buildS2SeedSQL({ pointsByDevice, spatialPoints = [] }) {
  const { tenantId, siteId, integrationInstanceId } = centralPlantIdentity;
  const { sensorIdByKey, pointIdByRef } = buildCentralPlantSpatialIdentities(spatialPoints);
  const platformDeviceByName = new Map(centralPlantDevices.map((device) => [device.name, device]));
  const bindingRows = centralPlantDevices.map((device) => `(${sqlLiteral(tenantId)},${sqlLiteral(device.platformDeviceId)},${sqlLiteral(siteId)},${sqlLiteral(integrationInstanceId)},'DEVICE',${sqlLiteral(device.platformDeviceId)},'ACTIVE',1,1,clock_timestamp(),NULL,clock_timestamp())`).join(',\n  ');
  const pointBindingRows = spatialPoints.map((point, index) => {
    const platformDevice = platformDeviceByName.get(point.deviceId);
    if (!platformDevice) throw new Error(`S2 Point projection references unknown Device ${point.deviceId}`);
    const pointID = pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`);
    const sensorID = point.sensorId ? sensorIdByKey.get(point.sensorId) : null;
    if (!pointID) throw new Error(`S2 Point projection identity is missing for ${point.deviceId}/${point.telemetryKey}`);
    if (point.sensorId && !sensorID) throw new Error(`S2 Point projection references unknown Sensor ${point.sensorId}`);
    return `(${sqlLiteral(localUUID(0x600000000000 + index + 1))},${sqlLiteral(tenantId)},${sqlLiteral(siteId)},${sqlLiteral(pointID)},${sensorID ? sqlLiteral(sensorID) : 'NULL'},${sqlLiteral(platformDevice.platformDeviceId)},${sqlLiteral(point.telemetryKey)},${sqlLiteral(point.pointType)},${sqlLiteral(point.valueType)},${point.unit ? sqlLiteral(point.unit) : 'NULL'},'ACTIVE',1,1,'2000-01-01T00:00:00Z',NULL,clock_timestamp())`;
  }).join(',\n  ');
  const presenceRows = centralPlantDevices.map((device) => {
    const maxSourceLagSeconds = device.name === 'METER-HVAC-TOTAL' ? 7 * 24 * 60 * 60 : 120;
    return `(${sqlLiteral(device.platformDeviceId)},1,30,120,true,ARRAY['SOURCE_ACTIVITY']::text[],15,${maxSourceLagSeconds},clock_timestamp())`;
  }).join(',\n  ');
  const freshnessRows = centralPlantDevices.flatMap((device) => (pointsByDevice.get(device.platformDeviceId) ?? []).map((point) => `(${sqlLiteral(device.platformDeviceId)},${sqlLiteral(point.telemetryKey)},1,30,true,5,${sqlLiteral(point.valueType)},${point.unit ? sqlLiteral(point.unit) : 'NULL'},NULL,NULL,clock_timestamp())`)).join(',\n  ');
  const coverageRows = centralPlantDevices.map((device) => `(${sqlLiteral(device.platformDeviceId)},true,clock_timestamp(),NULL,1,clock_timestamp())`).join(',\n  ');
  return `BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;
DELETE FROM telemetry_runtime.telemetry_publication_outbox;
INSERT INTO telemetry_runtime.registry_device_bindings (tenant_id, device_id, site_id, integration_instance_id, external_entity_type, external_id, binding_status, binding_revision, source_registry_revision, valid_from, valid_to, updated_at) VALUES
  ${bindingRows} ON CONFLICT (device_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, site_id=EXCLUDED.site_id, external_id=EXCLUDED.external_id, binding_status='ACTIVE', updated_at=clock_timestamp();
${pointBindingRows ? `INSERT INTO telemetry_runtime.registry_point_bindings (projection_id, tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key, point_type, value_type, unit, binding_status, point_revision, source_registry_revision, valid_from, valid_to, updated_at) VALUES
  ${pointBindingRows} ON CONFLICT (projection_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, site_id=EXCLUDED.site_id, point_id=EXCLUDED.point_id, sensor_id=EXCLUDED.sensor_id, device_id=EXCLUDED.device_id, telemetry_key=EXCLUDED.telemetry_key, point_type=EXCLUDED.point_type, value_type=EXCLUDED.value_type, unit=EXCLUDED.unit, binding_status='ACTIVE', point_revision=EXCLUDED.point_revision, source_registry_revision=EXCLUDED.source_registry_revision, valid_from=EXCLUDED.valid_from, valid_to=NULL, updated_at=clock_timestamp();` : ''}
INSERT INTO telemetry_runtime.presence_policies (device_id, policy_revision, online_within_seconds, offline_after_seconds, coverage_required, accepted_signal_types, max_future_clock_skew_seconds, max_source_lag_seconds, updated_at) VALUES
  ${presenceRows} ON CONFLICT (device_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision, max_future_clock_skew_seconds=EXCLUDED.max_future_clock_skew_seconds, max_source_lag_seconds=EXCLUDED.max_source_lag_seconds, updated_at=clock_timestamp();
INSERT INTO telemetry_runtime.freshness_policies (device_id, telemetry_key, policy_revision, fresh_within_seconds, configured, expected_sample_interval_seconds, value_type, expected_unit, minimum_number, maximum_number, updated_at) VALUES
  ${freshnessRows} ON CONFLICT (device_id, telemetry_key) DO UPDATE SET value_type=EXCLUDED.value_type, expected_unit=EXCLUDED.expected_unit, configured=true, updated_at=clock_timestamp();
INSERT INTO telemetry_runtime.observation_coverage (device_id, available, continuous_since, reason_code, source_revision, updated_at) VALUES
  ${coverageRows} ON CONFLICT (device_id) DO UPDATE SET available=true, reason_code=NULL, updated_at=clock_timestamp();
RESET ROLE;
COMMIT;
`;
}

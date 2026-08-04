import {
  analyticsActions,
  centralPlantDevices,
  centralPlantIdentity,
  localUUID,
  sqlLiteral,
  telemetryActions,
} from './central-plant-local-contract.mjs';
import {
  centralPlantAreas,
  centralPlantDeviceEndpoints,
  centralPlantEquipment,
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

export function buildS1SeedSQL({ oidcIssuer, pointKeysByDevice, spatialPoints = [] }) {
  const { organizationId, siteId, principalId } = centralPlantIdentity;
  const actions = `ARRAY[${telemetryActions.map(sqlLiteral).join(',')}]`;
  const analytics = `ARRAY[${analyticsActions.map(sqlLiteral).join(',')}]`;
  let sequence = 1;
  const nextID = () => localUUID(sequence++);

  const areaIdByKey = new Map(centralPlantAreas.map((area) => [area.id, nextID()]));
  const equipmentIdByKey = new Map(centralPlantEquipment.map((equipment) => [equipment.id, nextID()]));
  const sensorIdByKey = new Map(centralPlantSensors.map((sensor) => [sensor.id, nextID()]));
  const pointIdByRef = new Map(spatialPoints.map((point) => [`${point.deviceId}/${point.telemetryKey}`, nextID()]));
  const platformDeviceByName = new Map(centralPlantDevices.map((device) => [device.name, device]));

  const areaRows = centralPlantAreas.map((area) => `(${sqlLiteral(areaIdByKey.get(area.id))},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${area.parentId ? sqlLiteral(areaIdByKey.get(area.parentId)) : 'NULL'},${sqlLiteral(area.code)},${sqlLiteral(area.name)},${sqlLiteral(area.type)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const equipmentRows = centralPlantEquipment.map((equipment) => `(${sqlLiteral(equipmentIdByKey.get(equipment.id))},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(equipment.code)},${sqlLiteral(equipment.name)},${sqlLiteral(equipment.type)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const deviceRows = centralPlantDevices.map((device) => `(${sqlLiteral(device.platformDeviceId)},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(device.slug)},${sqlLiteral(device.name)},${sqlLiteral(device.type)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const equipmentAreaRows = centralPlantEquipment.map((equipment) => `(${sqlLiteral(nextID())},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(equipmentIdByKey.get(equipment.id))},${sqlLiteral(areaIdByKey.get(equipment.areaId))},'INSTALLED_IN','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const deviceAreaRows = centralPlantDeviceEndpoints.map((device) => `(${sqlLiteral(nextID())},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(device.platformDeviceId)},${sqlLiteral(areaIdByKey.get(device.areaId))},'INSTALLED_IN','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const deviceBindingRows = centralPlantDeviceEndpoints.flatMap((endpoint) => endpoint.equipmentIds.map((equipmentId) => {
    const contract = platformDeviceByName.get(endpoint.id);
    return `(${sqlLiteral(nextID())},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(endpoint.platformDeviceId)},${sqlLiteral(equipmentIdByKey.get(equipmentId))},${sqlLiteral(bindingRoleForDeviceType(contract.type))},'ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`;
  })).join(',\n  ');

  const sensorRows = centralPlantSensors.map((sensor) => `(${sqlLiteral(sensorIdByKey.get(sensor.id))},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(sensor.id)},${sqlLiteral(sensor.name)},${sqlLiteral(sensor.type)},NULL,NULL,NULL,NULL,${sqlJSON({ mode: sensor.mode })},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const sensorDeviceRows = centralPlantSensors.map((sensor) => {
    const device = platformDeviceByName.get(sensor.deviceId);
    const role = sensor.mode === 'INDEPENDENT_DEVICE' ? 'INDEPENDENT_DEVICE' : 'REPORTS_THROUGH';
    return `(${sqlLiteral(nextID())},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(sensorIdByKey.get(sensor.id))},${sqlLiteral(device.platformDeviceId)},${sqlLiteral(role)},'ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`;
  }).join(',\n  ');
  const sensorAreaRows = centralPlantSensors.map((sensor) => `(${sqlLiteral(nextID())},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(sensorIdByKey.get(sensor.id))},${sqlLiteral(areaIdByKey.get(sensor.mountedAreaId))},'MOUNTED_IN','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const sensorSubjectRows = centralPlantSensors.map((sensor) => `(${sqlLiteral(nextID())},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(sensorIdByKey.get(sensor.id))},${sqlLiteral(sensor.subjectType)},${sensor.subjectType === 'AREA' ? sqlLiteral(areaIdByKey.get(sensor.subjectId)) : 'NULL'},${sensor.subjectType === 'EQUIPMENT' ? sqlLiteral(equipmentIdByKey.get(sensor.subjectId)) : 'NULL'},'MEASURES','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');

  const pointRows = spatialPoints.map((point) => {
    const platformDevice = platformDeviceByName.get(point.deviceId);
    const metadata = point.sourceProtocol ? { protocol: point.sourceProtocol, address: point.sourceAddress } : {};
    return `(${sqlLiteral(pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`))},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(platformDevice.platformDeviceId)},${point.sensorId ? sqlLiteral(sensorIdByKey.get(point.sensorId)) : 'NULL'},${sqlLiteral(point.telemetryKey)},${sqlLiteral(point.sourceKey)},${sqlLiteral(point.name)},${sqlLiteral(point.kind)},${sqlLiteral(point.valueType)},${point.unit ? sqlLiteral(point.unit) : 'NULL'},${point.writable ? 'true' : 'false'},${durationMilliseconds(point.sampleInterval)},${durationMilliseconds(point.publishInterval)},${durationMilliseconds(point.staleAfter)},${point.formulaRevision ? sqlLiteral(point.formulaRevision) : 'NULL'},${sqlJSON(metadata)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`;
  }).join(',\n  ');
  const pointSubjectRows = spatialPoints.map((point) => `(${sqlLiteral(nextID())},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`))},${sqlLiteral(point.subjectType)},${point.subjectType === 'AREA' ? sqlLiteral(areaIdByKey.get(point.subjectId)) : 'NULL'},${point.subjectType === 'EQUIPMENT' ? sqlLiteral(equipmentIdByKey.get(point.subjectId)) : 'NULL'},${sqlLiteral(point.kind === 'COMMAND' ? 'CONTROLS' : point.kind === 'CALCULATED' ? 'AGGREGATES' : 'DESCRIBES')},'ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const calculatedInputRows = spatialPoints.flatMap((point) => (point.inputPointRefs ?? []).map((inputRef, index) => `(${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`))},${sqlLiteral(pointIdByRef.get(inputRef))},${sqlLiteral(`input-${index + 1}`)},${index},${sqlLiteral(point.formulaRevision)})`)).join(',\n  ');

  const scopeRows = centralPlantDevices.map((device) => `(${sqlLiteral(nextID())},${sqlLiteral(principalId)},${sqlLiteral(organizationId)},${sqlLiteral(organizationId)},${sqlLiteral(siteId)},${sqlLiteral(device.platformDeviceId)},${actions},'ALLOW','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const keyRows = centralPlantDevices.flatMap((device) => (pointKeysByDevice.get(device.platformDeviceId) ?? []).map((key) => `(${sqlLiteral(nextID())},${sqlLiteral(principalId)},${sqlLiteral(organizationId)},${sqlLiteral(device.platformDeviceId)},${sqlLiteral(key)},${actions},'ALLOW','ACTIVE',clock_timestamp(),NULL,1,clock_timestamp(),clock_timestamp())`)).join(',\n  ');

  return `BEGIN;
INSERT INTO core_registry.organizations (id, code, display_name, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(organizationId)}, 'central-plant-local', '中央机房本地验证组织', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.sites (id, organization_id, code, display_name, timezone, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(siteId)}, ${sqlLiteral(organizationId)}, 'central-plant', '中央机房', 'Asia/Shanghai', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.areas (id, organization_id, site_id, parent_area_id, code, display_name, area_type, status, revision, created_at, updated_at) VALUES
  ${areaRows}
ON CONFLICT (id) DO UPDATE SET parent_area_id=EXCLUDED.parent_area_id, display_name=EXCLUDED.display_name, area_type=EXCLUDED.area_type, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.equipment (id, organization_id, site_id, code, display_name, equipment_type, status, revision, created_at, updated_at) VALUES
  ${equipmentRows}
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, equipment_type=EXCLUDED.equipment_type, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.devices (id, organization_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at) VALUES
  ${deviceRows}
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, device_type=EXCLUDED.device_type, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.equipment_area_bindings (id, organization_id, site_id, equipment_id, area_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${equipmentAreaRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.device_area_bindings (id, organization_id, site_id, device_id, area_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${deviceAreaRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.device_bindings (id, organization_id, site_id, device_id, equipment_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${deviceBindingRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.sensors (id, organization_id, site_id, code, display_name, sensor_type, manufacturer, model, serial_number, calibration_due_at, metadata, status, revision, created_at, updated_at) VALUES
  ${sensorRows}
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, sensor_type=EXCLUDED.sensor_type, metadata=EXCLUDED.metadata, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.sensor_device_bindings (id, organization_id, site_id, sensor_id, device_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${sensorDeviceRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.sensor_area_bindings (id, organization_id, site_id, sensor_id, area_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${sensorAreaRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.sensor_subject_bindings (id, organization_id, site_id, sensor_id, subject_type, area_id, equipment_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${sensorSubjectRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.telemetry_points (id, organization_id, site_id, reporting_device_id, sensor_id, point_key, source_key, display_name, point_kind, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, formula_revision, source_metadata, status, revision, created_at, updated_at) VALUES
  ${pointRows}
ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, point_kind=EXCLUDED.point_kind, value_type=EXCLUDED.value_type, unit=EXCLUDED.unit, sample_interval_ms=EXCLUDED.sample_interval_ms, publish_interval_ms=EXCLUDED.publish_interval_ms, stale_after_ms=EXCLUDED.stale_after_ms, formula_revision=EXCLUDED.formula_revision, source_metadata=EXCLUDED.source_metadata, status='ACTIVE', updated_at=clock_timestamp();
INSERT INTO core_registry.point_subject_bindings (id, organization_id, site_id, point_id, subject_type, area_id, equipment_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
  ${pointSubjectRows} ON CONFLICT DO NOTHING;
INSERT INTO core_registry.calculated_point_inputs (organization_id, site_id, calculated_point_id, input_point_id, input_role, ordinal, formula_revision) VALUES
  ${calculatedInputRows} ON CONFLICT DO NOTHING;
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

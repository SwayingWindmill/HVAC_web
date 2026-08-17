import { chmodSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';

import {
  centralPlantDevices,
  centralPlantIdentity,
  localUUID,
  sqlLiteral,
} from './central-plant-local-contract.mjs';
import {
  buildCentralPlantSimulatorPoints,
  centralPlantAreas,
  centralPlantDeviceEndpoints,
  centralPlantEquipment,
  centralPlantSensors,
} from './central-plant-spatial-model.mjs';

const repoRoot = path.resolve(process.env.PHASE1_REPO_ROOT || process.cwd());
const postgresContainer = process.env.PHASE1_POSTGRES_CONTAINER || 'hvac-phase1-postgres-1';
const runtimeRoot = path.join(repoRoot, 'deploy', 'platform', 'phase1', 'runtime');
const internalPkiDir = path.join(runtimeRoot, 'internal-pki');
const simulatorPkiDir = path.join(internalPkiDir, 'eg8200-simulator');
const simulatorQueueDir = path.join(runtimeRoot, 'data', 'eg8200');
const runtimeConfigDir = path.join(runtimeRoot, 'config');
const mqttConfigPath = path.join(runtimeConfigDir, 'eg8200-mqtt.json');
const pointContractPath = path.join(repoRoot, 'contracts', 'registry', 'central-plant-device-points.v2.json');
const reconciliationPath = path.join(runtimeRoot, 'identity-reconcile.json');

const telemetryActions = [
  'telemetry.batch.read',
  'telemetry.history.read',
  'telemetry.recovery.checkpoint',
  'telemetry.recovery.use',
  'telemetry.resubscribe',
  'telemetry.snapshot.read',
  'telemetry.subscribe',
];

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: repoRoot,
    encoding: 'utf8',
    stdio: options.input ? ['pipe', 'inherit', 'inherit'] : 'inherit',
    input: options.input,
    env: { ...process.env, ...(options.env ?? {}) },
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} exited with status ${result.status}`);
}

function psql(database, sql) {
  run('docker', [
    'exec', '-i', postgresContainer,
    'psql', '-U', 'postgres', '-d', database,
    '-v', 'ON_ERROR_STOP=1',
  ], { input: sql });
}

function sqlJson(value) {
  return `${sqlLiteral(JSON.stringify(value))}::jsonb`;
}

function durationMilliseconds(value) {
  const match = /^(\d+)(ms|s|m|h)$/.exec(value);
  if (!match) throw new Error(`unsupported duration ${value}`);
  const multiplier = { ms: 1, s: 1_000, m: 60_000, h: 3_600_000 }[match[2]];
  return Number(match[1]) * multiplier;
}

function bindingRoleForDeviceType(deviceType) {
  if (deviceType === 'HVAC_POWER_METER' || deviceType === 'BTU_METER') return 'METER';
  if (deviceType === 'WEATHER_STATION') return 'SENSOR';
  return 'CONTROLLER';
}

function buildIdentities(points) {
  let sequence = 1;
  const nextID = () => localUUID(sequence++);
  const spaceIdByKey = new Map(centralPlantAreas.map((space) => [space.id, nextID()]));
  const assetIdByKey = new Map(centralPlantEquipment.map((asset) => [asset.id, nextID()]));
  const sensorIdByKey = new Map(centralPlantSensors.map((sensor) => [sensor.id, nextID()]));
  const pointIdByRef = new Map(points.map((point) => [`${point.deviceId}/${point.telemetryKey}`, nextID()]));
  return { nextID, spaceIdByKey, assetIdByKey, sensorIdByKey, pointIdByRef };
}

function buildS1Seed(points) {
  const { tenantId, siteId } = centralPlantIdentity;
  const ids = buildIdentities(points);
  const deviceByName = new Map(centralPlantDevices.map((device) => [device.name, device]));

  const spaces = centralPlantAreas.map((space) => `(
    ${sqlLiteral(ids.spaceIdByKey.get(space.id))}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${space.parentId ? sqlLiteral(ids.spaceIdByKey.get(space.parentId)) : 'NULL'},
    ${sqlLiteral(space.code)}, ${sqlLiteral(space.name)}, ${sqlLiteral(space.type)},
    'ACTIVE', 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');

  const assets = centralPlantEquipment.map((asset) => `(
    ${sqlLiteral(ids.assetIdByKey.get(asset.id))}, ${sqlLiteral(siteId)}, ${sqlLiteral(asset.code)},
    ${sqlLiteral(asset.name)}, ${sqlLiteral(asset.type)}, 'ACTIVE', 1,
    clock_timestamp(), clock_timestamp(), ${sqlLiteral(tenantId)}
  )`).join(',\n');

  const assetSpaceBindings = centralPlantEquipment.map((asset) => `(
    ${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(ids.assetIdByKey.get(asset.id))}, ${sqlLiteral(ids.spaceIdByKey.get(asset.areaId))},
    'INSTALLED_IN', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');

  const devices = centralPlantDeviceEndpoints.map((endpoint) => {
    const contract = deviceByName.get(endpoint.id);
    return `(
      ${sqlLiteral(endpoint.platformDeviceId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(contract.slug)},
      ${sqlLiteral(endpoint.name)}, ${sqlLiteral(endpoint.type)}, 'ACTIVE', 1,
      clock_timestamp(), clock_timestamp(), ${sqlLiteral(tenantId)}, NULL, NULL
    )`;
  }).join(',\n');

  const deviceSpaceBindings = centralPlantDeviceEndpoints.map((endpoint) => `(
    ${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(endpoint.platformDeviceId)}, ${sqlLiteral(ids.spaceIdByKey.get(endpoint.areaId))},
    'INSTALLED_IN', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');

  const deviceBindings = centralPlantDeviceEndpoints.flatMap((endpoint) => endpoint.equipmentIds.map((assetKey) => {
    const contract = deviceByName.get(endpoint.id);
    return `(
      ${sqlLiteral(ids.nextID())}, ${sqlLiteral(siteId)}, ${sqlLiteral(endpoint.platformDeviceId)},
      ${sqlLiteral(ids.assetIdByKey.get(assetKey))}, ${sqlLiteral(bindingRoleForDeviceType(contract.type))},
      'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp(), ${sqlLiteral(tenantId)}
    )`;
  })).join(',\n');

  const sensors = centralPlantSensors.map((sensor) => `(
    ${sqlLiteral(ids.sensorIdByKey.get(sensor.id))}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(sensor.id)}, ${sqlLiteral(sensor.name)}, ${sqlLiteral(sensor.type)}, NULL, NULL,
    ${sqlLiteral(sensor.serialNumber)}, ${sqlLiteral(sensor.calibrationDueAt)},
    ${sqlJson({ physicalTraceability: true, source: 'EG8200_SIMULATOR' })},
    'ACTIVE', 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');

  const sensorDeviceBindings = centralPlantSensors.map((sensor) => `(
    ${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(ids.sensorIdByKey.get(sensor.id))}, ${sqlLiteral(deviceByName.get(sensor.deviceId).platformDeviceId)},
    'REPORTS_THROUGH', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');

  const sensorSpaceBindings = centralPlantSensors.map((sensor) => `(
    ${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(ids.sensorIdByKey.get(sensor.id))}, ${sqlLiteral(ids.spaceIdByKey.get(sensor.mountedAreaId))},
    'MOUNTED_IN', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');

  const telemetryPoints = points.map((point) => {
    const device = deviceByName.get(point.deviceId);
    const metadata = {
      ...(point.sourceMetadata ?? {}),
      protocol: point.sourceProtocol ?? 'SIMULATED',
      address: point.sourceAddress ?? `${point.deviceId}:${point.sourceKey}`,
    };
    return `(
      ${sqlLiteral(ids.pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`))},
      ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(device.platformDeviceId)},
      ${point.sensorId ? sqlLiteral(ids.sensorIdByKey.get(point.sensorId)) : 'NULL'},
      ${sqlLiteral(point.pointCode)}, ${sqlLiteral(point.telemetryKey)}, ${sqlLiteral(point.name)},
      ${sqlLiteral(point.pointType)}, ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'},
      false, ${durationMilliseconds(point.sampleInterval)}, ${durationMilliseconds(point.publishInterval)}, ${durationMilliseconds(point.staleAfter)},
      ${sqlJson(metadata)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), NULL, NULL
    )`;
  }).join(',\n');

  const pointSubjects = points.map((point) => {
    const pointID = ids.pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`);
    if (point.subjectType === 'SITE') {
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'SITE', NULL, NULL, 'DESCRIBES', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`;
    }
    if (point.subjectType === 'EQUIPMENT') {
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'ASSET', NULL, ${sqlLiteral(ids.assetIdByKey.get(point.subjectId))}, 'DESCRIBES', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`;
    }
    if (point.subjectType === 'AREA') {
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'SPACE', ${sqlLiteral(ids.spaceIdByKey.get(point.subjectId))}, NULL, 'DESCRIBES', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`;
    }
    throw new Error(`unsupported point subject ${point.subjectType}`);
  }).join(',\n');

  return `BEGIN;
INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id)
VALUES (${sqlLiteral(siteId)}, 'local-energy-site', '本地智慧能源站点', 'Asia/Shanghai', 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${sqlLiteral(tenantId)})
ON CONFLICT (id) DO UPDATE SET status='ACTIVE', tenant_id=EXCLUDED.tenant_id, updated_at=clock_timestamp();

INSERT INTO core_registry.spaces (id, tenant_id, site_id, parent_space_id, code, display_name, space_type, status, revision, created_at, updated_at) VALUES
${spaces}
ON CONFLICT (id) DO UPDATE SET parent_space_id=EXCLUDED.parent_space_id, code=EXCLUDED.code, display_name=EXCLUDED.display_name, space_type=EXCLUDED.space_type, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.assets (id, site_id, code, display_name, asset_type, status, revision, created_at, updated_at, tenant_id) VALUES
${assets}
ON CONFLICT (id) DO UPDATE SET code=EXCLUDED.code, display_name=EXCLUDED.display_name, asset_type=EXCLUDED.asset_type, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.asset_space_bindings (id, tenant_id, site_id, asset_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${assetSpaceBindings}
ON CONFLICT (id) DO UPDATE SET asset_id=EXCLUDED.asset_id, space_id=EXCLUDED.space_id, binding_role=EXCLUDED.binding_role, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();

INSERT INTO core_registry.devices (id, site_id, code, display_name, device_type, status, revision, created_at, updated_at, tenant_id, product_id, template_version_id) VALUES
${devices}
ON CONFLICT (id) DO UPDATE SET code=EXCLUDED.code, display_name=EXCLUDED.display_name, device_type=EXCLUDED.device_type, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.device_space_bindings (id, tenant_id, site_id, device_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${deviceSpaceBindings}
ON CONFLICT (id) DO UPDATE SET device_id=EXCLUDED.device_id, space_id=EXCLUDED.space_id, binding_role=EXCLUDED.binding_role, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();

INSERT INTO core_registry.device_bindings (id, site_id, device_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at, tenant_id) VALUES
${deviceBindings}
ON CONFLICT (id) DO UPDATE SET device_id=EXCLUDED.device_id, asset_id=EXCLUDED.asset_id, binding_role=EXCLUDED.binding_role, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();

INSERT INTO core_registry.sensors (id, tenant_id, site_id, code, display_name, sensor_type, manufacturer, model, serial_number, calibration_due_at, metadata, status, revision, created_at, updated_at) VALUES
${sensors}
ON CONFLICT (id) DO UPDATE SET code=EXCLUDED.code, display_name=EXCLUDED.display_name, sensor_type=EXCLUDED.sensor_type, serial_number=EXCLUDED.serial_number, calibration_due_at=EXCLUDED.calibration_due_at, metadata=EXCLUDED.metadata, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.sensor_device_bindings (id, tenant_id, site_id, sensor_id, device_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${sensorDeviceBindings}
ON CONFLICT (id) DO UPDATE SET sensor_id=EXCLUDED.sensor_id, device_id=EXCLUDED.device_id, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();

INSERT INTO core_registry.sensor_space_bindings (id, tenant_id, site_id, sensor_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${sensorSpaceBindings}
ON CONFLICT (id) DO UPDATE SET sensor_id=EXCLUDED.sensor_id, space_id=EXCLUDED.space_id, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();

INSERT INTO core_registry.telemetry_points (id, tenant_id, site_id, reporting_device_id, sensor_id, point_code, source_key, display_name, point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, source_metadata, status, revision, created_at, updated_at, point_template_id, template_version_id) VALUES
${telemetryPoints}
ON CONFLICT (id) DO UPDATE SET reporting_device_id=EXCLUDED.reporting_device_id, sensor_id=EXCLUDED.sensor_id, point_code=EXCLUDED.point_code, source_key=EXCLUDED.source_key, display_name=EXCLUDED.display_name, point_type=EXCLUDED.point_type, value_type=EXCLUDED.value_type, unit=EXCLUDED.unit, sample_interval_ms=EXCLUDED.sample_interval_ms, publish_interval_ms=EXCLUDED.publish_interval_ms, stale_after_ms=EXCLUDED.stale_after_ms, source_metadata=EXCLUDED.source_metadata, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${pointSubjects}
ON CONFLICT (id) DO UPDATE SET point_id=EXCLUDED.point_id, subject_type=EXCLUDED.subject_type, space_id=EXCLUDED.space_id, asset_id=EXCLUDED.asset_id, binding_role=EXCLUDED.binding_role, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();
COMMIT;`;
}

function localAdminPrincipalId() {
  if (!existsSync(reconciliationPath)) throw new Error('identity-reconcile.json is required before simulator authorization bootstrap');
  const reconciliation = JSON.parse(readFileSync(reconciliationPath, 'utf8'));
  const principalId = reconciliation.seed?.principalId;
  if (!principalId) throw new Error('identity-reconcile.json is missing seed.principalId');
  return principalId;
}

function buildTelemetryKeyGrants(points, principalId) {
  const { tenantId } = centralPlantIdentity;
  const deviceByName = new Map(centralPlantDevices.map((device) => [device.name, device]));
  const actions = `ARRAY[${telemetryActions.map(sqlLiteral).join(',')}]::text[]`;
  const rows = points.map((point, index) => {
    const device = deviceByName.get(point.deviceId);
    return `(
      ${sqlLiteral(localUUID(0x700000000000 + index + 1))}, ${sqlLiteral(tenantId)},
      ${sqlLiteral(principalId)}, ${sqlLiteral(device.platformDeviceId)}, ${sqlLiteral(point.pointCode)},
      ${actions}, 'ALLOW', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
    )`;
  }).join(',\n');
  return `BEGIN;
INSERT INTO iam.telemetry_key_bindings (id, tenant_id, principal_id, device_id, telemetry_key, actions, effect, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${rows}
ON CONFLICT (id) DO UPDATE
SET tenant_id=EXCLUDED.tenant_id,
    principal_id=EXCLUDED.principal_id,
    device_id=EXCLUDED.device_id,
    telemetry_key=EXCLUDED.telemetry_key,
    actions=EXCLUDED.actions,
    effect='ALLOW',
    status='ACTIVE',
    valid_to=NULL,
    updated_at=clock_timestamp();
COMMIT;`;
}

function buildS2Seed(points) {
  const { tenantId, siteId, integrationInstanceId } = centralPlantIdentity;
  const ids = buildIdentities(points);
  const deviceByName = new Map(centralPlantDevices.map((device) => [device.name, device]));

  const deviceBindings = centralPlantDevices.map((device) => `(
    ${sqlLiteral(device.platformDeviceId)}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(integrationInstanceId)}, 'DEVICE', ${sqlLiteral(device.platformDeviceId)},
    'ACTIVE', 1, 1, clock_timestamp(), NULL, clock_timestamp(), 'APPLICABLE'
  )`).join(',\n');

  const pointBindings = points.map((point, index) => {
    const device = deviceByName.get(point.deviceId);
    return `(
      ${sqlLiteral(localUUID(0x600000000000 + index + 1))}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
      ${sqlLiteral(ids.pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`))},
      ${point.sensorId ? sqlLiteral(ids.sensorIdByKey.get(point.sensorId)) : 'NULL'},
      ${sqlLiteral(device.platformDeviceId)}, ${sqlLiteral(point.pointCode)},
      ${sqlLiteral(point.pointType)}, ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'},
      ${point.pointType === 'COUNTER' ? sqlLiteral('RESET_TO_ZERO') : 'NULL'}, NULL,
      'ACTIVE', 1, 1, '2000-01-01T00:00:00Z', NULL, clock_timestamp()
    )`;
  }).join(',\n');

  const presence = centralPlantDevices.map((device) => `(
    ${sqlLiteral(device.platformDeviceId)}, 1, 30, 120, true, ARRAY['SOURCE_ACTIVITY']::text[], 60,
    ${device.name === 'METER-HVAC-TOTAL' ? 604800 : 120}, clock_timestamp()
  )`).join(',\n');

  const freshness = points.map((point) => {
    const device = deviceByName.get(point.deviceId);
    return `(
      ${sqlLiteral(device.platformDeviceId)}, ${sqlLiteral(point.pointCode)}, 1, 30, true,
      ${Math.max(1, Math.round(durationMilliseconds(point.sampleInterval) / 1000))},
      ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'}, NULL, NULL, clock_timestamp()
    )`;
  }).join(',\n');

  const coverage = centralPlantDevices.map((device) => `(
    ${sqlLiteral(device.platformDeviceId)}, true, clock_timestamp(), NULL, 1, clock_timestamp()
  )`).join(',\n');

  return `BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;
INSERT INTO telemetry_runtime.registry_device_bindings (device_id, tenant_id, site_id, integration_instance_id, external_entity_type, external_id, binding_status, binding_revision, source_registry_revision, valid_from, valid_to, updated_at, presence_applicability) VALUES
${deviceBindings}
ON CONFLICT (device_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, site_id=EXCLUDED.site_id, integration_instance_id=EXCLUDED.integration_instance_id, external_entity_type=EXCLUDED.external_entity_type, external_id=EXCLUDED.external_id, binding_status='ACTIVE', valid_to=NULL, presence_applicability='APPLICABLE', updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.registry_point_bindings (projection_id, tenant_id, site_id, point_id, sensor_id, device_id, telemetry_key, point_type, value_type, unit, counter_decrease_mode, counter_rollover_modulus, binding_status, point_revision, source_registry_revision, valid_from, valid_to, updated_at) VALUES
${pointBindings}
ON CONFLICT (projection_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, site_id=EXCLUDED.site_id, point_id=EXCLUDED.point_id, sensor_id=EXCLUDED.sensor_id, device_id=EXCLUDED.device_id, telemetry_key=EXCLUDED.telemetry_key, point_type=EXCLUDED.point_type, value_type=EXCLUDED.value_type, unit=EXCLUDED.unit, counter_decrease_mode=EXCLUDED.counter_decrease_mode, counter_rollover_modulus=EXCLUDED.counter_rollover_modulus, binding_status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.presence_policies (device_id, policy_revision, online_within_seconds, offline_after_seconds, coverage_required, accepted_signal_types, max_future_clock_skew_seconds, max_source_lag_seconds, updated_at) VALUES
${presence}
ON CONFLICT (device_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision, online_within_seconds=EXCLUDED.online_within_seconds, offline_after_seconds=EXCLUDED.offline_after_seconds, coverage_required=true, accepted_signal_types=EXCLUDED.accepted_signal_types, max_future_clock_skew_seconds=EXCLUDED.max_future_clock_skew_seconds, max_source_lag_seconds=EXCLUDED.max_source_lag_seconds, updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.freshness_policies (device_id, telemetry_key, policy_revision, fresh_within_seconds, configured, expected_sample_interval_seconds, value_type, expected_unit, minimum_number, maximum_number, updated_at) VALUES
${freshness}
ON CONFLICT (device_id, telemetry_key) DO UPDATE SET policy_revision=EXCLUDED.policy_revision, fresh_within_seconds=EXCLUDED.fresh_within_seconds, configured=true, expected_sample_interval_seconds=EXCLUDED.expected_sample_interval_seconds, value_type=EXCLUDED.value_type, expected_unit=EXCLUDED.expected_unit, updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.observation_coverage (device_id, available, continuous_since, reason_code, source_revision, updated_at) VALUES
${coverage}
ON CONFLICT (device_id) DO UPDATE SET available=true, reason_code=NULL, source_revision=EXCLUDED.source_revision, updated_at=clock_timestamp();
RESET ROLE;
COMMIT;`;
}

function ensureSimulatorCertificate() {
  mkdirSync(simulatorPkiDir, { recursive: true, mode: 0o700 });
  const keyPath = path.join(simulatorPkiDir, 'tls.key');
  const certPath = path.join(simulatorPkiDir, 'tls.crt');
  const keyReady = existsSync(keyPath) && readFileSync(keyPath).includes('PRIVATE KEY');
  const certReady = existsSync(certPath) && readFileSync(certPath).includes('BEGIN CERTIFICATE');
  if (keyReady && certReady) return;

  const csrPath = path.join(simulatorPkiDir, 'tls.csr');
  const extPath = path.join(simulatorPkiDir, 'tls.ext');
  for (const stalePath of [keyPath, certPath, csrPath, extPath]) rmSync(stalePath, { force: true });
  const caCertPath = path.join(internalPkiDir, 'ca.pem');
  const caKeyPath = path.join(internalPkiDir, 'ca.key');
  const caSerialPath = path.join(internalPkiDir, 'ca.srl');
  if (!existsSync(caCertPath) || !existsSync(caKeyPath)) throw new Error('Phase 1 internal CA is unavailable');

  run('openssl', ['genrsa', '-out', keyPath, '2048']);
  run('openssl', ['req', '-new', '-key', keyPath, '-out', csrPath, '-subj', '/CN=EG8200-COMMERCIAL-001']);
  writeFileSync(extPath, 'extendedKeyUsage=clientAuth\nsubjectAltName=DNS:EG8200-COMMERCIAL-001\n', { mode: 0o600 });
  const serialArgs = existsSync(caSerialPath) ? ['-CAserial', caSerialPath] : ['-CAcreateserial'];
  run('openssl', [
    'x509', '-req', '-in', csrPath, '-CA', caCertPath, '-CAkey', caKeyPath,
    ...serialArgs, '-out', certPath, '-days', '825', '-sha256', '-extfile', extPath,
  ]);
  chmodSync(keyPath, 0o600);
  chmodSync(certPath, 0o644);
  rmSync(csrPath, { force: true });
  rmSync(extPath, { force: true });
}

function writeSimulatorConfig() {
  mkdirSync(runtimeConfigDir, { recursive: true });
  mkdirSync(simulatorQueueDir, { recursive: true });
  const config = {
    schemaVersion: 1,
    tenantId: centralPlantIdentity.tenantId,
    siteId: centralPlantIdentity.siteId,
    brokerUrl: 'tls://mqtt-broker:8883',
    clientId: 'EG8200-COMMERCIAL-001',
    caFile: '/run/hvac/pki/ca.pem',
    certFile: '/run/hvac/pki/eg8200-simulator/tls.crt',
    keyFile: '/run/hvac/pki/eg8200-simulator/tls.key',
    serverName: 'mqtt-broker',
    queueDirectory: '/run/hvac/eg8200',
    maximumQueueBytes: 64 * 1024 * 1024,
    deviceExternalIdByDeviceId: Object.fromEntries(centralPlantDevices.map((device) => [device.name, device.platformDeviceId])),
  };
  writeFileSync(mqttConfigPath, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
}

function runLocalAdminGrant() {
  if (!existsSync(reconciliationPath)) throw new Error('identity-reconcile.json is required before simulator authorization bootstrap');
  run(process.execPath, [path.join(repoRoot, 'scripts', 'phase1-grant-local-admin.mjs')]);
}

function startSimulatorService() {
  run(process.execPath, [
    path.join(repoRoot, 'scripts', 'phase1-wsl-compose.mjs'),
    '--profile', 'local-simulator',
    'up', '-d', '--build', 'eg8200-simulator',
  ]);
}

const pointContract = JSON.parse(readFileSync(pointContractPath, 'utf8'));
const points = buildCentralPlantSimulatorPoints(pointContract);

ensureSimulatorCertificate();
writeSimulatorConfig();
psql('hvac_s1', buildS1Seed(points));
psql('hvac_s2', buildS2Seed(points));
runLocalAdminGrant();
psql('hvac_s1', buildTelemetryKeyGrants(points, localAdminPrincipalId()));
startSimulatorService();

console.log(`Phase 1 central-plant simulator ready: devices=${centralPlantDevices.length}, spaces=${centralPlantAreas.length}, assets=${centralPlantEquipment.length}, sensors=${centralPlantSensors.length}, points=${points.length}`);

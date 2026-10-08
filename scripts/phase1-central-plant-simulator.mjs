import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';

import {
  centralPlantDevices,
  centralPlantIdentity,
  localSites,
  localUUID,
  siteDeviceId,
  siteUUID,
  sqlLiteral,
} from './central-plant-local-contract.mjs';
import {
  assignCentralPlantPointIds,
  buildCentralPlantControlPoints,
  buildCentralPlantSimulatorPoints,
  centralPlantAreas,
  centralPlantDeviceEndpoints,
  centralPlantEquipment,
  centralPlantSensors,
} from './central-plant-spatial-model.mjs';
import { applyRegistryRigToPoints, loadAcceptanceRig } from './lib/acceptance-rig.mjs';
import { localContainer, repoRoot, runtimeDir } from './lib/local-environment.mjs';
import { localAdministratorBrowser } from './lib/local-administrator-browser.mjs';

const postgresContainer = process.env.PHASE1_POSTGRES_CONTAINER || localContainer('postgres');
const runtimeRoot = runtimeDir;
const runtimeConfigDir = path.join(runtimeRoot, 'config');
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

function buildIdentities(site, points) {
  let sequence = 1;
  let commandSequence = 1;
  const nextID = () => siteUUID(site, sequence++);
  const spaceIdByKey = new Map(centralPlantAreas.map((space) => [space.id, nextID()]));
  const assetIdByKey = new Map(centralPlantEquipment.map((asset) => [asset.id, nextID()]));
  const sensorIdByKey = new Map(centralPlantSensors.map((sensor) => [sensor.id, nextID()]));
  const pointIdByRef = new Map(points.map((point) => {
    const expected = point.pointType === 'COMMAND' ? siteUUID(site, 0x500000000000 + commandSequence++) : nextID();
    if (site.index === 0 && point.pointId && point.pointId !== expected) {
      throw new Error(`point ${point.deviceId}/${point.telemetryKey} canonical pointId ${point.pointId} does not match deterministic Registry identity ${expected}`);
    }
    return [`${point.deviceId}/${point.telemetryKey}`, expected];
  }));
  return { nextID, spaceIdByKey, assetIdByKey, sensorIdByKey, pointIdByRef };
}

// The plant's Devices and endpoints with their platform ids at a Site.
function siteDevices(site) {
  return centralPlantDevices.map((device) => ({ ...device, platformDeviceId: siteDeviceId(site, device.platformDeviceId) }));
}

function siteEndpoints(site) {
  return centralPlantDeviceEndpoints.map((endpoint) => ({ ...endpoint, platformDeviceId: siteDeviceId(site, endpoint.platformDeviceId) }));
}

function buildS1Seed(site, points) {
  const { tenantId, siteId, gatewayDeviceId } = site;
  const ids = buildIdentities(site, points);
  const plantDevices = siteDevices(site);
  const plantEndpoints = siteEndpoints(site);
  const deviceByName = new Map(plantDevices.map((device) => [device.name, device]));

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

  const devices = [
    `(
      ${sqlLiteral(gatewayDeviceId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(site.gatewayCode)},
      ${sqlLiteral(site.gatewayName)}, 'GATEWAY', 'ACTIVE', 1,
      clock_timestamp(), clock_timestamp(), ${sqlLiteral(tenantId)}, NULL, NULL
    )`,
    ...plantEndpoints.map((endpoint) => {
      const contract = deviceByName.get(endpoint.id);
      return `(
      ${sqlLiteral(endpoint.platformDeviceId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(contract.slug)},
      ${sqlLiteral(endpoint.name)}, ${sqlLiteral(endpoint.type)}, 'ACTIVE', 1,
      clock_timestamp(), clock_timestamp(), ${sqlLiteral(tenantId)}, NULL, NULL
    )`;
    }),
  ].join(',\n');
  // Gateway messages name each Device behind the Gateway by its device name.
  const sourceKeys = plantDevices.map((device, index) => `(
    ${sqlLiteral(siteUUID(site, 0x830000000001 + index))}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(gatewayDeviceId)}, ${sqlLiteral(device.name)}, ${sqlLiteral(device.platformDeviceId)},
    'ACTIVE', 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');

  const deviceSpaceBindings = plantEndpoints.map((endpoint) => `(
    ${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(endpoint.platformDeviceId)}, ${sqlLiteral(ids.spaceIdByKey.get(endpoint.areaId))},
    'INSTALLED_IN', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');

  const deviceBindings = plantEndpoints.flatMap((endpoint) => endpoint.equipmentIds.map((assetKey) => {
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
      ${point.writable ? 'true' : 'false'}, ${durationMilliseconds(point.sampleInterval)}, ${durationMilliseconds(point.publishInterval)}, ${durationMilliseconds(point.staleAfter)},
      ${sqlJson(metadata)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), NULL, NULL,
      ${point.pointType === 'COUNTER' ? sqlLiteral('RESET_TO_ZERO') : 'NULL'}, NULL
    )`;
  }).join(',\n');

  const pointSubjects = points.map((point) => {
    const pointID = ids.pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`);
    const bindingRole = point.pointType === 'COMMAND' ? 'CONTROLS' : 'DESCRIBES';
    if (point.subjectType === 'SITE') {
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'SITE', NULL, NULL, ${sqlLiteral(bindingRole)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`;
    }
    if (point.subjectType === 'EQUIPMENT') {
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'ASSET', NULL, ${sqlLiteral(ids.assetIdByKey.get(point.subjectId))}, ${sqlLiteral(bindingRole)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`;
    }
    if (point.subjectType === 'AREA') {
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'SPACE', ${sqlLiteral(ids.spaceIdByKey.get(point.subjectId))}, NULL, ${sqlLiteral(bindingRole)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`;
    }
    throw new Error(`unsupported point subject ${point.subjectType}`);
  }).join(',\n');

  return `BEGIN;
INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id)
VALUES (${sqlLiteral(siteId)}, ${sqlLiteral(site.siteCode)}, ${sqlLiteral(site.siteName)}, 'Asia/Shanghai', 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${sqlLiteral(tenantId)})
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

INSERT INTO core_registry.gateway_device_source_keys (id, tenant_id, site_id, gateway_device_id, source_key, device_id, status, revision, created_at, updated_at) VALUES
${sourceKeys}
ON CONFLICT (id) DO UPDATE SET source_key=EXCLUDED.source_key, device_id=EXCLUDED.device_id, status='ACTIVE', updated_at=clock_timestamp();

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

INSERT INTO core_registry.telemetry_points (id, tenant_id, site_id, reporting_device_id, sensor_id, point_code, source_key, display_name, point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, source_metadata, status, revision, created_at, updated_at, point_template_id, template_version_id, counter_decrease_mode, counter_rollover_modulus) VALUES
${telemetryPoints}
ON CONFLICT (id) DO UPDATE SET reporting_device_id=EXCLUDED.reporting_device_id, sensor_id=EXCLUDED.sensor_id, point_code=EXCLUDED.point_code, source_key=EXCLUDED.source_key, display_name=EXCLUDED.display_name, point_type=EXCLUDED.point_type, value_type=EXCLUDED.value_type, unit=EXCLUDED.unit, sample_interval_ms=EXCLUDED.sample_interval_ms, publish_interval_ms=EXCLUDED.publish_interval_ms, stale_after_ms=EXCLUDED.stale_after_ms, source_metadata=EXCLUDED.source_metadata, counter_decrease_mode=EXCLUDED.counter_decrease_mode, counter_rollover_modulus=EXCLUDED.counter_rollover_modulus, status='ACTIVE', updated_at=clock_timestamp();

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

function buildTelemetryGrants(points, principalId) {
  const actions = `ARRAY[${telemetryActions.map(sqlLiteral).join(',')}]::text[]`;
  const scopes = localSites.map((site) => `(
    ${sqlLiteral(`01a006a0-0000-7000-8000-${String(site.index + 1).padStart(12, '0')}`)},
    ${sqlLiteral(site.tenantId)}, ${sqlLiteral(principalId)}, ${sqlLiteral(site.siteId)},
    NULL, ${actions}, 'ALLOW', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');
  const sites = localSites.map((site) => `(
    ${sqlLiteral(`01a006a0-0030-7000-8000-${String(site.index + 1).padStart(12, '0')}`)},
    ${sqlLiteral(site.tenantId)}, ${sqlLiteral(site.siteId)}, ${sqlLiteral(principalId)},
    ${actions}, 'ALLOW', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
  )`).join(',\n');
  const rows = localSites.flatMap((site) => {
    const deviceByName = new Map(siteDevices(site).map((device) => [device.name, device]));
    return points.map((point, index) => {
      const device = deviceByName.get(point.deviceId);
      return `(
        ${sqlLiteral(siteUUID(site, 0x700000000000 + index + 1))}, ${sqlLiteral(site.tenantId)},
        ${sqlLiteral(principalId)}, ${sqlLiteral(device.platformDeviceId)}, ${sqlLiteral(point.pointCode)},
        ${actions}, 'ALLOW', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
      )`;
    });
  }).join(',\n');
  return `BEGIN;
INSERT INTO iam.site_bindings (id, tenant_id, site_id, principal_id, actions, effect, valid_from, valid_to, revision, created_at, updated_at) VALUES
${sites}
ON CONFLICT (tenant_id, site_id, principal_id) DO UPDATE SET
actions=(SELECT array_agg(DISTINCT action ORDER BY action) FROM unnest(iam.site_bindings.actions || EXCLUDED.actions) AS action),
effect='ALLOW', valid_to=NULL, revision=iam.site_bindings.revision+1, updated_at=clock_timestamp()
WHERE NOT EXCLUDED.actions <@ iam.site_bindings.actions
   OR iam.site_bindings.effect IS DISTINCT FROM 'ALLOW' OR iam.site_bindings.valid_to IS NOT NULL;
INSERT INTO iam.telemetry_scope_bindings (id, tenant_id, principal_id, site_id, device_id, actions, effect, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${scopes}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, principal_id=EXCLUDED.principal_id,
site_id=EXCLUDED.site_id, device_id=NULL, actions=EXCLUDED.actions, effect='ALLOW', status='ACTIVE',
valid_to=NULL, revision=iam.telemetry_scope_bindings.revision+1, updated_at=clock_timestamp()
WHERE (iam.telemetry_scope_bindings.tenant_id, iam.telemetry_scope_bindings.principal_id,
       iam.telemetry_scope_bindings.site_id, iam.telemetry_scope_bindings.device_id,
       iam.telemetry_scope_bindings.actions, iam.telemetry_scope_bindings.effect,
       iam.telemetry_scope_bindings.status, iam.telemetry_scope_bindings.valid_to)
  IS DISTINCT FROM (EXCLUDED.tenant_id, EXCLUDED.principal_id, EXCLUDED.site_id, NULL::uuid,
                    EXCLUDED.actions, 'ALLOW', 'ACTIVE', NULL::timestamptz);
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
    revision=iam.telemetry_key_bindings.revision+1,
    updated_at=clock_timestamp()
WHERE (iam.telemetry_key_bindings.tenant_id, iam.telemetry_key_bindings.principal_id,
       iam.telemetry_key_bindings.device_id, iam.telemetry_key_bindings.telemetry_key,
       iam.telemetry_key_bindings.actions, iam.telemetry_key_bindings.effect,
       iam.telemetry_key_bindings.status, iam.telemetry_key_bindings.valid_to)
  IS DISTINCT FROM (EXCLUDED.tenant_id, EXCLUDED.principal_id, EXCLUDED.device_id, EXCLUDED.telemetry_key,
                    EXCLUDED.actions, 'ALLOW', 'ACTIVE', NULL::timestamptz);
COMMIT;`;
}

function buildS2Seed(site, points, rig) {
  const { tenantId, siteId } = site;
  const plantDevices = siteDevices(site);
  const deviceByName = new Map(plantDevices.map((device) => [device.name, device]));

  // Telemetry learns each Point from the identity Connectivity resolves on the first observation.
  const devices = plantDevices.map((device) => `(
    ${sqlLiteral(device.platformDeviceId)}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, 'APPLICABLE', clock_timestamp()
  )`).join(',\n');

  const presence = plantDevices.map((device) => `(
    ${sqlLiteral(device.platformDeviceId)}, 1, 30, 120, true, ARRAY['SOURCE_ACTIVITY']::text[], 60,
    ${device.name === 'METER-HVAC-TOTAL' ? 604800 : 120}, clock_timestamp()
  )`).join(',\n');

  const freshness = points.map((point) => {
    const device = deviceByName.get(point.deviceId);
    const expectedSeconds = Math.max(1, Math.round(durationMilliseconds(point.sampleInterval) / 1000));
    // A configured policy must keep fresh_within_seconds >= the expected sample interval,
    // so a Device the rig intentionally paces slowly cannot carry the fast window.
    const freshWithinSeconds = Math.max(rig?.runtimeFreshness.freshWithinSeconds ?? 30, expectedSeconds);
    return `(
      ${sqlLiteral(device.platformDeviceId)}, ${sqlLiteral(point.pointCode)}, 1, ${freshWithinSeconds}, true,
      ${expectedSeconds},
      ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'}, NULL, NULL, clock_timestamp()
    )`;
  }).join(',\n');

  const coverage = plantDevices.map((device) => `(
    ${sqlLiteral(device.platformDeviceId)}, true, clock_timestamp(), NULL, 1, clock_timestamp()
  )`).join(',\n');

  return `BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, presence_applicability, updated_at) VALUES
${devices}
ON CONFLICT (device_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, site_id=EXCLUDED.site_id, presence_applicability='APPLICABLE', updated_at=clock_timestamp();

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

function writeSimulatorConfig(site) {
  mkdirSync(runtimeConfigDir, { recursive: true });
  mkdirSync(path.join(runtimeRoot, 'data', site.simulatorQueue), { recursive: true });
  const config = {
    schemaVersion: 6,
    gatewayId: site.gatewayDeviceId,
    brokerUrl: 'tls://mqtt-broker:8883',
    caFile: '/run/hvac/pki/ca.pem',
    certFile: '/run/hvac/eg8200/gateway-identity.pem',
    keyFile: '/run/hvac/eg8200/gateway-identity.pem',
    enrollmentUrl: 'https://nginx',
    enrollmentServerName: 'localhost',
    enrollmentCaFile: '/run/hvac/enrollment-ca.pem',
    serverName: 'mqtt-broker',
    queueDirectory: '/run/hvac/eg8200',
    maximumQueueBytes: 64 * 1024 * 1024,
  };
  writeFileSync(path.join(runtimeConfigDir, site.simulatorConfig), `${JSON.stringify(config, null, 2)}\n`, { mode: 0o640 });
}

function runLocalAdminGrant() {
  if (!existsSync(reconciliationPath)) throw new Error('identity-reconcile.json is required before simulator authorization bootstrap');
  run(process.execPath, [path.join(repoRoot, 'scripts', 'phase1-grant-local-admin.mjs')]);
}

function startSimulatorService() {
  run(process.execPath, [
    path.join(repoRoot, 'scripts', 'phase1-wsl-compose.mjs'),
    'up', '-d', '--build', 'connectivity',
  ]);
  run(process.execPath, [
    path.join(repoRoot, 'scripts', 'phase1-wsl-compose.mjs'),
    '--simulator-acceptance',
    '--profile', 'simulator-acceptance',
    'up', '-d', '--build', ...localSites.map((site) => site.simulatorService),
  ]);
}

const pointContract = JSON.parse(readFileSync(pointContractPath, 'utf8'));
const rawObservedPoints = buildCentralPlantSimulatorPoints(pointContract);
const rawControlPoints = buildCentralPlantControlPoints(rawObservedPoints);
const registryPoints = assignCentralPlantPointIds([...rawObservedPoints, ...rawControlPoints]);

// --rig applies the reviewed live-acceptance profile so the published cadence, the
// Registry staleness contract and the runtime freshness policy come from one source instead of hand-edited runtime state.
const rigArgument = process.argv.indexOf('--rig');
const rigProfilePath = rigArgument >= 0 ? process.argv[rigArgument + 1] : undefined;
const rig = rigProfilePath ? await loadAcceptanceRig(repoRoot, rigProfilePath) : undefined;
const seededPoints = rig ? applyRegistryRigToPoints(rig, registryPoints) : registryPoints;
const observedPoints = seededPoints.filter((point) => point.pointType !== 'COMMAND');
const controlPoints = seededPoints.filter((point) => point.pointType === 'COMMAND');

// Reapply only the local simulator's explicit read permissions without reseeding
// Registry/Telemetry or restarting a running plant.
if (process.argv.includes('--authorization-only')) {
  psql('hvac_s1', buildTelemetryGrants(observedPoints, localAdminPrincipalId()));
  process.exit(0);
}

for (const site of localSites) {
  psql('hvac_s1', buildS1Seed(site, seededPoints));
  psql('hvac_s2', buildS2Seed(site, observedPoints, rig));
}
for (const site of localSites) writeSimulatorConfig(site);
// Credential bootstrap uses the local administrator's explicit Gateway write scopes.
runLocalAdminGrant();
psql('hvac_s1', buildTelemetryGrants(observedPoints, localAdminPrincipalId()));
const publicOrigin = process.env.PLATFORM_PUBLIC_ORIGIN || 'https://localhost:8443';
const { browser, context, principal } = await localAdministratorBrowser(publicOrigin);
try {
  for (const site of localSites) {
    if (existsSync(path.join(runtimeRoot, 'data', site.simulatorQueue, 'gateway-identity.pem'))) continue;
    const response = await context.request.post(`${publicOrigin}/api/v1/gateways/${site.gatewayDeviceId}/enrollment-code`, {
      headers: { 'X-CSRF-Token': principal.session.csrfToken, Origin: publicOrigin },
    });
    if (!response.ok()) throw new Error(`Gateway enrollment bootstrap failed for ${site.name} (HTTP ${response.status()})`);
    const { enrollmentCode } = await response.json();
    process.env[site.index === 0 ? 'EG8200_ENROLLMENT_CODE_A' : 'EG8200_ENROLLMENT_CODE_B'] = enrollmentCode;
  }
  startSimulatorService();
} finally { await browser.close(); }

console.log(`Phase 1 central-plant simulator ready: devices=${centralPlantDevices.length}, spaces=${centralPlantAreas.length}, assets=${centralPlantEquipment.length}, sensors=${centralPlantSensors.length}, observedPoints=${observedPoints.length}, controlPoints=${controlPoints.length}${rig ? `, rig=${rig.profile}` : ''}`);

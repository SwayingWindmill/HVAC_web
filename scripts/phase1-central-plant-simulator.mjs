import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';

import { centralPlantDevices, centralPlantIdentity, localSites, localUUID, siteDeviceId, siteUUID, sqlLiteral, sqlJobId } from './central-plant-local-contract.mjs';
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
INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id, floor_area_m2, climate_zone)
VALUES (${sqlLiteral(siteId)}, ${sqlLiteral(site.siteCode)}, ${sqlLiteral(site.siteName)}, 'Asia/Shanghai', 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${sqlLiteral(tenantId)}, ${site.floorAreaM2}, ${sqlLiteral(site.climateZone)})
-- A profile edited in the Registry stays; the seed only fills one never recorded.
ON CONFLICT (id) DO UPDATE SET status='ACTIVE', tenant_id=EXCLUDED.tenant_id, updated_at=clock_timestamp(),
  floor_area_m2=coalesce(core_registry.sites.floor_area_m2, EXCLUDED.floor_area_m2), climate_zone=coalesce(core_registry.sites.climate_zone, EXCLUDED.climate_zone);

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

// Every plant COUNTER is a PRIMARY meter on the Site's energy topology, so the
// energy projector can resolve each counter delta to an Energy Fact. The total
// meter sits on the grid inlet; each piece of equipment's own meter sits on the
// feed from the plant into it, which makes it a sub-meter inside that total.
const electricity = '01990000-0000-7000-8000-000000000001';
const meteredEquipment = Object.freeze(['CHILLER-01', 'CHWP-01', 'CWP-01', 'CT-01']);
const counterEnergy = Object.freeze({
  'METER-HVAC-TOTAL/energyKwh': { energyTypeId: electricity, from: 'grid', to: 'plant', direction: 'IMPORT' },
  'BTU-METER-01/accumulatedCoolingEnergyKwh': { energyTypeId: '01990000-0000-7000-8000-000000000006', from: 'plant', to: 'building', direction: 'CONSUME' },
  ...Object.fromEntries(meteredEquipment.map((device) => [`${device}/energyKwh`, { energyTypeId: electricity, from: 'plant', to: device, direction: 'CONSUME' }])),
});

function buildEnergyTopologySeed(site, points) {
  const { tenantId, siteId } = site;
  const ids = buildIdentities(site, points);
  const deviceByName = new Map(siteDevices(site).map((device) => [device.name, device]));
  let sequence = 0x600000000001;
  const nextID = () => siteUUID(site, sequence++);
  const topologyVersionId = nextID();
  const nodes = [
    { key: 'grid', type: 'GRID', name: '电网' },
    { key: 'plant', type: 'HVAC', name: '中央冷站' },
    { key: 'building', type: 'BUILDING', name: '建筑冷负荷' },
    ...meteredEquipment.map((device) => ({ key: device, type: 'LOAD', name: device, deviceId: deviceByName.get(device).platformDeviceId })),
  ].map((node) => ({ ...node, id: nextID() }));
  const nodeId = new Map(nodes.map((node) => [node.key, node.id]));
  const meters = points.filter((point) => point.pointType === 'COUNTER').map((point) => {
    const energy = counterEnergy[`${point.deviceId}/${point.sourceKey}`];
    if (!energy) throw new Error(`COUNTER ${point.deviceId}/${point.sourceKey} has no energy metering role`);
    const device = deviceByName.get(point.deviceId);
    return {
      ...energy, edgeId: nextID(), meterId: nextID(), bindingId: nextID(), deviceId: device.platformDeviceId,
      pointId: ids.pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`),
      code: `${device.slug.replaceAll('-', '_')}_${point.pointCode}`, name: `${device.name} ${point.name}`,
    };
  });
  const scope = `${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}`;
  const draft = `WHERE EXISTS (SELECT 1 FROM core_registry.energy_topology_versions WHERE id = ${sqlLiteral(topologyVersionId)} AND status = 'DRAFT')`;
  // A released topology graph is immutable, so nodes and edges are written only
  // while the version is still DRAFT; reruns leave the active version alone.
  return `BEGIN;
INSERT INTO core_registry.energy_topology_versions (id, tenant_id, site_id, version, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(topologyVersionId)}, ${scope}, 1, 'DRAFT', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.energy_nodes (id, tenant_id, site_id, topology_version_id, node_type, name, device_id, status, revision, created_at, updated_at)
SELECT node.* FROM (VALUES
${nodes.map((node) => `  (${sqlLiteral(node.id)}::uuid, ${sqlLiteral(tenantId)}::uuid, ${sqlLiteral(siteId)}::uuid, ${sqlLiteral(topologyVersionId)}::uuid, ${sqlLiteral(node.type)}, ${sqlLiteral(node.name)}, ${node.deviceId ? `${sqlLiteral(node.deviceId)}::uuid` : 'NULL::uuid'}, 'ACTIVE', 1::bigint, clock_timestamp(), clock_timestamp())`).join(',\n')}
) AS node ${draft}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.energy_edges (id, tenant_id, site_id, topology_version_id, from_node_id, to_node_id, energy_type_id, direction, enabled, revision, created_at, updated_at)
SELECT edge.* FROM (VALUES
${meters.map((meter) => `  (${sqlLiteral(meter.edgeId)}::uuid, ${sqlLiteral(tenantId)}::uuid, ${sqlLiteral(siteId)}::uuid, ${sqlLiteral(topologyVersionId)}::uuid, ${sqlLiteral(nodeId.get(meter.from))}::uuid, ${sqlLiteral(nodeId.get(meter.to))}::uuid, ${sqlLiteral(meter.energyTypeId)}::uuid, ${sqlLiteral(meter.direction)}, true, 1::bigint, clock_timestamp(), clock_timestamp())`).join(',\n')}
) AS edge ${draft}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.energy_meters (id, tenant_id, site_id, meter_code, display_name, device_id, energy_type_id, status, revision, created_at, updated_at) VALUES
${meters.map((meter) => `  (${sqlLiteral(meter.meterId)}, ${scope}, ${sqlLiteral(meter.code)}, ${sqlLiteral(meter.name)}, ${sqlLiteral(meter.deviceId)}, ${sqlLiteral(meter.energyTypeId)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.meter_bindings (id, tenant_id, site_id, topology_version_id, energy_edge_id, energy_type_id, meter_id, device_id, point_id, point_type, meter_role, direction, priority, effective_from, version, status, revision, created_at, updated_at) VALUES
${meters.map((meter) => `  (${sqlLiteral(meter.bindingId)}, ${scope}, ${sqlLiteral(topologyVersionId)}, ${sqlLiteral(meter.edgeId)}, ${sqlLiteral(meter.energyTypeId)}, ${sqlLiteral(meter.meterId)}, ${sqlLiteral(meter.deviceId)}, ${sqlLiteral(meter.pointId)}, 'COUNTER', 'PRIMARY', ${sqlLiteral(meter.direction)}, 0, '2026-01-01T00:00:00Z', 1, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.energy_topology_versions
SET status = 'ACTIVE', effective_from = '2026-01-01T00:00:00Z', released_at = clock_timestamp(), revision = revision + 1, updated_at = clock_timestamp()
WHERE id = ${sqlLiteral(topologyVersionId)} AND status = 'DRAFT';
COMMIT;`;
}

// The plant's HVAC demand is a 15-minute Site load metric computed on a schedule, and a
// day-ahead Site load forecast is deployed on it, so forecasting runs on the plant's own
// history. History starts when the simulator does, so the schedule alone builds it.
// Optimization weighs the next day, so the hourly forecast reaches 48 hours ahead: the latest one
// always covers a full day from whenever a run is asked for.
const forecastHorizonMinutes = 2880;

function buildForecastSeed(site, points) {
  const { tenantId, siteId } = site;
  const ids = buildIdentities(site, points);
  const loadPointId = ids.pointIdByRef.get('METER-HVAC-TOTAL/hvac_meter.active_power');
  if (!loadPointId) throw new Error('METER-HVAC-TOTAL/hvac_meter.active_power is required for the Site load metric');
  let sequence = 0x700000000001;
  const nextID = () => siteUUID(site, sequence++);
  const [metricId, metricVersionId, dependencyId, bindingId, scheduleId] = [nextID(), nextID(), nextID(), nextID(), nextID()];
  const [featureSetId, featureSetVersionId, datasetId, modelId, trainingRunId, modelVersionId, deploymentId, forecastScheduleId] =
    [nextID(), nextID(), nextID(), nextID(), nextID(), nextID(), nextID(), nextID()];
  const topologyVersionId = siteUUID(site, 0x600000000001);
  const code = site.siteCode.replaceAll('-', '_');
  const t = sqlLiteral(tenantId);
  const st = sqlLiteral(siteId);
  const id = sqlLiteral;
  return `BEGIN;
INSERT INTO core_registry.metrics (id, tenant_id, metric_code, metric_name, category, status, revision, created_at, updated_at)
VALUES (${id(metricId)}, ${t}, ${id(`plant_load_15min_${code}`)}, '冷站用电负荷（15 分钟）', 'LOAD', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.metric_versions (id, tenant_id, metric_id, version, unit_code, data_type, subject_type, time_granularity, aggregation, calculation_method, formula, quality_policy, effective_from, status, metadata, revision, created_at, updated_at)
VALUES (${id(metricVersionId)}, ${t}, ${id(metricId)}, 1, 'kW', 'NUMBER', 'SITE', '15MIN', 'AVG', 'IDENTITY', NULL, 'STRICT', '2026-01-01T00:00:00Z', 'DRAFT', '{}', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.metric_dependencies (id, tenant_id, metric_version_id, dependency_type, dependency_code, dependency_metric_id, sort_order, required, metadata, revision, created_at, updated_at)
SELECT ${id(dependencyId)}::uuid, ${t}::uuid, ${id(metricVersionId)}::uuid, 'POINT', 'site_load', NULL::uuid, 0, true, '{}'::jsonb, 1::bigint, clock_timestamp(), clock_timestamp()
WHERE EXISTS (SELECT 1 FROM core_registry.metric_versions WHERE id = ${id(metricVersionId)} AND status = 'DRAFT')
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.metric_versions SET status = 'RELEASED', revision = revision + 1, updated_at = clock_timestamp()
WHERE id = ${id(metricVersionId)} AND status = 'DRAFT';
INSERT INTO core_registry.metric_bindings (id, tenant_id, site_id, metric_version_id, metric_id, metric_version, binding_version, subject_type, subject_id, time_granularity, source_definition, effective_from, status, revision, created_at, updated_at)
VALUES (${id(bindingId)}, ${t}, ${st}, ${id(metricVersionId)}, ${id(metricId)}, 1, 1, 'SITE', ${st}, '15MIN', ${id(JSON.stringify({ points: { site_load: loadPointId } }))}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, next_fire_at)
VALUES (${id(scheduleId)}, ${id(`plant-load-15min-${site.siteCode}`)}, '冷站用电负荷每 15 分钟计算', ${t}, ${st}, 'METRIC_WINDOW_CALC', 'CRON', '*/15 * * * *', '5-field-v1', 'UTC', 'FIRE_ONCE', 60, ${id(JSON.stringify({ bindingId }))},
  date_trunc('hour', clock_timestamp()) + ceil(extract(minute FROM clock_timestamp()) / 15 + 0.0001) * interval '15 minutes')
ON CONFLICT (tenant_id, schedule_code) DO NOTHING;

INSERT INTO core_registry.forecast_feature_sets (id, tenant_id, feature_set_code, target, status, revision, created_at, updated_at)
VALUES (${id(featureSetId)}, ${t}, ${id(`plant_load_${code}`)}, 'SITE_LOAD', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_feature_set_versions (id, tenant_id, feature_set_id, version, feature_schema, fallback_schema, status, revision, created_at, updated_at)
VALUES (${id(featureSetVersionId)}, ${t}, ${id(featureSetId)}, 1, ${id(JSON.stringify({ targetMetricVersionId: metricVersionId, features: ['load_history'] }))}, NULL, 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_dataset_snapshots (id, tenant_id, site_id, target, subject_type, subject_id, train_from, train_to, feature_set_version_id, topology_version_id, metric_version_refs, weather_source, data_quality_summary, manifest_uri, manifest_checksum, created_at)
VALUES (${id(datasetId)}, ${t}, ${st}, 'SITE_LOAD', 'SITE', ${st}, '2026-01-01T00:00:00Z', '2026-10-01T00:00:00Z', ${id(featureSetVersionId)}, ${id(topologyVersionId)}, ${id(JSON.stringify([metricVersionId]))}, NULL, '{}', ${id(`local://forecast/${site.siteCode}/plant-load/manifest.json`)}, repeat('0', 64), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_models (id, tenant_id, model_code, target, subject_type, horizon_minutes, granularity, status, revision, created_at, updated_at)
VALUES (${id(modelId)}, ${t}, ${id(`plant_load_day_ahead_${code}`)}, 'SITE_LOAD', 'SITE', ${forecastHorizonMinutes}, '15MIN', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_training_runs (id, tenant_id, site_id, model_id, dataset_snapshot_id, feature_set_version_id, topology_version_id, algorithm, hyperparameters, code_version, evaluation, status, started_at, finished_at, revision, created_at, updated_at)
VALUES (${id(trainingRunId)}, ${t}, ${st}, ${id(modelId)}, ${id(datasetId)}, ${id(featureSetVersionId)}, ${id(topologyVersionId)}, 'BASELINE', '{"method":"TIME_OF_DAY_PROFILE"}', 'local-seed', NULL, 'PENDING', NULL, NULL, 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.forecast_training_runs SET status = 'RUNNING', started_at = clock_timestamp(), revision = revision + 1, updated_at = clock_timestamp()
WHERE id = ${id(trainingRunId)} AND status = 'PENDING';
UPDATE core_registry.forecast_training_runs SET status = 'SUCCEEDED', evaluation = '{"seeded":true}', finished_at = clock_timestamp(), revision = revision + 1, updated_at = clock_timestamp() + interval '1 second'
WHERE id = ${id(trainingRunId)} AND status = 'RUNNING';
INSERT INTO core_registry.forecast_model_versions (id, tenant_id, site_id, model_id, model_version, training_run_id, dataset_snapshot_id, feature_set_version_id, topology_version_id, artifact_uri, artifact_checksum, evaluation, compatibility, status, revision, created_at, updated_at)
VALUES (${id(modelVersionId)}, ${t}, ${st}, ${id(modelId)}, 1, ${id(trainingRunId)}, ${id(datasetId)}, ${id(featureSetVersionId)}, ${id(topologyVersionId)}, ${id(`local://forecast/${site.siteCode}/plant-load/model.bin`)}, repeat('0', 64), '{"seeded":true}', '{"runtime":"go"}', 'CANDIDATE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.forecast_model_versions SET status = 'VALIDATED', revision = revision + 1, updated_at = clock_timestamp() + interval '1 second'
WHERE id = ${id(modelVersionId)} AND status = 'CANDIDATE';
INSERT INTO core_registry.forecast_deployments (id, tenant_id, site_id, target, subject_type, subject_id, model_version_id, model_id, feature_set_version_id, topology_version_id, status, effective_from, revision, created_at, updated_at)
VALUES (${id(deploymentId)}, ${t}, ${st}, 'SITE_LOAD', 'SITE', ${st}, ${id(modelVersionId)}, ${id(modelId)}, ${id(featureSetVersionId)}, ${id(topologyVersionId)}, 'ACTIVE', '2026-01-01T00:00:00Z', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, next_fire_at)
VALUES (${id(forecastScheduleId)}, ${id(`plant-load-forecast-${site.siteCode}`)}, '冷站用电负荷日前预测（每小时）', ${t}, ${st}, 'FORECAST_RUN', 'CRON', '7 * * * *', '5-field-v1', 'UTC', 'FIRE_ONCE', 120,
  ${id(JSON.stringify({ target: 'SITE_LOAD', subjectType: 'SITE', subjectId: siteId }))}, clock_timestamp())
ON CONFLICT (tenant_id, schedule_code) DO NOTHING;
COMMIT;`;
}

// Optimization weighs a chilled-water supply setpoint against yesterday's plant energy and
// cost and the office zone's comfort. Daily energy and cost are tenant Metrics the
// optimizer reads by code, computed per Site each night after the Site's day closes; the
// policy and the AI deployment are tenant-wide, the import Tariff belongs to each Site.
//
// The grid import is priced by a two-part time-of-use Tariff, the same on every day type:
// energy by period, and demand on the peak period's maximum demand in the month, which is
// how Settlement charges it.
const touImportRates = {
  PEAK: { energyRatePerKWh: 1.2, demandRatePerKW: 38 },
  FLAT: { energyRatePerKWh: 0.72 },
  VALLEY: { energyRatePerKWh: 0.32 },
};
const touImportSlices = [
  ['VALLEY', 0, 360], ['FLAT', 360, 480], ['PEAK', 480, 660], ['FLAT', 660, 780], ['PEAK', 780, 900],
  ['FLAT', 900, 1080], ['PEAK', 1080, 1260], ['FLAT', 1260, 1320], ['VALLEY', 1320, 1440],
];
// The daily energy cost Metric prices each hour of the plant's hourly energy, which the savings
// seed registers first, at the import Tariff in effect then (TARIFF_ENERGY_COST).
const hourlyEnergyMetricId = localUUID(0x830000000001);

function buildOptimizationSeed(site, points) {
  const { tenantId, siteId } = site;
  const ids = buildIdentities(site, points);
  const energyPointId = ids.pointIdByRef.get('METER-HVAC-TOTAL/hvac_meter.energy');
  if (!energyPointId) throw new Error('METER-HVAC-TOTAL/hvac_meter.energy is required for the daily energy metric');
  let tenantSequence = 0x800000000001;
  const tenantID = () => localUUID(tenantSequence++);
  const [energyMetricId, energyVersionId, energyDependencyId, costMetricId, costVersionId, costDependencyId] =
    [tenantID(), tenantID(), tenantID(), tenantID(), tenantID(), tenantID()];
  const [policyId, policyVersionId, modelDefinitionId, deploymentRevisionId] = [tenantID(), tenantID(), tenantID(), tenantID()];
  let siteSequence = 0x810000000001;
  const siteID = () => siteUUID(site, siteSequence++);
  const [energyBindingId, costBindingId, energyScheduleId, costScheduleId, boundaryId, tariffId, tariffVersionId, assignmentId, deploymentBindingId] =
    [siteID(), siteID(), siteID(), siteID(), siteID(), siteID(), siteID(), siteID(), siteID()];
  const topologyVersionId = siteUUID(site, 0x600000000001);
  const gridNodeId = siteUUID(site, 0x600000000002);
  const electricity = '01990000-0000-7000-8000-000000000001';
  const tariffPeriods = ['WEEKDAY', 'WEEKEND'].flatMap((dayType) => touImportSlices.map(([code, start, end], ordinal) =>
    ({ id: siteID(), dayType, code, start, end, ordinal, pricingRule: touImportRates[code] })));
  const constraints = {
    comfort: { zoneTempMinC: 22, zoneTempMaxC: 26 },
    safety: { supplyTempMinC: 6, supplyTempMaxC: 10, maxSupplyTempStepC: 1 },
    // Telemetry history keys readings by point code: the BTU meter's supply, the zone sensor's temperature.
    inputMapping: { supplyTemperatureKey: 'supply_water_temperature', zoneTemperatureKey: 'temperature' },
    maintenanceConstraints: { outOfService: [] },
    manualLocks: { resources: [] },
    // The plant's chiller gains about 0.14 COP per °C of warmer supply water, near 60 kWh a
    // day on its typical load; the zone warms about 0.3 °C per °C once the coil runs short.
    responseModel: { dailyEnergyDeltaKWhPerSupplyTempC: -60, zoneTempDeltaCPerSupplyTempC: 0.3, energyUncertaintyP90KWh: 20, zoneTempUncertaintyP90C: 0.2 },
  };
  const t = sqlLiteral(tenantId);
  const st = sqlLiteral(siteId);
  const id = sqlLiteral;
  const nextLocalTime = (minute) => `(date_trunc('day', clock_timestamp() AT TIME ZONE 'Asia/Shanghai') + interval '1 day' + interval '${minute} minutes') AT TIME ZONE 'Asia/Shanghai'`;
  const draftVersion = (versionId) => `WHERE EXISTS (SELECT 1 FROM core_registry.metric_versions WHERE id = ${id(versionId)} AND status = 'DRAFT')`;
  return `BEGIN;
INSERT INTO core_registry.unit_registry (unit_code, display_name, quantity_kind, canonical_unit_code, multiplier, conversion_offset, status, revision, created_at, updated_at)
VALUES ('CNY', '人民币元', 'OTHER', 'CNY', 1, 0, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT DO NOTHING;
INSERT INTO core_registry.metrics (id, tenant_id, metric_code, metric_name, category, status, revision, created_at, updated_at) VALUES
  (${id(energyMetricId)}, ${t}, 'daily_energy', '冷站日用电量', 'ENERGY', 'ACTIVE', 1, clock_timestamp(), clock_timestamp()),
  (${id(costMetricId)}, ${t}, 'energy_cost', '冷站日电度电费', 'FINANCIAL', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.metric_versions (id, tenant_id, metric_id, version, unit_code, data_type, subject_type, time_granularity, aggregation, calculation_method, formula, quality_policy, effective_from, status, metadata, revision, created_at, updated_at) VALUES
  (${id(energyVersionId)}, ${t}, ${id(energyMetricId)}, 1, 'kWh', 'NUMBER', 'SITE', 'DAY', 'LAST', 'DELTA', NULL, 'STRICT', '2026-01-01T00:00:00Z', 'DRAFT', '{}', 1, clock_timestamp(), clock_timestamp()),
  (${id(costVersionId)}, ${t}, ${id(costMetricId)}, 1, 'CNY', 'NUMBER', 'SITE', 'DAY', 'SUM', 'TARIFF_ENERGY_COST', NULL, 'STRICT', '2026-01-01T00:00:00Z', 'DRAFT', '{}', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.metric_dependencies (id, tenant_id, metric_version_id, dependency_type, dependency_code, dependency_metric_id, sort_order, required, metadata, revision, created_at, updated_at)
SELECT ${id(energyDependencyId)}::uuid, ${t}::uuid, ${id(energyVersionId)}::uuid, 'POINT', 'site_energy', NULL::uuid, 0, true, '{}'::jsonb, 1::bigint, clock_timestamp(), clock_timestamp()
${draftVersion(energyVersionId)}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.metric_dependencies (id, tenant_id, metric_version_id, dependency_type, dependency_code, dependency_metric_id, sort_order, required, metadata, revision, created_at, updated_at)
SELECT ${id(costDependencyId)}::uuid, ${t}::uuid, ${id(costVersionId)}::uuid, 'METRIC', 'hourly_energy', ${id(hourlyEnergyMetricId)}::uuid, 0, true, '{}'::jsonb, 1::bigint, clock_timestamp(), clock_timestamp()
${draftVersion(costVersionId)}
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.metric_versions SET status = 'RELEASED', revision = revision + 1, updated_at = clock_timestamp()
WHERE id IN (${id(energyVersionId)}, ${id(costVersionId)}) AND status = 'DRAFT';
-- A binding is written once; after its Metric Version is superseded the overlap guard would
-- refuse it again before ON CONFLICT could skip it.
INSERT INTO core_registry.metric_bindings (id, tenant_id, site_id, metric_version_id, metric_id, metric_version, binding_version, subject_type, subject_id, time_granularity, source_definition, effective_from, status, revision, created_at, updated_at)
SELECT ${id(energyBindingId)}, ${t}, ${st}, ${id(energyVersionId)}, ${id(energyMetricId)}, 1, 1, 'SITE', ${st}, 'DAY', ${id(JSON.stringify({ points: { site_energy: energyPointId }, counter: { decreaseMode: 'RESET' } }))}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp()
WHERE NOT EXISTS (SELECT 1 FROM core_registry.metric_bindings WHERE id = ${id(energyBindingId)});
INSERT INTO core_registry.metric_bindings (id, tenant_id, site_id, metric_version_id, metric_id, metric_version, binding_version, subject_type, subject_id, time_granularity, source_definition, effective_from, status, revision, created_at, updated_at)
SELECT ${id(costBindingId)}, ${t}, ${st}, ${id(costVersionId)}, ${id(costMetricId)}, 1, 1, 'SITE', ${st}, 'DAY', '{}', '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp()
WHERE NOT EXISTS (SELECT 1 FROM core_registry.metric_bindings WHERE id = ${id(costBindingId)});
INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, next_fire_at) VALUES
  (${id(energyScheduleId)}, ${id(`daily-energy-${site.siteCode}`)}, '冷站日用电量（每日零点后）', ${t}, ${st}, 'METRIC_WINDOW_CALC', 'CRON', '10 0 * * *', '5-field-v1', 'Asia/Shanghai', 'FIRE_ONCE', 60, ${id(JSON.stringify({ bindingId: energyBindingId }))}, ${nextLocalTime(10)}),
  (${id(costScheduleId)}, ${id(`daily-energy-cost-${site.siteCode}`)}, '冷站日电度电费（每日零点后）', ${t}, ${st}, 'METRIC_WINDOW_CALC', 'CRON', '20 0 * * *', '5-field-v1', 'Asia/Shanghai', 'FIRE_ONCE', 60, ${id(JSON.stringify({ bindingId: costBindingId }))}, ${nextLocalTime(20)})
ON CONFLICT (tenant_id, schedule_code) DO NOTHING;

INSERT INTO core_registry.settlement_boundaries (id, tenant_id, site_id, topology_version_id, boundary_code, display_name, boundary_type, energy_type_id, direction, definition_mode, node_id, effective_from, status, revision, created_at, updated_at)
VALUES (${id(boundaryId)}, ${t}, ${st}, ${id(topologyVersionId)}, 'grid_import', '电网进线', 'GRID_CONNECTION', ${id(electricity)}, 'IMPORT', 'NODE', ${id(gridNodeId)}, '2026-01-01T00:00:00Z', 'DRAFT', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.settlement_boundaries SET status = 'ACTIVE', revision = revision + 1, updated_at = clock_timestamp()
WHERE id = ${id(boundaryId)} AND status = 'DRAFT';
INSERT INTO core_registry.tariffs (id, tenant_id, site_id, tariff_code, display_name, energy_type_id, status, revision, created_at, updated_at)
VALUES (${id(tariffId)}, ${t}, ${st}, 'grid_import_tou', '两部制分时电价', ${id(electricity)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.tariff_versions (id, tenant_id, site_id, tariff_id, version, effective_from, timezone, currency, billing_cycle, status, revision, created_at, updated_at)
VALUES (${id(tariffVersionId)}, ${t}, ${st}, ${id(tariffId)}, 1, '2026-01-01T00:00:00Z', 'Asia/Shanghai', 'CNY', 'CALENDAR_MONTH', 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.tariff_periods (id, tenant_id, site_id, tariff_version_id, period_code, day_type, local_start_minute, local_end_minute, pricing_rule, ordinal, revision, created_at, updated_at) VALUES
${tariffPeriods.map((period) => `  (${id(period.id)}, ${t}, ${st}, ${id(tariffVersionId)}, ${id(period.code)}, ${id(period.dayType)}, ${period.start}, ${period.end}, ${id(JSON.stringify(period.pricingRule))}, ${period.ordinal}, 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.tariff_assignments (id, tenant_id, site_id, boundary_id, tariff_id, effective_from, status, revision, created_at, updated_at)
VALUES (${id(assignmentId)}, ${t}, ${st}, ${id(boundaryId)}, ${id(tariffId)}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;

INSERT INTO core_registry.optimization_policies (id, tenant_id, policy_code, subject_type, resource_type, status, revision, created_at, updated_at)
VALUES (${id(policyId)}, ${t}, 'chilled_water_supply_cost', 'SITE', 'HVAC', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.optimization_policy_versions (id, tenant_id, policy_id, version, objective, weights, constraints, dispatch_mode, fallback_policy, risk_level, horizon, horizon_minutes, granularity, effective_from, status, revision, created_at, updated_at)
SELECT ${id(policyVersionId)}::uuid, ${t}::uuid, ${id(policyId)}::uuid, 1, 'COST', '{"cost":1}'::jsonb, ${id(JSON.stringify(constraints))}::jsonb, 'SHADOW', 'RULE_STRATEGY', 'LOW', 'DAY_AHEAD', 1440, '15MIN', '2026-01-01T00:00:00Z'::timestamptz, 'RELEASED', 1::bigint, clock_timestamp(), clock_timestamp()
-- The overlap guard fires before ON CONFLICT would, so a rerun skips the insert outright.
WHERE NOT EXISTS (SELECT 1 FROM core_registry.optimization_policy_versions WHERE id = ${id(policyVersionId)});
INSERT INTO core_registry.ai_model_definitions (id, tenant_id, name, provider, model_id, capabilities, status, revision, created_at, updated_at)
VALUES (${id(modelDefinitionId)}, ${t}, '冷站供水温度优化', 'LOCAL', 'hvac-recommendation-v1', ARRAY['optimization'], 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.ai_deployment_revisions (id, tenant_id, model_definition_id, use_case, revision, output_schema_version, enabled, created_at)
VALUES (${id(deploymentRevisionId)}, ${t}, ${id(modelDefinitionId)}, 'OPTIMIZATION', 1, 'optimization-recommendation-v1', true, clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.ai_deployment_bindings (id, tenant_id, site_id, use_case, deployment_revision_id, status, revision, created_at, updated_at)
VALUES (${id(deploymentBindingId)}, ${t}, ${st}, 'OPTIMIZATION', ${id(deploymentRevisionId)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
COMMIT;`;
}

// FDD checks the chilled-water loop every quarter-hour: the BTU meter's supply and return
// temperatures over the last 15 minutes against a 3.5 K floor (the loop is designed for 5 K).
function buildDiagnosisSeed(site, points) {
  const ids = buildIdentities(site, points);
  const meter = siteDevices(site).find((device) => device.name === 'BTU-METER-01');
  const assetId = ids.assetIdByKey.get('equipment-btu-meter');
  if (!meter || !assetId) throw new Error('BTU-METER-01 and its asset are required for the low-delta-T schedule');
  const plan = { assetId, deviceId: meter.platformDeviceId, ruleRevisionId: 'chilled-water-low-delta-t/v1', minimumDeltaTC: 3.5, windowMinutes: 15 };
  return `INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, next_fire_at)
VALUES (${sqlLiteral(siteUUID(site, 0x820000000001))}, ${sqlLiteral(`chilled-water-low-delta-t-${site.siteCode}`)}, '冷冻水低温差诊断（每 15 分钟）', ${sqlLiteral(site.tenantId)}, ${sqlLiteral(site.siteId)}, 'FDD_EVALUATION', 'CRON', '*/15 * * * *', '5-field-v1', 'UTC', 'FIRE_ONCE', 60, ${sqlLiteral(JSON.stringify(plan))},
  date_trunc('hour', clock_timestamp()) + ceil(extract(minute FROM clock_timestamp()) / 15 + 0.0001) * interval '15 minutes')
ON CONFLICT (tenant_id, schedule_code) DO NOTHING;`;
}

// A Site's monthly report is kept as a snapshot once the month is over (#483): report-service takes
// it at 03:00 on the 1st, Site time, for the month before.
function buildReportSeed(site) {
  return `INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, next_fire_at)
VALUES (${sqlLiteral(siteUUID(site, 0x850000000001))}, ${sqlLiteral(`monthly-report-${site.siteCode}`)}, '月度报告快照（每月 1 日 03:00）', ${sqlLiteral(site.tenantId)}, ${sqlLiteral(site.siteId)}, 'REPORT_GENERATE', 'CRON', '0 3 1 * *', '5-field-v1', 'Asia/Shanghai', 'FIRE_ONCE', 120, '{}',
  (date_trunc('month', clock_timestamp() AT TIME ZONE 'Asia/Shanghai') + interval '1 month' + interval '3 hours') AT TIME ZONE 'Asia/Shanghai')
ON CONFLICT (tenant_id, schedule_code) DO NOTHING;`;
}

// Savings verification (ADR 0021) fits electricity against cooling delivered, hour by hour: two
// hourly counter-delta Metrics per Site, and one per sub-metered device for measures bounded by
// its sub-meter (IPMVP Option B), up to fourteen days backfilled from telemetry history so a
// measure has its baseline period, and a daily MV_EVALUATION at 02:00 Site time.
function buildSavingsSeed(site, points) {
  const { tenantId, siteId } = site;
  const ids = buildIdentities(site, points);
  const energyPointId = ids.pointIdByRef.get('METER-HVAC-TOTAL/hvac_meter.energy');
  const coolingPointId = ids.pointIdByRef.get('BTU-METER-01/btu_meter.accumulated_cooling_energy');
  if (!energyPointId || !coolingPointId) throw new Error('the HVAC total meter energy and BTU cooling energy points are required for savings verification');
  let tenantSequence = 0x830000000001;
  const tenantID = () => localUUID(tenantSequence++);
  let siteSequence = 0x840000000001;
  const siteID = () => siteUUID(site, siteSequence++);
  const metrics = [
    { code: 'hourly_energy', name: '冷站逐时用电量', dependency: 'site_energy', pointId: energyPointId },
    { code: 'hourly_cooling', name: '冷站逐时供冷量', dependency: 'site_cooling', pointId: coolingPointId },
  ].map((metric) => ({ ...metric, metricId: tenantID(), versionId: tenantID(), dependencyId: tenantID(), bindingId: siteID(), scheduleId: siteID() }));
  if (metrics[0].metricId !== hourlyEnergyMetricId) throw new Error('the daily energy cost Metric depends on hourly_energy being the first savings Metric');
  const evaluationScheduleId = siteID();
  const deviceMetric = { code: 'device_hourly_energy', name: '设备逐时用电量', dependency: 'device_energy', metricId: tenantID(), versionId: tenantID(), dependencyId: tenantID() };
  const deviceByName = new Map(siteDevices(site).map((device) => [device.name, device]));
  const deviceBindings = points.filter((point) => point.pointType === 'COUNTER' && meteredEquipment.includes(point.deviceId)).map((point) => ({
    device: deviceByName.get(point.deviceId), pointId: ids.pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`), bindingId: siteID(), scheduleId: siteID(),
  }));
  if (deviceBindings.length !== meteredEquipment.length) throw new Error('every metered device needs its energy counter for savings verification');
  const hourlyBindings = [...metrics.map((metric) => metric.bindingId), ...deviceBindings.map((binding) => binding.bindingId)];
  const t = sqlLiteral(tenantId);
  const st = sqlLiteral(siteId);
  const id = sqlLiteral;
  return `BEGIN;
INSERT INTO core_registry.metrics (id, tenant_id, metric_code, metric_name, category, status, revision, created_at, updated_at) VALUES
${[...metrics, deviceMetric].map((metric) => `  (${id(metric.metricId)}, ${t}, ${id(metric.code)}, ${id(metric.name)}, 'ENERGY', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.metric_versions (id, tenant_id, metric_id, version, unit_code, data_type, subject_type, time_granularity, aggregation, calculation_method, formula, quality_policy, effective_from, status, metadata, revision, created_at, updated_at) VALUES
${[...metrics.map((metric) => ({ ...metric, subject: 'SITE' })), { ...deviceMetric, subject: 'DEVICE' }].map((metric) => `  (${id(metric.versionId)}, ${t}, ${id(metric.metricId)}, 1, 'kWh', 'NUMBER', ${id(metric.subject)}, 'HOUR', 'LAST', 'DELTA', NULL, 'STRICT', '2026-01-01T00:00:00Z', 'DRAFT', '{}', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
${[...metrics, deviceMetric].map((metric) => `INSERT INTO core_registry.metric_dependencies (id, tenant_id, metric_version_id, dependency_type, dependency_code, dependency_metric_id, sort_order, required, metadata, revision, created_at, updated_at)
SELECT ${id(metric.dependencyId)}::uuid, ${t}::uuid, ${id(metric.versionId)}::uuid, 'POINT', ${id(metric.dependency)}, NULL::uuid, 0, true, '{}'::jsonb, 1::bigint, clock_timestamp(), clock_timestamp()
WHERE EXISTS (SELECT 1 FROM core_registry.metric_versions WHERE id = ${id(metric.versionId)} AND status = 'DRAFT')
ON CONFLICT (id) DO NOTHING;`).join('\n')}
UPDATE core_registry.metric_versions SET status = 'RELEASED', revision = revision + 1, updated_at = clock_timestamp()
WHERE id IN (${[...metrics, deviceMetric].map((metric) => id(metric.versionId)).join(', ')}) AND status = 'DRAFT';
INSERT INTO core_registry.metric_bindings (id, tenant_id, site_id, metric_version_id, metric_id, metric_version, binding_version, subject_type, subject_id, time_granularity, source_definition, effective_from, status, revision, created_at, updated_at) VALUES
${metrics.map((metric) => `  (${id(metric.bindingId)}, ${t}, ${st}, ${id(metric.versionId)}, ${id(metric.metricId)}, 1, 1, 'SITE', ${st}, 'HOUR', ${id(JSON.stringify({ points: { [metric.dependency]: metric.pointId }, counter: { decreaseMode: 'RESET' } }))}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp())`).join(',\n')},
${deviceBindings.map((binding) => `  (${id(binding.bindingId)}, ${t}, ${st}, ${id(deviceMetric.versionId)}, ${id(deviceMetric.metricId)}, 1, 1, 'DEVICE', ${id(binding.device.platformDeviceId)}, 'HOUR', ${id(JSON.stringify({ points: { [deviceMetric.dependency]: binding.pointId }, counter: { decreaseMode: 'RESET' } }))}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, next_fire_at) VALUES
${metrics.map((metric) => `  (${id(metric.scheduleId)}, ${id(`${metric.code.replace('_', '-')}-${site.siteCode}`)}, ${id(`${metric.name}（每小时）`)}, ${t}, ${st}, 'METRIC_WINDOW_CALC', 'CRON', '5 * * * *', '5-field-v1', 'Asia/Shanghai', 'FIRE_ONCE', 60, ${id(JSON.stringify({ bindingId: metric.bindingId }))}, date_trunc('hour', clock_timestamp()) + interval '65 minutes')`).join(',\n')},
${deviceBindings.map((binding) => `  (${id(binding.scheduleId)}, ${id(`device-hourly-energy-${binding.device.slug}-${site.siteCode}`)}, ${id(`${binding.device.name} ${deviceMetric.name}（每小时）`)}, ${t}, ${st}, 'METRIC_WINDOW_CALC', 'CRON', '5 * * * *', '5-field-v1', 'Asia/Shanghai', 'FIRE_ONCE', 60, ${id(JSON.stringify({ bindingId: binding.bindingId }))}, date_trunc('hour', clock_timestamp()) + interval '65 minutes')`).join(',\n')},
  (${id(evaluationScheduleId)}, ${id(`savings-verification-${site.siteCode}`)}, '节能量核算（每日 02:00）', ${t}, ${st}, 'MV_EVALUATION', 'CRON', '0 2 * * *', '5-field-v1', 'Asia/Shanghai', 'FIRE_ONCE', 300, '{}',
    (date_trunc('day', clock_timestamp() AT TIME ZONE 'Asia/Shanghai') + interval '1 day 2 hours') AT TIME ZONE 'Asia/Shanghai')
ON CONFLICT (tenant_id, schedule_code) DO NOTHING;
WITH hours AS (
  -- Telemetry starts once the Site's devices are registered; an earlier hour has nothing to count.
  SELECT generate_series(
    GREATEST(date_trunc('hour', clock_timestamp()) - interval '14 days', (SELECT date_trunc('hour', min(created_at)) + interval '1 hour' FROM core_registry.devices WHERE site_id = ${st})),
    date_trunc('hour', clock_timestamp()) - interval '2 hours', interval '1 hour') AS period_start
), plan AS (
  SELECT binding.id AS binding_id, hours.period_start,
         'savings-backfill-' || binding.id || '-' || to_char(hours.period_start AT TIME ZONE 'UTC', 'YYYYMMDDHH24') AS dedup
  FROM hours CROSS JOIN (VALUES ${hourlyBindings.map((bindingId) => `(${id(bindingId)}::uuid)`).join(', ')}) AS binding(id)
)
INSERT INTO core_registry.job_instances (job_id, trigger_type, job_type, tenant_id, site_id, scheduled_for, priority, dedup_key, payload, state, max_attempts, timeout_seconds, created_at, updated_at)
SELECT ${sqlJobId}, 'BACKFILL', 'METRIC_BACKFILL', ${t}, ${st}, period_start + interval '1 hour', 20, dedup,
       jsonb_build_object('bindingId', binding_id, 'periodStart', period_start, 'periodEnd', period_start + interval '1 hour'),
       'READY', 3, 60, clock_timestamp(), clock_timestamp()
FROM plan
ON CONFLICT (dedup_key) DO NOTHING;
COMMIT;`;
}

function localAdminPrincipalId() {
  if (!existsSync(reconciliationPath)) throw new Error('identity-reconcile.json is required before simulator authorization bootstrap');
  const reconciliation = JSON.parse(readFileSync(reconciliationPath, 'utf8'));
  const principalId = reconciliation.seed?.principalId;
  if (!principalId) throw new Error('identity-reconcile.json is missing seed.principalId');
  return principalId;
}

function buildLocalGrants(points, controlPoints, principalId) {
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
  const commands = localSites.flatMap((site) => {
    const deviceByName = new Map(siteDevices(site).map((device) => [device.name, device]));
    return controlPoints.map((point, index) => `(
      ${sqlLiteral(siteUUID(site, 0x710000000000 + index + 1))}, ${sqlLiteral(principalId)},
      ${sqlLiteral(site.tenantId)}, ${sqlLiteral(site.siteId)}, ${sqlLiteral(deviceByName.get(point.deviceId).platformDeviceId)},
      ${sqlLiteral(point.sourceMetadata.capability)}, ${sqlLiteral(point.sourceMetadata.capabilityRevision)},
      'COMMAND_SUBMIT', 'MEDIUM', 'ALLOW', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
    )`);
  }).join(',\n');
  return `BEGIN;
INSERT INTO iam.command_permissions (id,principal_id,tenant_id,site_id,device_id,capability,capability_revision,purpose,maximum_risk,effect,status,valid_from,valid_to,revision,created_at,updated_at) VALUES
${commands}
ON CONFLICT (id) DO UPDATE SET principal_id=EXCLUDED.principal_id,tenant_id=EXCLUDED.tenant_id,
site_id=EXCLUDED.site_id,device_id=EXCLUDED.device_id,capability=EXCLUDED.capability,
capability_revision=EXCLUDED.capability_revision,purpose=EXCLUDED.purpose,maximum_risk=EXCLUDED.maximum_risk,
effect='ALLOW',status='ACTIVE',valid_to=NULL,revision=iam.command_permissions.revision+1,updated_at=clock_timestamp()
WHERE (iam.command_permissions.principal_id,iam.command_permissions.tenant_id,iam.command_permissions.site_id,
       iam.command_permissions.device_id,iam.command_permissions.capability,iam.command_permissions.capability_revision,
       iam.command_permissions.purpose,iam.command_permissions.maximum_risk,iam.command_permissions.effect,
       iam.command_permissions.status,iam.command_permissions.valid_to)
IS DISTINCT FROM (EXCLUDED.principal_id,EXCLUDED.tenant_id,EXCLUDED.site_id,EXCLUDED.device_id,EXCLUDED.capability,
                  EXCLUDED.capability_revision,EXCLUDED.purpose,EXCLUDED.maximum_risk,'ALLOW','ACTIVE',NULL::timestamptz);
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
    'up', '-d', '--no-deps', 'connectivity',
  ]);
  run(process.execPath, [
    path.join(repoRoot, 'scripts', 'phase1-wsl-compose.mjs'),
    '--simulator-acceptance',
    '--profile', 'simulator-acceptance',
    'up', '-d', '--build', '--no-deps', ...localSites.map((site) => site.simulatorService),
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

// Reapply only the local simulator's explicit permissions without reseeding
// Registry/Telemetry or restarting a running plant.
if (process.argv.includes('--authorization-only')) {
  psql('hvac_s1', buildLocalGrants(observedPoints, controlPoints, localAdminPrincipalId()));
  process.exit(0);
}

for (const site of localSites) {
  psql('hvac_s1', buildS1Seed(site, seededPoints));
  psql('hvac_s1', buildEnergyTopologySeed(site, seededPoints));
  psql('hvac_s1', buildForecastSeed(site, seededPoints));
  // The daily energy cost Metric depends on the savings seed's hourly_energy Metric.
  psql('hvac_s1', buildSavingsSeed(site, seededPoints));
  psql('hvac_s1', buildOptimizationSeed(site, seededPoints));
  psql('hvac_s1', buildDiagnosisSeed(site, seededPoints));
  psql('hvac_s1', buildReportSeed(site));
  psql('hvac_s2', buildS2Seed(site, observedPoints, rig));
}
for (const site of localSites) writeSimulatorConfig(site);
// Credential bootstrap uses the local administrator's explicit Gateway write scopes.
runLocalAdminGrant();
psql('hvac_s1', buildLocalGrants(observedPoints, controlPoints, localAdminPrincipalId()));
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

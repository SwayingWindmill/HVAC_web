import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { resolve } from 'node:path';
import {
  analyticsActions,
  centralPlantDevices,
  centralPlantIdentity,
  localUUID,
  telemetryActions,
} from './central-plant-local-contract.mjs';
import { buildCentralPlantRouteOwnership } from './central-plant-local-routing.mjs';
import { buildS1SeedSQL, buildS2SeedSQL } from './central-plant-local-seed.mjs';
import {
  buildCentralPlantSimulatorConfig,
  centralPlantAreas,
  centralPlantCalculatedPointCount,
  centralPlantDeviceEndpoints,
  centralPlantEquipment,
  centralPlantSensors,
} from './central-plant-spatial-model.mjs';

const root = resolve(process.cwd());
const adapterTemplate = JSON.parse(await readFile(resolve(root, 'services/thingsboard-telemetry-adapter/configs/central-plant.local.example.json'), 'utf8'));
const simulatorConfig = buildCentralPlantSimulatorConfig(adapterTemplate);
const thingsBoardCompose = await readFile(resolve(root, 'infra/central-plant-local/thingsboard.compose.yaml'), 'utf8');
const realtimeCompose = await readFile(resolve(root, 'infra/central-plant-local/realtime.compose.yaml'), 'utf8');
const s2Compose = await readFile(resolve(root, 'infra/s2-telemetry/compose.yaml'), 'utf8');
const topology = await readFile(resolve(root, 'scripts/central-plant-local-topology.mjs'), 'utf8');
const smoke = await readFile(resolve(root, 'scripts/central-plant-local.mjs'), 'utf8');
const routeOwnershipSource = JSON.parse(await readFile(resolve(root, 'contracts/ownership/route-ownership.v1.json'), 'utf8'));

function pointMaps() {
  const pointsByDevice = new Map();
  const pointKeysByDevice = new Map();
  centralPlantDevices.forEach((device, index) => {
    const points = adapterTemplate.devices[index]?.points ?? [];
    pointsByDevice.set(device.platformDeviceId, points);
    pointKeysByDevice.set(device.platformDeviceId, points.map((point) => point.telemetryKey));
  });
  return { pointsByDevice, pointKeysByDevice };
}

test('central plant contract defines unique spatial, Equipment, Device, Sensor and Point identities', () => {
  assert.equal(centralPlantDevices.length, 7);
  assert.equal(new Set(centralPlantDevices.map((device) => device.platformDeviceId)).size, 7);
  assert.equal(new Set(centralPlantDevices.map((device) => device.name)).size, 7);
  assert.deepEqual(centralPlantDevices.map((device) => device.type), [
    'CHILLER',
    'CHILLED_WATER_PUMP',
    'COOLING_WATER_PUMP',
    'COOLING_TOWER',
    'HVAC_POWER_METER',
    'BTU_METER',
    'WEATHER_STATION',
  ]);
  assert.equal(centralPlantAreas.length, 4);
  assert.equal(centralPlantEquipment.length, 7);
  assert.equal(centralPlantDeviceEndpoints.length, 7);
  assert.equal(centralPlantSensors.length, 15);
  assert.equal(simulatorConfig.points.length, 47);
  assert.equal(centralPlantCalculatedPointCount, 7);
  assert.equal(simulatorConfig.schemaVersion, 2);
  assert.equal(simulatorConfig.sensors.filter((sensor) => sensor.mode === 'INDEPENDENT_DEVICE').length, 3);
  assert.ok(simulatorConfig.points.some((point) => point.kind === 'CALCULATED' && point.inputPointRefs.length > 0));
  assert.match(centralPlantIdentity.organizationId, /^[0-9a-f-]{36}$/);
  assert.match(localUUID(1), /^01910000-0000-7000-8000-[0-9a-f]{12}$/);
});

test('database seeds cover the complete Registry graph and every adapter point', () => {
  const { pointsByDevice, pointKeysByDevice } = pointMaps();
  assert.equal([...pointsByDevice.values()].reduce((total, points) => total + points.length, 0), 47);
  const s1 = buildS1SeedSQL({
    oidcIssuer: 'https://127.0.0.1:18443',
    pointKeysByDevice,
    spatialPoints: simulatorConfig.points,
  });
  const s2 = buildS2SeedSQL({ pointsByDevice });
  for (const marker of [
    'core_registry.areas',
    'core_registry.equipment_area_bindings',
    'core_registry.device_area_bindings',
    'core_registry.device_bindings',
    'core_registry.sensors',
    'core_registry.sensor_device_bindings',
    'core_registry.sensor_area_bindings',
    'core_registry.sensor_subject_bindings',
    'core_registry.telemetry_points',
    'core_registry.point_subject_bindings',
    'core_registry.calculated_point_inputs',
  ]) assert.ok(s1.includes(marker), `S1 seed is missing ${marker}`);
  for (const area of centralPlantAreas) assert.ok(s1.includes(area.name), `${area.name} is missing from the S1 Area seed`);
  for (const sensor of centralPlantSensors) assert.ok(s1.includes(sensor.id), `${sensor.id} is missing from the S1 Sensor seed`);
  for (const device of centralPlantDevices) {
    assert.ok(s1.includes(device.platformDeviceId), `${device.name} is missing from the S1 seed`);
    assert.ok(s2.includes(device.platformDeviceId), `${device.name} is missing from the S2 seed`);
    for (const point of pointsByDevice.get(device.platformDeviceId)) {
      assert.ok(s1.includes(point.telemetryKey), `${point.telemetryKey} is missing from IAM exact-key bindings`);
      assert.ok(s2.includes(point.telemetryKey), `${point.telemetryKey} is missing from S2 freshness policies`);
    }
  }
  for (const action of telemetryActions) assert.ok(s1.includes(action));
  for (const action of analyticsActions) {
    assert.equal(s1.match(new RegExp(action.replaceAll('.', '\\.')), 'g')?.length, 1);
  }
  assert.ok(!s1.includes("'analytics-reader'"));
  assert.ok(!s1.includes("ARRAY['registry.read'] ||"));
  assert.ok(s2.includes('DELETE FROM telemetry_runtime.telemetry_publication_outbox;'));
  assert.ok(!s1.includes('ACCESS_TOKEN'));
  assert.ok(!s2.includes('ACCESS_TOKEN'));
});

test('local route ownership enables all S2 Telemetry routes without changing production policy', () => {
  const local = buildCentralPlantRouteOwnership(routeOwnershipSource);
  const productionTelemetryRoutes = routeOwnershipSource.routes.filter((route) => route.owner === 'telemetry-runtime-service');
  const localTelemetryRoutes = local.routes.filter((route) => route.owner === 'telemetry-runtime-service');
  assert.equal(productionTelemetryRoutes.length, 4);
  assert.equal(localTelemetryRoutes.length, 4);
  assert.ok(productionTelemetryRoutes.every((route) => route.rollout.mode === 'disabled'));
  assert.ok(localTelemetryRoutes.every((route) => route.rollout.mode === 'all'));
  assert.ok(localTelemetryRoutes.every((route) => route.activationStatus === 'primary'));
  assert.ok(localTelemetryRoutes.every((route) => route.migrationPhase === 'R7-primary-100'));
  assert.deepEqual(
    local.routes.filter((route) => route.owner !== 'telemetry-runtime-service'),
    routeOwnershipSource.routes.filter((route) => route.owner !== 'telemetry-runtime-service'),
  );
});

test('central plant smoke verifies the atomic Asset Model and exact Real UI counts', () => {
  for (const marker of [
    '/api/v1/sites/${centralPlantIdentity.siteId}/asset-model',
    'expectedAssetCounts',
    'root?.dataset.areaCount',
    'root?.dataset.deviceEndpointCount',
    'root?.dataset.telemetryPointCount',
    'authority.assetModel.counts',
    'durableSmokeReportPath',
    '{up|smoke}',
  ]) assert.ok(smoke.includes(marker), `central plant smoke is missing ${marker}`);
  assert.ok(!smoke.includes('/devices?limit=100'));
  assert.ok(!smoke.includes('devices.length !== 6'));
});

test('local topology stays isolated and derives simulator credentials from the v2 Device graph', () => {
  for (const marker of [
    '127.0.0.1:${CENTRAL_PLANT_THINGSBOARD_PORT}:8080',
    'thingsboard/tb-node:4.3.1.3',
  ]) assert.ok(thingsBoardCompose.includes(marker));
  for (const marker of [
    '127.0.0.1:${CENTRAL_PLANT_CENTRIFUGO_PORT}:8000',
    'host.docker.internal:host-gateway',
    'redis:7.4.2-alpine',
  ]) assert.ok(realtimeCompose.includes(marker));
  for (const marker of [
    'clickhouse/clickhouse-server:26.3.12.3@sha256:1f7cd090d5c4e2b8bfe0ea5d8ae6125937e1d932c6371b4d25fbd6088829dc9c',
    '127.0.0.1:${S2_CLICKHOUSE_HTTP_HOST_PORT:-58123}:8123',
    './clickhouse/init:/docker-entrypoint-initdb.d:ro',
  ]) assert.ok(s2Compose.includes(marker));
  for (const marker of [
    "'spiffe://hvac.local/thingsboard-telemetry-adapter'",
    "'spiffe://hvac.local/centrifugo'",
    'await stop();',
    "['down', '--volumes', '--remove-orphans']",
    'ThingsBoard Telemetry Adapter',
    'Telemetry History Projector',
    'Telemetry Query Service',
    'QUERY_CUBE_ENDPOINT',
    'TELEMETRY_QUERY_URL',
    'TELEMETRY_CLICKHOUSE_HTTP_URL',
    'HVAC Web Real',
    'buildCentralPlantSimulatorConfig',
    'simulatorConfig.credentialEnvByDeviceId',
  ]) assert.ok(topology.includes(marker), `topology is missing ${marker}`);
  assert.ok(topology.includes('buildGoBinaries(paths, goCache, quiet);'));
  assert.ok(topology.includes("IDENTITY_POLICY_REVISION: 'registry-read:1'"));
  assert.ok(!topology.includes("spawnService('OIDC fixture', goBinary"));
  assert.ok(!topology.includes('rejectUnauthorized: false, cert'));
});

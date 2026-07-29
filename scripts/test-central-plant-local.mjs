import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { resolve } from 'node:path';
import {
  centralPlantDevices,
  centralPlantIdentity,
  localUUID,
  telemetryActions,
} from './central-plant-local-contract.mjs';
import { buildCentralPlantRouteOwnership } from './central-plant-local-routing.mjs';
import { buildS1SeedSQL, buildS2SeedSQL } from './central-plant-local-seed.mjs';

const root = resolve(process.cwd());
const adapterTemplate = JSON.parse(await readFile(resolve(root, 'services/thingsboard-telemetry-adapter/configs/central-plant.local.example.json'), 'utf8'));
const thingsBoardCompose = await readFile(resolve(root, 'infra/central-plant-local/thingsboard.compose.yaml'), 'utf8');
const realtimeCompose = await readFile(resolve(root, 'infra/central-plant-local/realtime.compose.yaml'), 'utf8');
const topology = await readFile(resolve(root, 'scripts/central-plant-local-topology.mjs'), 'utf8');
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

test('central plant contract defines six unique S1/S2 device identities', () => {
  assert.equal(centralPlantDevices.length, 6);
  assert.equal(new Set(centralPlantDevices.map((device) => device.platformDeviceId)).size, 6);
  assert.equal(new Set(centralPlantDevices.map((device) => device.name)).size, 6);
  assert.deepEqual(centralPlantDevices.map((device) => device.type), [
    'CHILLER',
    'CHILLED_WATER_PUMP',
    'COOLING_WATER_PUMP',
    'COOLING_TOWER',
    'HVAC_POWER_METER',
    'BTU_METER',
  ]);
  assert.match(centralPlantIdentity.organizationId, /^[0-9a-f-]{36}$/);
  assert.match(localUUID(1), /^01910000-0000-7000-8000-[0-9a-f]{12}$/);
});

test('database seeds cover every adapter point with exact-key authorization and freshness policy', () => {
  const { pointsByDevice, pointKeysByDevice } = pointMaps();
  assert.equal([...pointsByDevice.values()].reduce((total, points) => total + points.length, 0), 44);
  const s1 = buildS1SeedSQL({ oidcIssuer: 'https://127.0.0.1:18443', pointKeysByDevice });
  const s2 = buildS2SeedSQL({ pointsByDevice });
  for (const device of centralPlantDevices) {
    assert.ok(s1.includes(device.platformDeviceId), `${device.name} is missing from the S1 seed`);
    assert.ok(s2.includes(device.platformDeviceId), `${device.name} is missing from the S2 seed`);
    for (const point of pointsByDevice.get(device.platformDeviceId)) {
      assert.ok(s1.includes(point.telemetryKey), `${point.telemetryKey} is missing from IAM exact-key bindings`);
      assert.ok(s2.includes(point.telemetryKey), `${point.telemetryKey} is missing from S2 freshness policies`);
    }
  }
  for (const action of telemetryActions) assert.ok(s1.includes(action));
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

test('local topology stays isolated and fails closed around realtime and workload identity', () => {
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
    "'spiffe://hvac.local/thingsboard-telemetry-adapter'",
    "'spiffe://hvac.local/centrifugo'",
    'await stop();',
    "['down', '--volumes', '--remove-orphans']",
    'ThingsBoard Telemetry Adapter',
    'HVAC Web Real',
  ]) assert.ok(topology.includes(marker), `topology is missing ${marker}`);
  assert.ok(topology.includes('buildGoBinaries(paths, goCache, quiet);'));
  assert.ok(topology.includes("IDENTITY_POLICY_REVISION: 'registry-read:1'"));
  assert.ok(!topology.includes("spawnService('OIDC fixture', goBinary"));
  assert.ok(!topology.includes('rejectUnauthorized: false, cert'));
});

import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { resolve } from 'node:path';
import { centralPlantDevices } from './central-plant-local-contract.mjs';
import { buildS1SeedSQL, buildS2SeedSQL } from './central-plant-local-seed.mjs';
import { buildHistoricalEnergyBootstrap } from './central-plant-local-topology.mjs';
import {
  buildCentralPlantControlPoints,
  buildCentralPlantSimulatorConfig,
  buildCentralPlantSimulatorPoints,
} from './central-plant-spatial-model.mjs';

const root = resolve(process.cwd());
const pointContract = JSON.parse(await readFile(resolve(root, 'contracts/registry/central-plant-device-points.v2.json'), 'utf8'));
const simulatorConfig = buildCentralPlantSimulatorConfig(pointContract);
const mqttCompose = await readFile(resolve(root, 'infra/s2-telemetry/mqtt/compose.yaml'), 'utf8');
const topology = await readFile(resolve(root, 'scripts/central-plant-local-topology.mjs'), 'utf8');

function pointMaps() {
  const pointsByDevice = new Map();
  const pointKeysByDevice = new Map();
  centralPlantDevices.forEach((device) => {
    const points = pointContract.devices.find((entry) => entry.deviceId === device.name)?.points ?? [];
    pointsByDevice.set(device.platformDeviceId, points);
    pointKeysByDevice.set(device.platformDeviceId, points.map((point) => point.telemetryKey));
  });
  return { pointsByDevice, pointKeysByDevice };
}

test('central plant v2 point contract generates the MQTT simulator graph', () => {
  assert.equal(centralPlantDevices.length, 7);
  assert.equal(simulatorConfig.schemaVersion, 2);
  assert.equal(simulatorConfig.gatewayId, 'EG8200-COMMERCIAL-001');
  assert.equal(simulatorConfig.points.length, 48);
  assert.equal(buildCentralPlantSimulatorPoints(pointContract).length, 48);
  assert.ok(simulatorConfig.points.every((point) => /^[a-z][a-z0-9_]*$/.test(point.pointCode)));
});

test('central plant historical Energy fixture is complete without provider replay', () => {
  const history = buildHistoricalEnergyBootstrap(new Date('2026-08-06T12:00:00.000Z'), simulatorConfig.points);
  assert.equal(history.readings.length, 2411);
  assert.equal(history.analyticsFacts.length, history.readings.length - 1);
  assert.equal(history.analyticsIntervalCount, history.readings.length - 1);
  assert.ok(history.analyticsFacts.every((fact) => fact.quality === 'VALID'));
  assert.equal(history.finalEnergyKwh, history.readings.at(-1).energyKwh);
});

test('central plant seeds cover every canonical telemetry Point', () => {
  const { pointsByDevice, pointKeysByDevice } = pointMaps();
  const s1 = buildS1SeedSQL({
    oidcIssuer: 'https://127.0.0.1:18443/oidc',
    principalSubject: 'logto-central-plant-user',
    pointKeysByDevice,
    spatialPoints: [...simulatorConfig.points, ...buildCentralPlantControlPoints()],
  });
  const s2 = buildS2SeedSQL({ pointsByDevice, spatialPoints: simulatorConfig.points });
  for (const device of centralPlantDevices) {
    assert.ok(s1.includes(device.platformDeviceId));
    assert.ok(s2.includes(device.platformDeviceId));
    for (const point of pointsByDevice.get(device.platformDeviceId)) {
      assert.ok(s1.includes(point.telemetryKey));
      assert.ok(s2.includes(point.telemetryKey));
    }
  }
  assert.ok(!s1.includes('ACCESS_TOKEN'));
  assert.ok(!s2.includes('ACCESS_TOKEN'));
});

test('central plant local runtime uses MQTT only', () => {
  for (const marker of [
    'eclipse-mosquitto:2.1.2-alpine',
    '${MQTT_PKI_DIR:?MQTT_PKI_DIR is required}',
  ]) assert.ok(mqttCompose.includes(marker));
  for (const marker of [
    './services/mqtt-telemetry-adapter/cmd/mqtt-telemetry-adapter',
    './tools/eg8200-simulator/cmd/eg8200-mqtt-publisher',
    "'spiffe://hvac.local/mqtt-telemetry-adapter'",
    "topicFilter: 'energy/v1/+/+/+/telemetry'",
    'mqttGatewayConfig',
    'EG8200 MQTT Publisher',
    'MQTT Telemetry Adapter',
  ]) assert.ok(topology.includes(marker), `topology is missing ${marker}`);
  assert.ok(!topology.toLowerCase().includes('thingsboard'));
});

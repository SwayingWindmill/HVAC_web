import assert from 'node:assert/strict';
import test from 'node:test';
import { GatewayError } from '../miniprogram/lib/gateway.ts';
import { buildPlant, deviceSnapshotKeyBatches, fetchPlantDevice, snapshotBatches, type Snapshot } from '../miniprogram/lib/plant.ts';
import { assetModel, recordedAt, snapshots } from './recorded.ts';


test('current values are asked for every device and its observed points, within the gateway batch limits', () => {
  const [only, ...rest] = snapshotBatches(assetModel);
  assert.equal(rest.length, 0);
  assert.equal(only.length, assetModel.devices.length);
  const chiller = only.find((request) => request.deviceId === '018f3e00-4000-7000-8000-000000000001')!;
  // Commands are written, never observed. Keys go sorted, as the web asks for them.
  assert.deepEqual(chiller.keys, [
    'chilled_water_temperature_setpoint', 'compressor_load', 'cooling_capacity', 'cop', 'entering_chilled_water_temperature',
    'entering_cooling_water_temperature', 'fault_code', 'leaving_chilled_water_temperature', 'load_limit', 'power', 'run_state',
  ]);

  // At most 100 devices and 2,048 keys go in one request.
  const devices = Array.from({ length: 150 }, (_, index) => ({ id: `device-${index}`, displayName: `D${index}`, deviceType: 'POWER_METER' }));
  const telemetryPoints = devices.flatMap((device) => Array.from({ length: 10 }, (_, index) => ({
    reportingDeviceId: device.id, pointCode: `p${index}`, sourceKey: `hvac_meter.p${index}`, displayName: `p${index}`,
    pointType: 'TELEMETRY', valueType: 'NUMBER', status: 'ACTIVE',
  })));
  assert.deepEqual(snapshotBatches({ assets: [], devices, telemetryPoints, relationships: [] }).map((batch) => batch.length), [100, 50]);
  const wide = telemetryPoints.flatMap((point) => Array.from({ length: 30 }, (_, index) => ({ ...point, pointCode: `${point.pointCode}-${index}` })));
  assert.deepEqual(snapshotBatches({ assets: [], devices, telemetryPoints: wide, relationships: [] }).map((batch) => batch.length), [6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6]);
});

test('fetchPlantDevice calls only the single-device observation-snapshot endpoint and caches asset-model across refreshes', async () => {
  const chillerId = '018f3e00-4000-7000-8000-000000000001';
  const chillerSnapshot = (snapshots.items as { deviceId: string; snapshot?: Snapshot }[]).find((item) => item.deviceId === chillerId)!.snapshot!;
  const siteId = 'site-1';
  const calls: string[] = [];

  const client = {
    async get<T>(path: string): Promise<T> {
      calls.push(path);
      if (path === `/api/v1/sites/${siteId}/asset-model`) return assetModel as T;
      if (path.startsWith(`/api/v1/devices/${chillerId}/observation-snapshot`)) return chillerSnapshot as T;
      throw new Error(`unexpected GET ${path}`);
    },
  };

  const first = await fetchPlantDevice(client, siteId, chillerId, false, recordedAt);
  const second = await fetchPlantDevice(client, siteId, chillerId, false, recordedAt);
  const refreshed = await fetchPlantDevice(client, siteId, chillerId, true, recordedAt);

  assert.equal(calls.filter((path) => path.endsWith('/asset-model')).length, 2);
  const snapshotCalls = calls.filter((path) => path.includes('/observation-snapshot'));
  assert.equal(snapshotCalls.length, 3);
  assert.ok(snapshotCalls.every((path) => path.startsWith(`/api/v1/devices/${chillerId}/observation-snapshot?keys=`)));

  const expected = buildPlant(assetModel, snapshots, recordedAt).devices.find((device) => device.deviceId === chillerId)!;
  for (const device of [first, second, refreshed]) {
    assert.ok(device !== null);
    assert.equal(device.name, expected.name);
    assert.equal(device.connection, expected.connection);
    assert.equal(device.runState, expected.runState);
    assert.deepEqual(device.readings, expected.readings);
  }
});

test('fetchPlantDevice degrades 404 snapshots, chunks >64 keys, omits keys when empty, and skips unknown devices', async () => {
  const chillerId = '018f3e00-4000-7000-8000-000000000001';
  const siteId = 'site-1';
  const missingSnapshot = await fetchPlantDevice({
    async get<T>(path: string): Promise<T> {
      if (path.endsWith('/asset-model')) return assetModel as T;
      throw new GatewayError('RESOURCE_NOT_FOUND', 404);
    },
  }, siteId, chillerId, true, recordedAt);
  assert.ok(missingSnapshot !== null);
  assert.equal(missingSnapshot.connection, 'UNKNOWN');
  assert.ok(missingSnapshot.readings.every((reading) => reading.display === '—' && reading.sampledAt === null));

  const manyPointsModel = {
    assets: [],
    devices: [
      { id: 'dev-many', displayName: 'Many', deviceType: 'CHILLER' },
      { id: 'dev-empty', displayName: 'Empty', deviceType: 'POWER_METER' },
    ],
    telemetryPoints: Array.from({ length: 130 }, (_, index) => ({
      reportingDeviceId: 'dev-many',
      pointCode: `p_${String(index).padStart(3, '0')}`,
      sourceKey: `chiller.p_${index}`,
      displayName: `P${index}`,
      pointType: index === 0 ? 'COMMAND' : 'TELEMETRY',
      valueType: 'NUMBER',
      status: 'ACTIVE',
    })),
    relationships: [],
  };
  assert.deepEqual(deviceSnapshotKeyBatches(manyPointsModel, 'dev-many').map((batch) => batch.length), [64, 64, 1]);
  assert.deepEqual(deviceSnapshotKeyBatches(manyPointsModel, 'dev-empty'), [[]]);

  const multiCalls: string[] = [];
  const multiDevice = await fetchPlantDevice({
    async get<T>(path: string): Promise<T> {
      multiCalls.push(path);
      if (path.endsWith('/asset-model')) return manyPointsModel as T;
      return { evaluationAvailability: 'AVAILABLE', presence: { applicability: 'APPLICABLE', currentState: 'ONLINE' }, values: [] } as T;
    },
  }, siteId, 'dev-many', true, recordedAt);
  assert.ok(multiDevice !== null);
  assert.equal(multiCalls.filter((path) => path.includes('/observation-snapshot')).length, 3);

  const emptyDeviceCalls: string[] = [];
  await fetchPlantDevice({
    async get<T>(path: string): Promise<T> {
      emptyDeviceCalls.push(path);
      if (path.endsWith('/asset-model')) return manyPointsModel as T;
      return { evaluationAvailability: 'AVAILABLE', presence: { applicability: 'APPLICABLE', currentState: 'ONLINE' }, values: [] } as T;
    },
  }, siteId, 'dev-empty', true, recordedAt);
  assert.deepEqual(emptyDeviceCalls, [`/api/v1/sites/${siteId}/asset-model`, '/api/v1/devices/dev-empty/observation-snapshot']);

  let snapshotTouched = false;
  const unknown = await fetchPlantDevice({
    async get<T>(path: string): Promise<T> {
      if (path.endsWith('/asset-model')) return manyPointsModel as T;
      snapshotTouched = true;
      throw new Error('should not be called');
    },
  }, siteId, 'missing-device', true, recordedAt);
  assert.equal(unknown, null);
  assert.equal(snapshotTouched, false);
});

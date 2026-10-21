import assert from 'node:assert/strict';
import test from 'node:test';
import { snapshotBatches } from '../miniprogram/lib/plant.ts';
import { assetModel } from './recorded.ts';


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

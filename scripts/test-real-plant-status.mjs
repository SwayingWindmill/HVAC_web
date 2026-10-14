import assert from 'node:assert/strict';
import test from 'node:test';
import { buildPlantView, equipmentStatus, isRunning, soleReading, tileReadings } from '../apps/hvac-web/src/features/operations/realtime/plant-model.ts';

const device = (category, overrides = {}) => ({
  deviceId: `${category}-${Math.random()}`,
  name: category,
  category,
  connection: 'ONLINE',
  runState: 'RUNNING',
  latestSampleAt: '2026-10-10T06:00:00Z',
  hasStaleData: false,
  reading: (key) => ({ label: key, numeric: 7, state: 'PRESENT', current: true }),
  ...overrides,
});
const plant = (devices) => ({ devices, groups: [], onlineCount: 0, latestSampleAt: null, totalPower: null, coolingCapacity: null, plantCop: null });

test('an offline device is offline whatever run state it last reported', () => {
  assert.equal(isRunning(device('CHILLER')), true);
  assert.equal(isRunning(device('CHILLER', { connection: 'OFFLINE' })), false);
  assert.equal(isRunning(device('CHILLER', { runState: 'STOPPED' })), false);
});

test('equipment counts cover chillers, pumps and towers; freshness covers every device', () => {
  const status = equipmentStatus(plant([
    device('CHILLER'),
    device('CHILLED_WATER_PUMP', { connection: 'OFFLINE', runState: 'FAULT' }),
    device('COOLING_TOWER', { runState: 'FAULT' }),
    device('METER', { runState: 'UNKNOWN', latestSampleAt: null }),
    device('WEATHER', { runState: 'UNKNOWN', hasStaleData: true }),
  ]));
  assert.deepEqual(status, { total: 3, running: 1, faults: 1, offline: 1, withoutData: 1, stale: 1 });
});

test('a device probe stands for its pipe only when the category has one device', () => {
  assert.equal(soleReading(plant([device('CHILLER')]), 'CHILLER', 'chiller.leaving_chilled_water_temperature')?.numeric, 7);
  assert.equal(soleReading(plant([device('CHILLER'), device('CHILLER')]), 'CHILLER', 'chiller.leaving_chilled_water_temperature'), null);
  assert.equal(soleReading(plant([]), 'CHILLER', 'chiller.leaving_chilled_water_temperature'), null);
});

test('a Gateway-wired device without an Asset or source-key namespace is grouped by its device type', () => {
  const row = (deviceType, displayName) => ({
    device: { id: displayName, deviceType, displayName },
    binding: { state: 'unbound' },
    telemetryPoints: [{ sourceKey: 't1', pointCode: 't1', status: 'ACTIVE' }],
    operational: { connection: { state: 'ONLINE' }, points: [] },
  });
  const view = buildPlantView([row('FLOW_METER', '流量计'), row('TEMPERATURE_MODULE', '温度采集模块'), row('SOMETHING_NEW', '未知设备')]);
  assert.deepEqual(view.groups.map((group) => [group.label, group.devices.map((device) => device.name)]), [
    ['计量', ['流量计']], ['采集与控制', ['温度采集模块']], ['其他设备', ['未知设备']],
  ]);
});

test("a device without its role's key readings shows its own measurements, current ones first", () => {
  const readings = { power_total: { label: '有功功率', current: false }, t1: { label: '温度 1', current: true }, t2: { label: '温度 2', current: true } };
  const meter = {
    row: { telemetryPoints: ['power_total', 't1', 't2', 'energy'].map((sourceKey) => ({ sourceKey, status: 'ACTIVE', pointType: sourceKey === 'energy' ? 'COUNTER' : 'TELEMETRY', valueType: 'NUMBER' })) },
    reading: (key) => readings[key] ?? null,
  };
  const own = tileReadings(meter, ['chiller.power'], []);
  assert.equal(own.lead.sourceKey, 't1');
  assert.deepEqual(own.facts.map((reading) => reading.label), ['温度 2', '有功功率']);
  const keyed = tileReadings({ ...meter, reading: (key) => (key === 'chiller.power' ? { label: '功率', current: true } : readings[key] ?? null) }, ['chiller.power'], ['t2']);
  assert.equal(keyed.lead.sourceKey, 'chiller.power');
  assert.deepEqual(keyed.facts.map((reading) => reading.label), ['温度 2']);
});

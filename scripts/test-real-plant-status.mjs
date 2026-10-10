import assert from 'node:assert/strict';
import test from 'node:test';
import { equipmentStatus, isRunning, soleReading } from '../apps/hvac-web/src/features/operations/realtime/plant-model.ts';

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

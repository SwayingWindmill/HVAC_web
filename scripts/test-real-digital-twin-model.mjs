import assert from 'node:assert/strict';
import test from 'node:test';
import { buildTwinPlant } from '../apps/hvac-web/src/features/operations/digital-twin/twin-model.ts';

const asset = (id, code, assetType, status = 'ACTIVE') => ({ id, code, displayName: code, assetType, status });
const device = (category, { assetId, connection = 'ONLINE', runState = 'RUNNING', readings = {} } = {}) => ({
  deviceId: `${category}-${assetId ?? 'unbound'}`,
  category,
  connection,
  runState,
  row: { binding: assetId ? { state: 'bound', asset: { id: assetId } } : { state: 'unbound' } },
  reading: (key) => (key in readings ? { label: key, display: String(readings[key]), unit: 'kW', state: 'PRESENT', numeric: readings[key], current: true } : null),
});
const plant = (devices) => ({ devices, groups: [], onlineCount: 0, latestSampleAt: null, totalPower: null, coolingCapacity: null, plantCop: null });

test('every chiller, tower and pump asset is drawn in code order, live when a device reports for it', () => {
  const twin = buildTwinPlant([
    asset('b', 'chiller-b', 'CHILLER'),
    asset('a', 'chiller-a', 'CHILLER'),
    asset('t', 'cooling-tower-1', 'COOLING_TOWER'),
    asset('p', 'chwp-1', 'CHILLED_WATER_PUMP'),
    asset('c', 'cwp-1', 'COOLING_WATER_PUMP'),
    asset('r', 'chiller-retired', 'CHILLER', 'RETIRED'),
    asset('m', 'meter', 'HVAC_POWER_METER'),
  ], plant([
    device('CHILLER', { assetId: 'a', readings: { 'chiller.power': 120 } }),
    device('CHILLER', { assetId: 'b', connection: 'OFFLINE' }),
  ]));
  assert.deepEqual(twin.chillers.map((item) => [item.name, item.state, item.readings.map((reading) => reading.display)]), [
    ['chiller-a', 'running', ['120']],
    ['chiller-b', 'offline', []],
  ]);
  assert.deepEqual([twin.towers, twin.chilledPumps, twin.coolingPumps].map((items) => items.map((item) => [item.name, item.state])), [
    [['cooling-tower-1', 'unconnected']], [['chwp-1', 'unconnected']], [['cwp-1', 'unconnected']],
  ]);
});

test('chilled water moves with a running chilled-water pump; cooling water with a cooling pump or any running machine', () => {
  const assets = [asset('a', 'chiller-a', 'CHILLER')];
  const still = buildTwinPlant(assets, plant([device('CHILLER', { assetId: 'a', runState: 'STOPPED' }), device('CHILLED_WATER_PUMP', { runState: 'STOPPED' })]));
  assert.deepEqual([still.chilledFlow, still.coolingFlow], [false, false]);
  const running = buildTwinPlant(assets, plant([device('CHILLER', { assetId: 'a' }), device('CHILLED_WATER_PUMP')]));
  assert.deepEqual([running.chilledFlow, running.coolingFlow], [true, true]);
  const offlinePump = buildTwinPlant(assets, plant([device('CHILLED_WATER_PUMP', { connection: 'OFFLINE' })]));
  assert.equal(offlinePump.chilledFlow, false);
});

import assert from 'node:assert/strict';
import test from 'node:test';
import { energyBreakdown } from '../apps/hvac-web/src/features/breakdown/breakdown-model.ts';

const hour = (h) => new Date(Date.UTC(2026, 9, 12, h)).toISOString();
const points = (values) => values.map((energyKWh, index) => ({ periodStart: hour(index), energyKWh }));
const machine = (deviceId, category) => ({ deviceId, name: deviceId, category });
const plant = [
  machine('CHILLER-01', 'CHILLER'),
  machine('CHWP-01', 'CHILLED_WATER_PUMP'),
  machine('CWP-01', 'COOLING_WATER_PUMP'),
  machine('CT-01', 'COOLING_TOWER'),
];

test('each machine\'s energy is what its own sub-meter read; the total meter\'s remainder is unallocated', () => {
  const breakdown = energyBreakdown(plant, points([210, 220]), [
    { deviceId: 'CHILLER-01', points: points([150, 160]) },
    { deviceId: 'CHWP-01', points: points([45, 45]) },
    { deviceId: 'CT-01', points: points([10, 10]) },
  ]);

  assert.equal(breakdown.totalKWh, 430);
  assert.equal(breakdown.subMeteredKWh, 420);
  assert.equal(breakdown.unallocatedKWh, 10);
  assert.deepEqual(breakdown.items.map((item) => [item.deviceId, item.energyKWh]), [['CHILLER-01', 310], ['CHWP-01', 90], ['CT-01', 20]]);
  assert.equal(breakdown.items[0].share, 310 / 430);
  assert.deepEqual(breakdown.unmetered.map((item) => item.deviceId), ['CWP-01']);
  assert.deepEqual(
    breakdown.categories.map((category) => [category.category, category.energyKWh, category.metered, category.unmetered]),
    [['CHILLER', 310, 1, 0], ['CHILLED_WATER_PUMP', 90, 1, 0], ['COOLING_WATER_PUMP', 0, 0, 1], ['COOLING_TOWER', 20, 1, 0]],
  );
});

test('sub-meters adding up past the total meter leave a negative remainder, shown as it is', () => {
  const breakdown = energyBreakdown([machine('CHILLER-01', 'CHILLER')], points([100]), [{ deviceId: 'CHILLER-01', points: points([120]) }]);
  assert.equal(breakdown.unallocatedKWh, -20);
  assert.equal(breakdown.items[0].share, 1.2);
});

test('without a total meter reading nothing is unallocated and shares follow the sub-meters', () => {
  const breakdown = energyBreakdown(plant.slice(0, 2), [], [
    { deviceId: 'CHILLER-01', points: points([75]) },
    { deviceId: 'CHWP-01', points: points([25]) },
  ]);
  assert.equal(breakdown.totalKWh, null);
  assert.equal(breakdown.unallocatedKWh, null);
  assert.deepEqual(breakdown.items.map((item) => item.share), [0.75, 0.25]);
});

test('a sub-meter on a device the plant does not list is left out', () => {
  const breakdown = energyBreakdown([machine('CHILLER-01', 'CHILLER')], points([100]), [
    { deviceId: 'CHILLER-01', points: points([90]) },
    { deviceId: 'BOILER-09', points: points([5]) },
  ]);
  assert.deepEqual(breakdown.items.map((item) => item.deviceId), ['CHILLER-01']);
  assert.equal(breakdown.unallocatedKWh, 10);
});

test('energy per bucket lines up machines by the key a chart groups them on', () => {
  const breakdown = energyBreakdown(plant, [], [
    { deviceId: 'CHILLER-01', points: points([150, 160]) },
    { deviceId: 'CWP-01', points: points([30, 30]) },
  ], (periodStart) => (Date.parse(periodStart) < Date.UTC(2026, 9, 12, 1) ? '00:00' : '01:00'));
  assert.deepEqual(breakdown.rows, [
    { key: '00:00', CHILLER: 150, CHILLED_WATER_PUMP: 0, COOLING_WATER_PUMP: 30, COOLING_TOWER: 0 },
    { key: '01:00', CHILLER: 160, CHILLED_WATER_PUMP: 0, COOLING_WATER_PUMP: 30, COOLING_TOWER: 0 },
  ]);
});

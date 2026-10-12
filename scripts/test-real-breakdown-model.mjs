import assert from 'node:assert/strict';
import test from 'node:test';
import { energyBreakdown, powerBuckets } from '../apps/hvac-web/src/features/breakdown/breakdown-model.ts';

const hour = (h, minutes = 60) => {
  const start = Date.UTC(2026, 9, 12, h);
  return { periodStart: new Date(start).toISOString(), periodEnd: new Date(start + minutes * 60_000).toISOString() };
};
const equipment = (deviceId, category, averages) => ({
  deviceId, name: deviceId, category,
  buckets: averages.map((averageKW, index) => ({ ...hour(index), averageKW, peakKW: averageKW + 10 })),
});

test('aggregate buckets become average and peak power over their span', () => {
  const buckets = powerBuckets([
    { ...hour(0), gauge: { average: 100, maximum: 120, minimum: 80, first: 90, last: 110, sampleCount: 60 } },
    { ...hour(1), gauge: null },
  ]);
  assert.deepEqual(buckets, [{ ...hour(0), averageKW: 100, peakKW: 120 }]);
});

test('each machine\'s energy is its average power over each bucket; the meter\'s remainder is unallocated', () => {
  const breakdown = energyBreakdown([
    equipment('CHILLER-01', 'CHILLER', [150, 160]),
    equipment('CHWP-01', 'CHILLED_WATER_PUMP', [45, 45]),
    equipment('CT-01', 'COOLING_TOWER', [10, 10]),
  ], [{ periodStart: hour(0).periodStart, energyKWh: 210 }, { periodStart: hour(1).periodStart, energyKWh: 220 }]);

  assert.equal(breakdown.meteredKWh, 430);
  assert.deepEqual(breakdown.items.map((item) => [item.deviceId, item.energyKWh]), [['CHILLER-01', 310], ['CHWP-01', 90], ['CT-01', 20]]);
  assert.equal(breakdown.items[0].averageKW, 155);
  assert.equal(breakdown.items[0].peakKW, 170);
  assert.equal(breakdown.unallocatedKWh, 10);
  assert.equal(Math.round(breakdown.items[0].share * 1000), Math.round((310 / 430) * 1000));
  assert.deepEqual(breakdown.categories.map((category) => [category.category, category.energyKWh]), [['CHILLER', 310], ['CHILLED_WATER_PUMP', 90], ['COOLING_TOWER', 20]]);
});

test('a running bucket counts only the time it has run', () => {
  const breakdown = energyBreakdown([{ deviceId: 'CHILLER-01', name: 'CHILLER-01', category: 'CHILLER', buckets: [{ ...hour(0, 30), averageKW: 100, peakKW: 100 }] }], []);
  assert.equal(breakdown.items[0].energyKWh, 50);
  assert.equal(breakdown.meteredKWh, null);
  assert.equal(breakdown.unallocatedKWh, null);
});

test('when the machines add up past the meter, nothing is unallocated and shares follow the machines', () => {
  const breakdown = energyBreakdown([equipment('CHILLER-01', 'CHILLER', [120])], [{ periodStart: hour(0).periodStart, energyKWh: 100 }]);
  assert.equal(breakdown.unallocatedKWh, 0);
  assert.equal(breakdown.items[0].share, 1);
});

test('energy per bucket lines up machines by the key a chart groups them on', () => {
  const breakdown = energyBreakdown([
    equipment('CHILLER-01', 'CHILLER', [150, 160]),
    equipment('CWP-01', 'COOLING_WATER_PUMP', [30, 30]),
  ], [], (periodStart) => (Date.parse(periodStart) < Date.UTC(2026, 9, 12, 1) ? '00:00' : '01:00'));
  assert.deepEqual(breakdown.rows, [
    { key: '00:00', CHILLER: 150, CHILLED_WATER_PUMP: 0, COOLING_WATER_PUMP: 30, COOLING_TOWER: 0 },
    { key: '01:00', CHILLER: 160, CHILLED_WATER_PUMP: 0, COOLING_WATER_PUMP: 30, COOLING_TOWER: 0 },
  ]);
});

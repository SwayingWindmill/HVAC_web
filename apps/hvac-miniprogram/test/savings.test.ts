import assert from 'node:assert/strict';
import test from 'node:test';
import { monthToDate, savingsCard } from '../miniprogram/lib/savings.ts';

test('the month so far is read from the start of the Site day the owner cut, never from the phone time zone', () => {
  // 00:00 on 21 October at a UTC+8 Site is 16:00Z the day before.
  assert.deepEqual(monthToDate('2026-10-20T16:00:00Z'), { from: '2026-10-01', to: '2026-10-21' });
  // 00:00 on 1 November at a UTC−5 Site.
  assert.deepEqual(monthToDate('2026-11-01T05:00:00Z'), { from: '2026-11-01', to: '2026-11-01' });
});

const measure = (name: string, days: number, avoidedKWh: number, avoidedCost: number, trusted = true) => ({
  id: name, name, status: 'IMPLEMENTED', baseline: { trusted }, totals: { days, avoidedKWh, avoidedCost },
});

test('the savings card leads with the money saved this month, and each counted measure with its own', () => {
  const card = savingsCard(
    { days: 20, avoidedKWh: 7350, avoidedCost: 8640, plantKWh: 44410 },
    [measure('冷冻水供水温度调高 1 °C', 20, 5020, 5910), measure('夜间只开一台冷却塔', 20, 2330, 2730), { ...measure('已结束', 5, 10, 10), status: 'ENDED' }],
  );
  assert.deepEqual(card.saved, { cost: 8640, kWh: 7350, rate: 0.142 });
  assert.deepEqual(card.measures.map((each) => each.name), ['冷冻水供水温度调高 1 °C', '夜间只开一台冷却塔']);
});

test('without savings yet the card says why, in the order an operator can act on', () => {
  const empty = { days: 0, avoidedKWh: 0, avoidedCost: 0, plantKWh: 0 };
  assert.equal(savingsCard(empty, []).why, 'NO_MEASURE');
  assert.equal(savingsCard(empty, [measure('调高供水温度', 0, 0, 0, false)]).why, 'BASELINE_UNTRUSTED');
  assert.equal(savingsCard(empty, [measure('调高供水温度', 0, 0, 0)]).why, 'FIRST_DAY_PENDING');
  assert.equal(savingsCard(empty, [{ ...measure('调高供水温度', 0, 0, 0), baseline: null }]).why, 'BASELINE_UNTRUSTED');
  assert.equal(savingsCard(empty, []).saved, null);
});

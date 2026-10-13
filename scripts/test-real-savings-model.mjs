import assert from 'node:assert/strict';
import test from 'node:test';
import { boundaryLabel, costBasis, dayRows, fitChecks, instantFromSiteInput, measureActions, monthToDate, savingsRate, siteInputValue, siteSavingsRate } from '../apps/hvac-web/src/features/savings/savings-model.ts';

const zone = 'Asia/Shanghai';
const totals = (days) => ({ days, unpricedHours: 0, adjustedBaselineKWh: 0, actualKWh: 0, avoidedKWh: 0, avoidedCost: 0 });

test('a baseline passes Guideline 14 only when coverage, CV(RMSE) and NMBE all hold', () => {
  const model = { interceptKWh: 20, slope: 0.25, cvRmse: 0.12, nmbe: -0.004 };
  const baseline = { samples: 320, expectedHours: 336, model };
  assert.deepEqual(fitChecks(baseline).map((check) => check.pass), [true, true, true]);
  assert.equal(fitChecks(baseline)[0].value, '320 / 336（95%）');
  assert.deepEqual(fitChecks({ ...baseline, model: { ...model, cvRmse: 0.31, nmbe: 0.11 } }).map((check) => check.pass), [true, false, false]);
  assert.equal(fitChecks({ ...baseline, model: { ...model, nmbe: -0.1 } })[2].pass, true);
});

test('a period too thin to fit is judged on its hours alone', () => {
  const checks = fitChecks({ samples: 15, expectedHours: 336, model: null });
  assert.deepEqual(checks.map((check) => check.pass), [false, null, null]);
  assert.deepEqual(checks.map((check) => check.value), ['15 / 336（4%）', '未拟合', '未拟合']);
});

test('only a failed baseline is refitted, and only until the first day is published', () => {
  assert.deepEqual(measureActions({ status: 'PLANNED', totals: totals(0) }), ['implement']);
  assert.deepEqual(measureActions({ status: 'IMPLEMENTED', totals: totals(0), baseline: { trusted: false } }), ['refit', 'end']);
  assert.deepEqual(measureActions({ status: 'IMPLEMENTED', totals: totals(0), baseline: { trusted: true } }), ['end']);
  assert.deepEqual(measureActions({ status: 'IMPLEMENTED', totals: totals(3), baseline: { trusted: true } }), ['end']);
  assert.deepEqual(measureActions({ status: 'ENDED', totals: totals(3) }), []);
});

test('verified days chart oldest first with each day\'s coverage', () => {
  const day = (date, avoided, hours = 24) => ({ day: date, expectedHours: 24, hours, unpricedHours: 0, adjustedBaselineKWh: 1000, actualKWh: 1000 - avoided, avoidedKWh: avoided, avoidedCost: avoided * 0.8 });
  const rows = dayRows([day('2026-10-17', 90), day('2026-10-16', 100, 18)]);
  assert.deepEqual(rows.map((row) => [row.label, row.avoided, row.coverage]), [['10/16', 100, 0.75], ['10/17', 90, 1]]);
});

test('the savings rate is avoided energy over the adjusted baseline, absent before any day', () => {
  assert.equal(savingsRate({ ...totals(2), adjustedBaselineKWh: 2000, avoidedKWh: 190 }), 0.095);
  assert.equal(savingsRate(totals(0)), null);
});

// Measures on several machines add up against the whole plant: what it used plus what it avoided.
test("a Site's savings rate is avoided energy over what the plant would have used, absent before any day", () => {
  assert.equal(siteSavingsRate({ avoidedKWh: 100, plantKWh: 900 }), 0.1);
  assert.equal(siteSavingsRate({ avoidedKWh: 0, plantKWh: 0 }), null);
});

test("a measurement boundary is the plant meter or a machine's sub-meter, named as the plant page names it", () => {
  assert.equal(boundaryLabel(null, null), '冷站总表');
  assert.equal(boundaryLabel('chwp', new Map([['chwp', 'CHWP-01']])), 'CHWP-01 分表');
  assert.equal(boundaryLabel('chwp', null), '设备分表');
});

test('the summary runs from the first of the Site month to the Site day', () => {
  assert.deepEqual(monthToDate(new Date('2026-10-31T17:30:00Z'), zone), { from: '2026-11-01', to: '2026-11-01' });
  assert.deepEqual(monthToDate(new Date('2026-10-12T04:00:00Z'), zone), { from: '2026-10-01', to: '2026-10-12' });
});

test('implementation times are entered on the Site clock whatever the browser zone', () => {
  assert.equal(instantFromSiteInput('2026-10-15T09:00', zone).toISOString(), '2026-10-15T01:00:00.000Z');
  assert.equal(siteInputValue(new Date('2026-10-15T01:00:00Z'), zone), '2026-10-15T09:00');
  assert.equal(instantFromSiteInput('2026-03-08T09:30', 'America/New_York').toISOString(), '2026-03-08T13:30:00.000Z');
});

test('avoided cost says when hours went unpriced for want of a tariff', () => {
  assert.equal(costBasis({ unpricedHours: 0 }), '按逐时分时电度电价，不含需量电费');
  assert.equal(costBasis({ unpricedHours: 24 }), '24 小时没有电价未计入；不含需量电费');
});

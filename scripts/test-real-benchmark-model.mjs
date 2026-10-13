import assert from 'node:assert/strict';
import test from 'node:test';
import { benchmark } from '../apps/hvac-web/src/features/benchmark/benchmark-model.ts';

const row = (day, electricityKWh, coolingKWh) => ({
  periodStart: `2026-10-${String(day).padStart(2, '0')}T16:00:00.000Z`,
  label: `10/${day}`,
  electricityKWh,
  coolingKWh,
  cop: electricityKWh && coolingKWh !== null ? coolingKWh / electricityKWh : null,
});
const summary = (rows) => {
  const complete = rows.filter((entry) => entry.cop !== null);
  const sum = (key, entries) => entries.reduce((total, entry) => total + (entry[key] ?? 0), 0);
  return {
    granularity: 'day', rows, peak: null, dataWatermark: null, excludedIntervals: 0,
    electricityKWh: rows.some((entry) => entry.electricityKWh !== null) ? sum('electricityKWh', rows) : null,
    coolingKWh: rows.some((entry) => entry.coolingKWh !== null) ? sum('coolingKWh', rows) : null,
    cop: complete.length ? sum('coolingKWh', complete) / sum('electricityKWh', complete) : null,
  };
};

test('sites rank by plant COP, each against the fleet\'s energy-weighted COP', () => {
  const result = benchmark([
    { siteId: 'a', name: '一号站', floorAreaM2: null, climateZone: null, summary: summary([row(1, 1000, 3000), row(2, 1000, 3200)]) },
    { siteId: 'b', name: '二号站', floorAreaM2: null, climateZone: null, summary: summary([row(1, 500, 2000), row(2, 500, 2100)]) },
  ]);
  assert.deepEqual(result.sites.map((site) => site.siteId), ['b', 'a']);
  assert.equal(result.fleet.electricityKWh, 3000);
  assert.equal(result.fleet.cop, (6200 + 4100) / 3000);
  const [best, other] = result.sites;
  assert.equal(best.cop, 4.1);
  assert.equal(best.dailyElectricityKWh, 500);
  assert.equal(Math.round(best.copVsFleet * 1000), Math.round((4.1 / ((6200 + 4100) / 3000) - 1) * 1000));
  assert.equal(other.rank, 2);
});

test('a site without both energies has no COP and ranks last, without a gap to the fleet', () => {
  const result = benchmark([
    { siteId: 'a', name: '一号站', floorAreaM2: null, climateZone: null, summary: summary([row(1, 1000, null)]) },
    { siteId: 'b', name: '二号站', floorAreaM2: null, climateZone: null, summary: summary([row(1, 500, 2000)]) },
  ]);
  assert.deepEqual(result.sites.map((site) => [site.siteId, site.rank, site.copVsFleet]), [['b', 1, 0], ['a', null, null]]);
  assert.equal(result.fleet.cop, 4);
});

test('daily COP lines up each site by day for one chart', () => {
  const result = benchmark([
    { siteId: 'a', name: '一号站', floorAreaM2: null, climateZone: null, summary: summary([row(1, 1000, 3000), row(2, 1000, 3200)]) },
    { siteId: 'b', name: '二号站', floorAreaM2: null, climateZone: null, summary: summary([row(2, 500, 2100)]) },
  ]);
  assert.deepEqual(result.daily, [
    { key: '2026-10-01T16:00:00.000Z', label: '10/1', a: 3, b: null },
    { key: '2026-10-02T16:00:00.000Z', label: '10/2', a: 3.2, b: 4.2 },
  ]);
});

// Energy per square metre (GB/T 51161) compares sites of different sizes: least is best, and a site
// without a recorded floor area stays out of that ranking rather than ranking last.
test('energy per square metre ranks the sites with a floor area, least first', () => {
  const result = benchmark([
    { siteId: 'a', name: '一号站', floorAreaM2: 1000, climateZone: 'HOT_SUMMER_COLD_WINTER', summary: summary([row(1, 1000, 3000), row(2, 1000, 3200)]) },
    { siteId: 'b', name: '二号站', floorAreaM2: 250, climateZone: 'HOT_SUMMER_WARM_WINTER', summary: summary([row(1, 500, 2000), row(2, 500, 2100)]) },
    { siteId: 'c', name: '三号站', floorAreaM2: null, climateZone: null, summary: summary([row(1, 100, 300)]) },
  ]);
  const byId = Object.fromEntries(result.sites.map((site) => [site.siteId, site]));
  assert.equal(byId.a.intensityKWhPerM2, 2);
  assert.equal(byId.b.intensityKWhPerM2, 4);
  assert.deepEqual([byId.a.intensityRank, byId.b.intensityRank, byId.c.intensityRank], [1, 2, null]);
  assert.equal(byId.c.intensityKWhPerM2, null);
  assert.equal(byId.b.climateZone, 'HOT_SUMMER_WARM_WINTER');
  // The COP ranking is unchanged by floor area.
  assert.deepEqual([byId.b.rank, byId.a.rank, byId.c.rank], [1, 2, 3]);
});

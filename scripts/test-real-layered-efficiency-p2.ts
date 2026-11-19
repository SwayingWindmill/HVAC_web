import assert from 'node:assert/strict';
import test from 'node:test';
import {
  computeLayeredEfficiency,
  computeMeterBalance,
} from '../apps/hvac-web/src/features/energy-analysis/consumption/layered-efficiency.ts';
import type { Breakdown } from '../apps/hvac-web/src/features/breakdown/breakdown-model.ts';
import type { EnergySummary } from '../apps/hvac-web/src/features/energy-analysis/consumption/model.ts';
import { dayRows, fitChecks } from '../apps/hvac-web/src/features/savings/savings-model.ts';
import { METER_IMBALANCE_RULE, RULES, ruleFor } from '../apps/hvac-web/src/features/diagnostics/diagnostics-model.ts';
import type { ReportSnapshot } from '../apps/hvac-web/src/features/reports/report-model.ts';

function makeSummary(overrides: Partial<Pick<EnergySummary, 'cop' | 'electricityKWh' | 'coolingKWh'>> = {}) {
  return {
    cop: 4.0,
    electricityKWh: 1000,
    coolingKWh: 4000,
    ...overrides,
  };
}

function makeBreakdown(overrides: Partial<Pick<Breakdown, 'totalKWh' | 'subMeteredKWh' | 'categories'>> = {}) {
  return {
    totalKWh: 1000,
    subMeteredKWh: 980,
    categories: [
      { category: 'CHILLER', energyKWh: 700, share: 0.7, metered: 1, unmetered: 0 },
      { category: 'CHILLED_WATER_PUMP', energyKWh: 120, share: 0.12, metered: 1, unmetered: 0 },
      { category: 'COOLING_WATER_PUMP', energyKWh: 100, share: 0.1, metered: 1, unmetered: 0 },
      { category: 'COOLING_TOWER', energyKWh: 60, share: 0.06, metered: 1, unmetered: 0 },
    ] satisfies Breakdown['categories'],
    ...overrides,
  };
}

test('computeLayeredEfficiency computes plant COP, chiller COP, pump WTFs, and tower power share', () => {
  const result = computeLayeredEfficiency({
    summary: makeSummary({ cop: 4.0, electricityKWh: 1000, coolingKWh: 4000 }),
    breakdown: makeBreakdown(),
  });
  assert.equal(result.plantCop, 4.0);
  assert.ok(Math.abs((result.chillerCop ?? 0) - 4000 / 700) < 1e-9);
  assert.ok(Math.abs((result.chilledPumpWtf ?? 0) - 4000 / 120) < 1e-9);
  assert.equal(result.coolingPumpWtf, 40);
  assert.equal(result.towerPowerShare, 0.06);
});

test('computeLayeredEfficiency enforces standby, physical bounds, and unmetered guards', () => {
  const lowLoad = computeLayeredEfficiency({
    summary: makeSummary({ cop: null, coolingKWh: 0.5, electricityKWh: 100 }),
    breakdown: makeBreakdown(),
  });
  assert.deepEqual(lowLoad, {
    plantCop: null,
    chillerCop: null,
    chilledPumpWtf: null,
    coolingPumpWtf: null,
    towerPowerShare: 0.06,
  });

  const unmeteredAndOutliers = computeLayeredEfficiency({
    summary: makeSummary({ cop: 3.5, electricityKWh: 1000, coolingKWh: 4000 }),
    breakdown: {
      totalKWh: 1000,
      categories: [
        // chillerCop = 4000 / 200 = 20 (> 15 outlier -> null)
        { category: 'CHILLER', energyKWh: 200, share: 0.2, metered: 1, unmetered: 0 },
        // pumpWtf = 4000 / 20 = 200 (> 100 outlier -> null)
        { category: 'CHILLED_WATER_PUMP', energyKWh: 20, share: 0.02, metered: 1, unmetered: 0 },
        // pumpKWh < 0.5 -> null
        { category: 'COOLING_WATER_PUMP', energyKWh: 0.4, share: 0, metered: 1, unmetered: 0 },
        // metered === 0 -> null (never report 0% for unmetered tower)
        { category: 'COOLING_TOWER', energyKWh: 0, share: 0, metered: 0, unmetered: 1 },
      ],
    },
  });
  assert.equal(unmeteredAndOutliers.plantCop, 3.5);
  assert.equal(unmeteredAndOutliers.chillerCop, null);
  assert.equal(unmeteredAndOutliers.chilledPumpWtf, null);
  assert.equal(unmeteredAndOutliers.coolingPumpWtf, null);
  assert.equal(unmeteredAndOutliers.towerPowerShare, null);

  // Tower share clamps within 1.05x tolerance and rejects > 1.05x
  const clamped = computeLayeredEfficiency({
    summary: makeSummary({ electricityKWh: 100, coolingKWh: 400 }),
    breakdown: {
      totalKWh: 100,
      categories: [{ category: 'COOLING_TOWER', energyKWh: 104, share: 1.04, metered: 1, unmetered: 0 }],
    },
  });
  assert.equal(clamped.towerPowerShare, 1);

  const excessTower = computeLayeredEfficiency({
    summary: makeSummary({ electricityKWh: 100, coolingKWh: 400 }),
    breakdown: {
      totalKWh: 100,
      categories: [{ category: 'COOLING_TOWER', energyKWh: 106, share: 1.06, metered: 1, unmetered: 0 }],
    },
  });
  assert.equal(excessTower.towerPowerShare, null);
});

test('computeMeterBalance classifies NO_MAIN_METER, BALANCED, UNMETERED_GAP, and SUBMETER_EXCEEDS_MAIN', () => {
  assert.equal(computeMeterBalance({ breakdown: null }).status, 'NO_MAIN_METER');
  assert.equal(computeMeterBalance({ breakdown: { totalKWh: null, subMeteredKWh: 400 } }).status, 'NO_MAIN_METER');
  assert.equal(computeMeterBalance({ breakdown: { totalKWh: 0, subMeteredKWh: 10 } }).status, 'NO_MAIN_METER');

  const balanced = computeMeterBalance({ breakdown: { totalKWh: 1000, subMeteredKWh: 950 } });
  assert.equal(balanced.status, 'BALANCED');
  assert.equal(balanced.diffKWh, 50);
  assert.equal(balanced.diffRatio, 0.05);

  const balancedNegative = computeMeterBalance({ breakdown: { totalKWh: 1000, subMeteredKWh: 1050 } });
  assert.equal(balancedNegative.status, 'BALANCED');
  assert.equal(balancedNegative.diffRatio, -0.05);

  const gap = computeMeterBalance({ breakdown: { totalKWh: 1000, subMeteredKWh: 940 } });
  assert.equal(gap.status, 'UNMETERED_GAP');
  assert.equal(gap.diffKWh, 60);

  const exceeds = computeMeterBalance({ breakdown: { totalKWh: 1000, subMeteredKWh: 1060 } });
  assert.equal(exceeds.status, 'SUBMETER_EXCEEDS_MAIN');
  assert.equal(exceeds.diffKWh, -60);

  const customThreshold = computeMeterBalance({ breakdown: { totalKWh: 1000, subMeteredKWh: 970 }, threshold: 0.02 });
  assert.equal(customThreshold.status, 'UNMETERED_GAP');
});

test('savings fitChecks appends R² only when present and dayRows preserves condition anchors', () => {
  const univariateChecks = fitChecks({
    samples: 336,
    expectedHours: 336,
    model: { interceptKWh: 80, slope: 0.2, cvRmse: 0.12, nmbe: 0.01 },
  });
  assert.equal(univariateChecks.length, 3);

  const passingBivariate = fitChecks({
    samples: 336,
    expectedHours: 336,
    model: {
      modelKind: 'BIVARIATE_COOLING_WET_BULB',
      interceptKWh: 82.4,
      slope: 0.168,
      wetBulbSlope: 2.45,
      rSquared: 0.942,
      meanWetBulbC: 24.5,
      cvRmse: 0.08,
      nmbe: -0.002,
    },
  });
  assert.equal(passingBivariate.length, 4);
  assert.deepEqual(passingBivariate[3], {
    label: '决定系数 R²',
    value: '0.942',
    limit: '≥ 0.75',
    pass: true,
  });

  const failingBivariate = fitChecks({
    samples: 336,
    expectedHours: 336,
    model: {
      modelKind: 'BIVARIATE_COOLING_WET_BULB',
      interceptKWh: 82.4,
      slope: 0.168,
      wetBulbSlope: 2.45,
      rSquared: 0.68,
      cvRmse: 0.22,
      nmbe: 0.04,
    },
  });
  assert.equal(failingBivariate[3]?.pass, false);

  const rows = dayRows([
    {
      day: '2026-10-03',
      expectedHours: 24,
      hours: 24,
      unpricedHours: 0,
      adjustedBaselineKWh: 4200,
      actualKWh: 3900,
      avoidedKWh: 300,
      avoidedCost: 270,
      plantKWh: 3900,
      meanWetBulbC: 24.1,
      meanDryBulbC: 28.4,
      meanLoadRatio: 0.72,
      aiClosedLoopShare: 0.88,
    },
    {
      day: '2026-10-02',
      expectedHours: 24,
      hours: 24,
      unpricedHours: 0,
      adjustedBaselineKWh: 4100,
      actualKWh: 3820,
      avoidedKWh: 280,
      avoidedCost: 252,
      plantKWh: 3820,
      meanWetBulbC: 23.8,
      meanLoadRatio: 0.68,
      aiClosedLoopShare: 0.85,
    },
  ]);
  assert.deepEqual(rows.map((row) => [row.day, row.meanWetBulbC, row.meanDryBulbC, row.meanLoadRatio, row.aiClosedLoopShare]), [
    ['2026-10-02', 23.8, null, 0.68, 0.85],
    ['2026-10-03', 24.1, 28.4, 0.72, 0.88],
  ]);
});

test('diagnostics RULES registers METER_IMBALANCE rule without deprecated copy terms', () => {
  assert.ok(RULES.some((rule) => rule.findingType === 'METER_IMBALANCE'));
  const found = ruleFor('METER_IMBALANCE');
  assert.equal(found, METER_IMBALANCE_RULE);
  assert.equal(found?.title, '主计量与设备计量差额偏大');
  assert.equal(found?.designValue, 5);
  assert.equal(found?.unit, '%');
});

test('ReportSnapshot P2 layeredEfficiency, meterBalance, and conditionAnchors align with report-service JSON contract', () => {
  const snapshot: ReportSnapshot = {
    schemaVersion: 1,
    month: '2026-09',
    timezone: 'Asia/Shanghai',
    daysInMonth: 30,
    daysWithData: 16,
    electricityKWh: 72696.83,
    coolingKWh: 209458.9,
    cop: 2.88,
    energyCostCNY: 60258.71,
    energyCostStatus: 'PRICED',
    tariffPeriods: [],
    days: [],
    findings: { count: 0, byType: [] },
    savings: {
      days: 0,
      adjustedBaselineKWh: 0,
      avoidedKWh: 0,
      avoidedCost: 0,
      unpricedHours: 0,
      plantKWh: 0,
    },
    layeredEfficiency: {
      plantCOP: 4.18,
      chillerCOP: 5.62,
      chilledPumpWTF: 32.4,
      coolingPumpWTF: 28.9,
      towerPowerShare: 0.084,
    },
    meterBalance: {
      siteKWh: 72696.83,
      submeterSumKWh: 71842.15,
      diffKWh: 854.68,
      imbalanceRatio: 0.0118,
      status: 'BALANCED',
    },
    conditionAnchors: {
      meanWetBulbC: 23.6,
      meanDryBulbC: 28.4,
      meanLoadRatio: 0.64,
      aiClosedLoopShare: 0.72,
    },
  };
  assert.equal(snapshot.layeredEfficiency?.chillerCOP, 5.62);
  assert.equal(snapshot.meterBalance?.siteKWh, 72696.83);
  assert.equal(snapshot.conditionAnchors?.aiClosedLoopShare, 0.72);
});

import assert from 'node:assert/strict';
import test from 'node:test';
import {
  PlatformApiError,
  type EnergySeriesResponse,
  type MonthlyElectricityBudget,
  type UtilityBill,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import {
  billingMonthOptions,
  computeUtilityBillTotalPayable,
  describeBillingApiError,
  normalizeBillingMonth,
  reconcileUtilityBill,
  resolveBillingMonthWindow,
  resolveReadingDateWindow,
  summarizeMonthlyBudgetProgress,
} from '../apps/hvac-web/src/features/billing/billing-model.ts';

const SITE_ID = '018f3e00-1000-7000-8000-000000000001';
const TENANT_ID = '018f3d00-0000-7000-8000-000000000001';
const TIMEZONE = 'Asia/Shanghai';

function makeBudget(amount: number, billingMonth = '2026-10'): MonthlyElectricityBudget {
  return {
    id: '01990000-8000-7000-8000-000000000001',
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    billingMonth,
    budgetAmountCny: amount,
    note: '10 月空调预算',
    revision: 1,
    updatedBy: '018f3e00-2000-7000-8000-000000000001',
    updatedByDisplay: '张工',
    createdAt: '2026-10-01T01:00:00.000Z',
    updatedAt: '2026-10-01T01:00:00.000Z',
  };
}

function makeDailyElectricitySeries(
  days: readonly {
    readonly dateUtcStart: string;
    readonly energyKWh: number;
    readonly unpricedKWh?: number;
    readonly peakCost?: number;
    readonly flatCost?: number;
    readonly valleyCost?: number;
    readonly superPeakCost?: number;
    readonly omitCost?: boolean;
  }[],
  watermark?: string,
): EnergySeriesResponse {
  return {
    schemaVersion: 1,
    points: days.map((day) => {
      const startMs = Date.parse(day.dateUtcStart);
      const superPeak = day.superPeakCost ?? 0;
      const peak = day.peakCost ?? 0;
      const flat = day.flatCost ?? 0;
      const valley = day.valleyCost ?? 0;
      const unpriced = day.unpricedKWh ?? 0;
      const pricedKWh = Math.max(0, day.energyKWh - unpriced);
      return {
        periodStart: new Date(startMs).toISOString(),
        periodEnd: new Date(startMs + 86_400_000).toISOString(),
        energyKWh: day.energyKWh,
        ...(day.omitCost
          ? {}
          : {
              cost: {
                amountCNY: Number((superPeak + peak + flat + valley).toFixed(2)),
                unpricedKWh: unpriced,
                periods: [
                  ...(superPeak > 0
                    ? [{ periodCode: 'SUPER_PEAK' as const, energyKWh: pricedKWh * 0.1, amountCNY: superPeak }]
                    : []),
                  ...(peak > 0
                    ? [{ periodCode: 'PEAK' as const, energyKWh: pricedKWh * 0.4, amountCNY: peak }]
                    : []),
                  ...(flat > 0
                    ? [{ periodCode: 'FLAT' as const, energyKWh: pricedKWh * 0.3, amountCNY: flat }]
                    : []),
                  ...(valley > 0
                    ? [{ periodCode: 'VALLEY' as const, energyKWh: pricedKWh * 0.2, amountCNY: valley }]
                    : []),
                ],
              },
            }),
      };
    }),
    metadata: {
      requestedGranularity: 'day',
      actualGranularity: 'day',
      dataWatermark: watermark ?? '2026-10-15T16:00:00.000Z',
      aggregateWatermark: watermark ?? '2026-10-15T16:00:00.000Z',
      datasetRevision: 'local-development-v1:1',
      partial: false,
      qualitySummary: { valid: days.length * 96, suspect: 2, invalid: 1 },
    },
  };
}

function makeBill(overrides: Partial<UtilityBill> = {}): UtilityBill {
  return {
    id: '01990000-9000-7000-8000-000000000001',
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    billingMonth: '2026-10',
    meterScope: 'CHILLER_PLANT',
    readingStartDate: '2026-09-25',
    readingEndDate: '2026-10-24',
    billedEnergyKwh: 120_000,
    energyChargeCny: 98_400,
    basicChargeMode: 'MAX_DEMAND_KW',
    basicChargeQuantity: 620,
    basicChargeCny: 24_800,
    standardPowerFactor: 0.9,
    actualPowerFactor: 0.95,
    powerFactorAdjustmentCny: -1_257,
    otherChargesCny: 357,
    totalPayableCny: 122_300,
    chillerShareOverrideRatio: null,
    utilityAccountNo: '',
    invoiceNo: 'GD-202610-0081',
    note: '供电局 10 月结算单',
    status: 'DRAFT',
    confirmedAt: null,
    confirmedBy: null,
    confirmedByDisplay: null,
    revision: 1,
    updatedBy: '018f3e00-2000-7000-8000-000000000001',
    updatedByDisplay: '张工',
    createdAt: '2026-10-26T02:00:00.000Z',
    updatedAt: '2026-10-26T02:00:00.000Z',
    ...overrides,
  };
}

test('resolveBillingMonthWindow handles past, current, future, and leap-year months on site clock', () => {
  const now = new Date('2026-10-15T16:00:00.000Z'); // 2026-10-16 00:00 Asia/Shanghai (15 days elapsed)

  const past = resolveBillingMonthWindow('2026-09', now, TIMEZONE);
  assert.equal(past.state, 'PAST');
  assert.equal(past.daysInMonth, 30);
  assert.equal(past.from.toISOString(), '2026-08-31T16:00:00.000Z');
  assert.equal(past.monthEnd.toISOString(), '2026-09-30T16:00:00.000Z');
  assert.equal(past.queryTo.toISOString(), '2026-09-30T16:00:00.000Z');
  assert.equal(past.elapsedDays, 30);

  const current = resolveBillingMonthWindow('2026-10', now, TIMEZONE);
  assert.equal(current.state, 'CURRENT');
  assert.equal(current.daysInMonth, 31);
  assert.equal(current.elapsedDays, 15);
  assert.equal(current.queryTo.toISOString(), now.toISOString());

  const future = resolveBillingMonthWindow('2026-11', now, TIMEZONE);
  assert.equal(future.state, 'FUTURE');
  assert.equal(future.daysInMonth, 30);
  assert.equal(future.elapsedDays, 0);
  assert.equal(future.queryTo.toISOString(), future.from.toISOString());

  const leap = resolveBillingMonthWindow('2028-02', now, TIMEZONE);
  assert.equal(leap.daysInMonth, 29);

  const options = billingMonthOptions(now, TIMEZONE);
  assert.ok(options.includes('2026-11'));
  assert.ok(options.includes('2026-10'));
  assert.ok(options.includes('2025-11'));
  assert.equal(normalizeBillingMonth('invalid', now, TIMEZONE), '2026-10');
  assert.equal(normalizeBillingMonth('2026-09', now, TIMEZONE), '2026-09');
});

test('summarizeMonthlyBudgetProgress computes cumulative cost, month-end projection, linear budget line, and TOU shares', () => {
  // 15 days elapsed in October (31 days): 15 days * 6,000 CNY/day = 90,000 CNY cumulative -> projected 186,000 CNY
  const now = new Date('2026-10-15T16:00:00.000Z'); // Oct 16 00:00 CST
  const days = Array.from({ length: 15 }, (_, index) => ({
    dateUtcStart: new Date(Date.parse('2026-09-30T16:00:00.000Z') + index * 86_400_000).toISOString(),
    energyKWh: 7_500,
    superPeakCost: 600,
    peakCost: 3_000,
    flatCost: 1_800,
    valleyCost: 600,
  }));
  const series = makeDailyElectricitySeries(days, '2026-10-15T16:00:00.000Z');
  const budget = makeBudget(170_000, '2026-10');

  const summary = summarizeMonthlyBudgetProgress({
    electricity: series,
    billingMonth: '2026-10',
    budget,
    now,
    timeZone: TIMEZONE,
  });

  assert.equal(summary.pricingState, 'PRICED');
  assert.equal(summary.cumulativeEstimatedCny, 90_000);
  assert.equal(summary.projectedMonthEndCny, 186_000);
  assert.equal(summary.projectedOverrunCny, 16_000);
  assert.ok(summary.usageRatio !== null && Math.abs(summary.usageRatio - 90_000 / 170_000) < 1e-6);
  assert.equal(summary.dailySeries.length, 31);
  assert.equal(summary.dailySeries[0]?.cumulativeCostCny, 6_000);
  assert.equal(summary.dailySeries[14]?.cumulativeCostCny, 90_000);
  assert.equal(summary.dailySeries[20]?.cumulativeCostCny, null);
  assert.equal(summary.dailySeries[30]?.linearBudgetCny, 170_000);
  assert.deepEqual(
    summary.touPeriods.map((item) => [item.code, item.costCny]),
    [
      ['SUPER_PEAK', 9_000],
      ['PEAK', 45_000],
      ['FLAT', 27_000],
      ['VALLEY', 9_000],
    ],
  );
});

test('summarizeMonthlyBudgetProgress refuses to fabricate full cost when electricity is partially unpriced or missing', () => {
  const now = new Date('2026-10-15T16:00:00.000Z');
  const partialSeries = makeDailyElectricitySeries([
    {
      dateUtcStart: '2026-09-30T16:00:00.000Z',
      energyKWh: 1_000,
      unpricedKWh: 100,
      peakCost: 600,
      flatCost: 300,
    },
  ]);

  const partial = summarizeMonthlyBudgetProgress({
    electricity: partialSeries,
    billingMonth: '2026-10',
    budget: makeBudget(150_000),
    now,
    timeZone: TIMEZONE,
  });
  assert.equal(partial.pricingState, 'PARTIAL_UNPRICED');
  assert.equal(partial.cumulativeEstimatedCny, null);
  assert.equal(partial.projectedMonthEndCny, null);
  assert.equal(partial.unpricedKWh, 100);
  assert.equal(partial.pricedPortionCny, 900);

  const empty = summarizeMonthlyBudgetProgress({
    electricity: makeDailyElectricitySeries([]),
    billingMonth: '2026-10',
    budget: null,
    now,
    timeZone: TIMEZONE,
  });
  assert.equal(empty.pricingState, 'NO_DATA');
  assert.equal(empty.cumulativeEstimatedCny, null);
  assert.equal(empty.projectedMonthEndCny, null);
});

test('resolveReadingDateWindow converts inclusive cross-month local dates to half-open site instants', () => {
  const window = resolveReadingDateWindow('2026-09-25', '2026-10-24', TIMEZONE);
  assert.ok(window !== null);
  assert.equal(window.from.toISOString(), '2026-09-24T16:00:00.000Z');
  assert.equal(window.to.toISOString(), '2026-10-24T16:00:00.000Z');
  assert.equal(window.dayCount, 30);

  const singleDay = resolveReadingDateWindow('2026-10-01', '2026-10-01', TIMEZONE);
  assert.ok(singleDay !== null);
  assert.equal(singleDay.dayCount, 1);

  assert.equal(resolveReadingDateWindow('2026-10-25', '2026-10-24', TIMEZONE), null);
});

test('computeUtilityBillTotalPayable handles exact cents and negative power-factor rewards', () => {
  const total = computeUtilityBillTotalPayable({
    energyChargeCny: 98_400.15,
    basicChargeCny: 24_800.2,
    powerFactorAdjustmentCny: -1_257.35,
    otherChargesCny: 357.0,
  });
  assert.equal(total, 122_300);
});

test('reconcileUtilityBill computes direct meter & energy-charge variance for CHILLER_PLANT scope', () => {
  const bill = makeBill({
    meterScope: 'CHILLER_PLANT',
    billedEnergyKwh: 120_000,
    energyChargeCny: 98_400,
    basicChargeCny: 24_800,
    powerFactorAdjustmentCny: -1_257,
    otherChargesCny: 357,
    totalPayableCny: 122_300,
  });
  // Platform measured 118,000 kWh and 96,000 CNY energy charge over the same 30-day reading window
  const windowSeries = makeDailyElectricitySeries([
    {
      dateUtcStart: '2026-09-24T16:00:00.000Z',
      energyKWh: 118_000,
      peakCost: 58_000,
      flatCost: 26_000,
      valleyCost: 12_000,
    },
  ]);

  const result = reconcileUtilityBill(bill, windowSeries);
  assert.equal(result.mode, 'DIRECT_VARIANCE');
  assert.equal(result.nonEnergyChargeCny, 23_900);
  assert.equal(result.plantEnergyKwh, 118_000);
  assert.equal(result.plantEstimatedEnergyCostCny, 96_000);
  assert.equal(result.energyVarianceKwh, 2_000);
  assert.ok(result.energyVarianceRatio !== null && Math.abs(result.energyVarianceRatio - 2_000 / 118_000) < 1e-6);
  assert.equal(result.energyChargeVarianceCny, 2_400);
  assert.ok(
    result.energyChargeVarianceRatio !== null &&
      Math.abs(result.energyChargeVarianceRatio - 2_400 / 96_000) < 1e-6,
  );
});

test('reconcileUtilityBill computes building energy-share allocation and honors finance override for WHOLE_BUILDING scope', () => {
  const wholeBuildingBill = makeBill({
    meterScope: 'WHOLE_BUILDING',
    billedEnergyKwh: 400_000,
    energyChargeCny: 310_000,
    basicChargeMode: 'TRANSFORMER_CAPACITY_KVA',
    basicChargeQuantity: 2_000,
    basicChargeCny: 48_000,
    powerFactorAdjustmentCny: -3_000,
    otherChargesCny: 5_000,
    totalPayableCny: 360_000,
    chillerShareOverrideRatio: null,
  });
  // Chiller plant consumed 130,000 kWh out of 400,000 kWh (32.5%), blended rate = 0.90 CNY/kWh -> allocated = 117,000 CNY
  const plantSeries = makeDailyElectricitySeries([
    {
      dateUtcStart: '2026-09-24T16:00:00.000Z',
      energyKWh: 130_000,
      peakCost: 65_000,
      flatCost: 30_000,
      valleyCost: 10_000,
    },
  ]);

  const measured = reconcileUtilityBill(wholeBuildingBill, plantSeries);
  assert.equal(measured.mode, 'BUILDING_ALLOCATION');
  assert.equal(measured.shareSource, 'MEASURED_ENERGY_RATIO');
  assert.equal(measured.measuredChillerShareRatio, 0.325);
  assert.equal(measured.effectiveShareRatio, 0.325);
  assert.equal(measured.averageEnergyUnitPriceCnyPerKwh, 0.775);
  assert.equal(measured.blendedUnitPriceCnyPerKwh, 0.9);
  assert.equal(measured.plantEstimatedEnergyCostCny, 105_000);
  assert.equal(measured.allocatedChillerTotalCostCny, 117_000);
  assert.equal(measured.allocatedChillerEnergyChargeCny, 100_750);
  assert.equal(measured.allocatedChillerNonEnergyChargeCny, 16_250);

  // Finance override (e.g. 35%) supersedes measured ratio while preserving measured ratio for display
  const overridden = reconcileUtilityBill(
    { ...wholeBuildingBill, chillerShareOverrideRatio: 0.35 },
    plantSeries,
  );
  assert.equal(overridden.mode, 'BUILDING_ALLOCATION');
  assert.equal(overridden.shareSource, 'FINANCE_OVERRIDE');
  assert.equal(overridden.measuredChillerShareRatio, 0.325);
  assert.equal(overridden.effectiveShareRatio, 0.35);
  assert.equal(overridden.allocatedChillerTotalCostCny, 126_000);

  // Guardrail: if submeter kWh > whole-building billed kWh without finance override, refuse >100% auto-allocation
  const mismatch = reconcileUtilityBill(
    { ...wholeBuildingBill, billedEnergyKwh: 100_000, totalPayableCny: 90_000, energyChargeCny: 70_000, basicChargeCny: 20_000, powerFactorAdjustmentCny: 0, otherChargesCny: 0 },
    plantSeries,
  );
  assert.equal(mismatch.mode, 'BUILDING_ALLOCATION');
  assert.equal(mismatch.meterExceedsBuilding, true);
  assert.equal(mismatch.effectiveShareRatio, null);
  assert.equal(mismatch.allocatedChillerTotalCostCny, null);
});

test('describeBillingApiError maps gateway problem codes to plain Chinese without leaking raw English detail', () => {
  const stateConflict = new PlatformApiError({
    type: 'about:blank',
    status: 409,
    title: 'Conflict',
    detail: 'utility bill is already CONFIRMED and immutable',
    instance: '/api/v1/sites/s1/utility-bills/b1',
    code: 'UTILITY_BILL_STATE_CONFLICT',
    traceId: '0'.repeat(32),
    retryable: false,
  });
  const msg = describeBillingApiError(stateConflict, '保存失败');
  assert.ok(msg.includes('确认归档'));
  assert.ok(!msg.includes('immutable'));

  const duplicate = new PlatformApiError({
    type: 'about:blank',
    status: 409,
    title: 'Conflict',
    detail: 'duplicate key value violates unique constraint',
    instance: '/api/v1/sites/s1/utility-bills/b1/confirm',
    code: 'REGISTRY_BINDING_CONFLICT',
    traceId: '0'.repeat(32),
    retryable: false,
  });
  assert.ok(describeBillingApiError(duplicate, '保存失败').includes('已存在已确认归档'));
});

test('cross-gap electricity counts in monthly budget totals, TOU breakdown, and bill reconciliation without spiking daily cumulative curves (#798)', () => {
  const now = new Date('2026-10-15T16:00:00.000Z');
  const baseSeries = makeDailyElectricitySeries([
    {
      dateUtcStart: '2026-09-30T16:00:00.000Z',
      energyKWh: 10_000,
      peakCost: 5_000,
      flatCost: 3_000,
    },
  ]);
  const withCrossGap: EnergySeriesResponse = {
    ...baseSeries,
    crossGap: {
      energyKWh: 2_000,
      cost: {
        amountCNY: 1_600,
        unpricedKWh: 0,
        periods: [{ periodCode: 'FLAT', energyKWh: 2_000, amountCNY: 1_600 }],
      },
    },
  };

  const progress = summarizeMonthlyBudgetProgress({
    electricity: withCrossGap,
    billingMonth: '2026-10',
    budget: makeBudget(100_000, '2026-10'),
    now,
    timeZone: TIMEZONE,
  });
  assert.equal(progress.pricingState, 'PRICED');
  assert.equal(progress.cumulativeEstimatedCny, 9_600); // 8,000 daily + 1,600 cross-gap
  assert.equal(progress.dailySeries[0]?.dailyCostCny, 8_000); // daily curve stays un-spiked
  assert.equal(progress.dailySeries[0]?.cumulativeCostCny, 8_000);
  const flatPeriod = progress.touPeriods.find((item) => item.code === 'FLAT');
  assert.equal(flatPeriod?.costCny, 4_600);

  const bill = makeBill({
    meterScope: 'CHILLER_PLANT',
    billedEnergyKwh: 12_500,
    energyChargeCny: 10_000,
  });
  const reconciliation = reconcileUtilityBill(bill, withCrossGap);
  if (!('energyVarianceKwh' in reconciliation)) {
    throw new Error('Expected utility bill reconciliation for CHILLER_PLANT scope');
  }
  assert.equal(reconciliation.plantEnergyKwh, 12_000); // 10,000 + 2,000 cross-gap
  assert.equal(reconciliation.plantEstimatedEnergyCostCny, 9_600);
  assert.equal(reconciliation.energyVarianceKwh, 500);
});

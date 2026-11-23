import assert from 'node:assert/strict';
import test from 'node:test';
import {
  accountingRecalcJobSchema,
  describeAccountingRecalcError,
} from '../apps/hvac-web/src/api/accounting-recalc.ts';
import { GatewayRequestError } from '../apps/hvac-web/src/api/gateway-client.ts';
import {
  PlatformApiError,
  type EnergySeriesResponse,
  type MonthlyElectricityBudget,
  type UtilityBill,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import {
  billingMonthOptions,
  computeUtilityBillTotalPayable,
  defaultRecalcMonthRange,
  describeBillingApiError,
  formatRecalcWindowLabel,
  formatTouBreakdownSummary,
  hasTouBreakdownValues,
  normalizeBillingMonth,
  reconcileUtilityBill,
  resolveBillingMonthWindow,
  resolveReadingDateWindow,
  summarizeMonthlyBudgetProgress,
  validateRecalculateWindow,
  type BillingElectricitySeriesInput,
} from '../apps/hvac-web/src/features/billing/billing-model.ts';
import {
  buildUtilityBillInitialValues,
  buildUtilityBillMutationPayload,
  formatIsoCalendarDate,
  hasExtraTicketDetails,
  parseIsoCalendarDate,
  utilityBillFormSchema,
} from '../apps/hvac-web/src/features/billing/utility-bill-form.ts';
import type { CostPeriod } from '../apps/hvac-web/src/features/tariff/tariff-model.ts';

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
      ['DEEP_VALLEY', 0],
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

test('5-tier DEEP_VALLEY TOU breakdown formatting and budget shares include deep valley', () => {
  const now = new Date('2026-10-15T16:00:00.000Z');
  const deepValleyPeriods: readonly CostPeriod[] = [
    { periodCode: 'SUPER_PEAK', energyKWh: 100, amountCNY: 150 },
    { periodCode: 'PEAK', energyKWh: 300, amountCNY: 360 },
    { periodCode: 'FLAT', energyKWh: 250, amountCNY: 180 },
    { periodCode: 'VALLEY', energyKWh: 200, amountCNY: 64 },
    { periodCode: 'DEEP_VALLEY', energyKWh: 150, amountCNY: 27 },
  ];
  const fiveTierSeries: BillingElectricitySeriesInput = {
    schemaVersion: 1,
    points: [
      {
        periodStart: '2026-09-30T16:00:00.000Z',
        periodEnd: '2026-10-01T16:00:00.000Z',
        energyKWh: 1_000,
        cost: {
          amountCNY: 781,
          unpricedKWh: 0,
          periods: [...deepValleyPeriods],
        },
      },
    ],
    metadata: {
      requestedGranularity: 'day',
      actualGranularity: 'day',
      dataWatermark: '2026-10-15T16:00:00.000Z',
      aggregateWatermark: '2026-10-15T16:00:00.000Z',
      datasetRevision: 'local-development-v1:1',
      partial: false,
      qualitySummary: { valid: 96, suspect: 0, invalid: 0 },
    },
  };

  const progress = summarizeMonthlyBudgetProgress({
    electricity: fiveTierSeries,
    billingMonth: '2026-10',
    budget: makeBudget(10_000, '2026-10'),
    now,
    timeZone: TIMEZONE,
  });
  assert.deepEqual(
    progress.touPeriods.map((item) => [item.code, item.label, item.energyKWh, item.costCny]),
    [
      ['SUPER_PEAK', '尖峰', 100, 150],
      ['PEAK', '峰', 300, 360],
      ['FLAT', '平', 250, 180],
      ['VALLEY', '谷', 200, 64],
      ['DEEP_VALLEY', '深谷', 150, 27],
    ],
  );

  assert.equal(hasTouBreakdownValues(undefined), false);
  assert.equal(hasTouBreakdownValues({ deepValleyKwh: 1200 }), true);
  assert.equal(
    formatTouBreakdownSummary({
      sharpKwh: 100,
      peakKwh: 300,
      flatKwh: 250,
      valleyKwh: 200,
      deepValleyKwh: 150.5,
    }),
    '票面分时电量（kWh）：尖峰 100.00 · 峰 300.00 · 平 250.00 · 谷 200.00 · 深谷 150.50',
  );
  assert.equal(formatTouBreakdownSummary({}), null);
});

test('recalculation window validation, defaults, label formatting, and gateway error mapping', () => {
  const valid = validateRecalculateWindow('2026-10-01', '2026-10-15', TIMEZONE, {
    maxDate: '2026-10-16',
  });
  assert.equal(valid.ok, true);
  if (valid.ok) {
    assert.equal(valid.window.dayCount, 15);
    assert.equal(valid.window.from.toISOString(), '2026-09-30T16:00:00.000Z');
    assert.equal(valid.window.to.toISOString(), '2026-10-15T16:00:00.000Z');
  }

  const invalidOrder = validateRecalculateWindow('2026-10-20', '2026-10-15', TIMEZONE);
  assert.equal(invalidOrder.ok, false);
  if (!invalidOrder.ok) assert.equal(invalidOrder.field, 'toDate');

  const futureEnd = validateRecalculateWindow('2026-10-01', '2026-10-18', TIMEZONE, {
    maxDate: '2026-10-16',
  });
  assert.equal(futureEnd.ok, false);
  if (!futureEnd.ok) assert.ok(futureEnd.message.includes('晚于'));

  const tooLong = validateRecalculateWindow('2025-01-01', '2026-12-31', TIMEZONE);
  assert.equal(tooLong.ok, false);
  if (!tooLong.ok) assert.ok(tooLong.message.includes('366'));

  // Default ranges for past, current, and future billing months on site clock
  const now = new Date('2026-10-16T04:00:00.000Z'); // 2026-10-16 12:00 CST
  assert.deepEqual(defaultRecalcMonthRange('2026-09', now, TIMEZONE), {
    fromDate: '2026-09-01',
    toDate: '2026-09-30',
  });
  assert.deepEqual(defaultRecalcMonthRange('2026-10', now, TIMEZONE), {
    fromDate: '2026-10-01',
    toDate: '2026-10-16',
  });
  assert.deepEqual(defaultRecalcMonthRange('2026-11', now, TIMEZONE), {
    fromDate: '2026-10-16',
    toDate: '2026-10-16',
  });

  // Half-open UTC midnight windows render as inclusive local dates
  assert.equal(
    formatRecalcWindowLabel('2026-09-30T16:00:00.000Z', '2026-10-15T16:00:00.000Z', TIMEZONE),
    '2026-10-01 至 2026-10-15',
  );
  assert.equal(
    formatRecalcWindowLabel('2026-09-30T16:00:00.000Z', '2026-10-01T16:00:00.000Z', TIMEZONE),
    '2026-10-01',
  );

  // Recalc job schema & gateway error mapping
  const parsedJob = accountingRecalcJobSchema.parse({
    id: '01990000-1000-7000-8000-000000000001',
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    granularity: 'ALL',
    windowStart: '2026-09-30T16:00:00.000Z',
    windowEnd: '2026-10-15T16:00:00.000Z',
    status: 'PENDING',
    availableAt: '2026-10-16T04:00:00.000Z',
    requestedBy: '018f3e00-2000-7000-8000-000000000001',
    requestedByDisplay: '值班工程师',
    reason: '更新深谷电价后重算',
    source: 'MANUAL',
    attemptCount: 0,
    lastError: '',
    createdAt: '2026-10-16T04:00:00.000Z',
    updatedAt: '2026-10-16T04:00:00.000Z',
    finishedAt: null,
  });
  assert.equal(parsedJob.status, 'PENDING');

  const forbiddenMsg = describeAccountingRecalcError(
    new GatewayRequestError(
      {
        kind: 'refused',
        problem: { status: 403, code: 'BILLING_MANAGE_FORBIDDEN', detail: 'forbidden' },
      },
      'raw',
    ),
    'fallback',
  );
  assert.ok(forbiddenMsg.includes('权限'));
  assert.ok(!forbiddenMsg.includes('forbidden'));

  const routeMissingMsg = describeAccountingRecalcError(
    new GatewayRequestError({ kind: 'routeMissing', status: 404 }, 'raw'),
    'fallback',
  );
  assert.equal(routeMissingMsg, '此功能尚未在服务器上启用');
});

test('parseIsoCalendarDate and formatIsoCalendarDate validate leap days and reject calendar rollover', () => {
  const leap = parseIsoCalendarDate('2028-02-29');
  assert.ok(leap instanceof Date);
  assert.equal(formatIsoCalendarDate(leap), '2028-02-29');

  assert.equal(parseIsoCalendarDate('2026-02-29'), undefined);
  assert.equal(parseIsoCalendarDate('2026-04-31'), undefined);
  assert.equal(parseIsoCalendarDate('2026-10-1'), undefined);
});

test('utility-bill form defaults, schema validation, and payload builder preserve TOU, overrides, and rounding', () => {
  const createDefaults = buildUtilityBillInitialValues(null, '2026-02');
  assert.equal(createDefaults.billingMonth, '2026-02');
  assert.equal(createDefaults.readingStartDate, '2026-02-01');
  assert.equal(createDefaults.readingEndDate, '2026-02-28');
  assert.equal(createDefaults.meterScope, 'CHILLER_PLANT');
  assert.equal(hasExtraTicketDetails(null), false);

  const editSource = makeBill({
    meterScope: 'WHOLE_BUILDING',
    chillerShareOverrideRatio: 0.35,
    utilityAccountNo: '0800123456',
    standardPowerFactor: 0.9,
    actualPowerFactor: 0.95,
    touBreakdown: { sharpKwh: 40000, peakKwh: 120000, deepValleyKwh: 15000.5 },
  });
  assert.equal(hasExtraTicketDetails(editSource), true);
  assert.equal(
    hasExtraTicketDetails(
      makeBill({
        utilityAccountNo: '',
        standardPowerFactor: null,
        actualPowerFactor: null,
        touBreakdown: undefined,
      }),
    ),
    false,
  );

  const editDefaults = buildUtilityBillInitialValues(editSource, '2026-10');
  assert.equal(editDefaults.chillerShareOverridePercentInput, '35');
  assert.equal(editDefaults.touSuperPeakKwhInput, '40000');
  assert.equal(editDefaults.touDeepValleyKwhInput, '15000.5');

  const validForm = utilityBillFormSchema.safeParse({
    ...createDefaults,
    meterScope: 'WHOLE_BUILDING',
    readingStartDate: '2026-01-25',
    readingEndDate: '2026-02-24',
    billedEnergyInput: '400000',
    touSuperPeakKwhInput: '40000',
    touDeepValleyKwhInput: '1234.56',
    energyChargeInput: '310000',
    basicChargeMode: 'MAX_DEMAND_KW',
    basicChargeQuantityInput: '1800',
    basicChargeInput: '50900',
    standardPowerFactorInput: '0.90',
    actualPowerFactorInput: '0.95',
    powerFactorAdjustmentInput: '-1257',
    otherChargesInput: '357',
    chillerShareOverridePercentInput: '35',
    utilityAccountNo: ' 0800123456 ',
    invoiceNo: ' GD-202610-WB01 ',
    note: '  ',
    reason: '录入供电局月度结算电费单',
  });
  assert.equal(validForm.success, true);
  if (validForm.success) {
    const payload = buildUtilityBillMutationPayload(validForm.data, {
      expectedRevision: 2,
      idempotencyKey: 'idem-bill-1',
    });
    assert.equal(payload.totalPayableCny, 360000);
    assert.equal(payload.chillerShareOverrideRatio, 0.35);
    assert.deepEqual(payload.touBreakdown, { sharpKwh: 40000, deepValleyKwh: 1234.56 });
    assert.equal(payload.utilityAccountNo, '0800123456');
    assert.equal(payload.invoiceNo, 'GD-202610-WB01');
    assert.equal(payload.note, undefined);
    assert.equal(payload.meta.expectedRevision, 2);
  }

  const singlePart = utilityBillFormSchema.safeParse({
    ...createDefaults,
    billedEnergyInput: '10000',
    energyChargeInput: '8000',
    basicChargeMode: 'NONE',
    basicChargeQuantityInput: '',
    basicChargeInput: '0',
    powerFactorAdjustmentInput: '0',
    otherChargesInput: '0',
  });
  assert.equal(singlePart.success, true);
  if (singlePart.success) {
    const payload = buildUtilityBillMutationPayload(singlePart.data, {
      expectedRevision: 0,
      idempotencyKey: 'idem-bill-2',
    });
    assert.equal(payload.basicChargeQuantity, undefined);
    assert.equal(payload.basicChargeCny, 0);
  }

  for (const invalidCase of [
    { readingStartDate: '2026-02-20', readingEndDate: '2026-02-10' },
    { basicChargeMode: 'NONE', basicChargeInput: '100' },
    { basicChargeMode: 'MAX_DEMAND_KW', basicChargeQuantityInput: '0', basicChargeInput: '1000' },
    { meterScope: 'WHOLE_BUILDING', chillerShareOverridePercentInput: '120' },
    { energyChargeInput: '100', basicChargeInput: '0', powerFactorAdjustmentInput: '-200', otherChargesInput: '0' },
  ] as const) {
    const res = utilityBillFormSchema.safeParse({
      ...createDefaults,
      billedEnergyInput: '10000',
      energyChargeInput: '8000',
      basicChargeQuantityInput: '500',
      basicChargeInput: '1000',
      ...invalidCase,
    });
    assert.equal(res.success, false);
  }
});

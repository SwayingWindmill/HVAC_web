import {
  PlatformApiError,
  type EnergySeriesResponse,
  type MonthlyElectricityBudget,
  type UtilityBill,
  type UtilityBillBasicChargeMode,
  type UtilityBillMeterScope,
  type UtilityBillStatus,
  type UtilityBillTouBreakdown,
} from '../../api/generated/platformGateway.gen.ts';
import type { AccountingRecalcGranularity } from '../../api/accounting-recalc.ts';
import { formatDecimal, formatTime } from '../../lib/operator-format.ts';
import { siteDayKey, siteMidnight, wallTime } from '../energy-analysis/consumption/model.ts';
import { PERIOD_LABEL, PERIOD_ORDER, sumCosts, type CostInput, type PeriodCode } from '../tariff/tariff-model.ts';

const DAY_MS = 86_400_000;
const MONTH_PATTERN = /^(\d{4})-(0[1-9]|1[0-2])$/;
const DATE_PATTERN = /^(\d{4})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;

export const CANONICAL_PERIODS: readonly PeriodCode[] = PERIOD_ORDER;

export type UtilityBillTouBreakdownWithDeepValley = UtilityBillTouBreakdown & {
  readonly deepValleyKwh?: number;
};

export function hasTouBreakdownValues(
  breakdown: UtilityBillTouBreakdownWithDeepValley | null | undefined,
): boolean {
  if (!breakdown) return false;
  return (
    breakdown.sharpKwh !== undefined ||
    breakdown.peakKwh !== undefined ||
    breakdown.flatKwh !== undefined ||
    breakdown.valleyKwh !== undefined ||
    breakdown.deepValleyKwh !== undefined
  );
}

export function formatTouBreakdownSummary(
  breakdown: UtilityBillTouBreakdownWithDeepValley | null | undefined,
): string | null {
  if (!breakdown) return null;
  const entries: readonly (readonly [string, number | undefined])[] = [
    [PERIOD_LABEL.SUPER_PEAK, breakdown.sharpKwh],
    [PERIOD_LABEL.PEAK, breakdown.peakKwh],
    [PERIOD_LABEL.FLAT, breakdown.flatKwh],
    [PERIOD_LABEL.VALLEY, breakdown.valleyKwh],
    [PERIOD_LABEL.DEEP_VALLEY, breakdown.deepValleyKwh],
  ];
  const parts = entries
    .filter((entry): entry is readonly [string, number] => entry[1] !== undefined)
    .map(([label, value]) => `${label} ${formatDecimal(value, 2)}`);
  return parts.length > 0 ? `票面分时电量（kWh）：${parts.join(' · ')}` : null;
}

export const ACCOUNTING_RECALC_GRANULARITY_LABEL: Readonly<
  Record<AccountingRecalcGranularity, string>
> = {
  ALL: '全部粒度（逐时、每日与每月）',
  HOUR: '仅按小时',
  DAY: '仅按日',
  MONTH: '仅按月',
};

export const ACCOUNTING_RECALC_GRANULARITY_SHORT_LABEL: Readonly<
  Record<AccountingRecalcGranularity, string>
> = {
  ALL: '全部粒度',
  HOUR: '按小时',
  DAY: '按日',
  MONTH: '按月',
};

export const METER_SCOPE_LABEL: Readonly<Record<UtilityBillMeterScope, string>> = {
  CHILLER_PLANT: '冷站专变',
  WHOLE_BUILDING: '整栋总表',
};

export const BASIC_CHARGE_MODE_LABEL: Readonly<Record<UtilityBillBasicChargeMode, string>> = {
  NONE: '无基本电费（单一制）',
  TRANSFORMER_CAPACITY_KVA: '按变压器容量（kVA）',
  MAX_DEMAND_KW: '按最大需量（kW）',
};

export const UTILITY_BILL_STATUS_LABEL: Readonly<Record<UtilityBillStatus, string>> = {
  CONFIRMED: '○ 已确认归档',
  DRAFT: '■ 草稿待确认',
};

export function round2(value: number): number {
  return Math.round((value + Number.EPSILON) * 100) / 100;
}

export function formatBillingMonthLabel(billingMonth: string): string {
  const match = MONTH_PATTERN.exec(billingMonth);
  if (!match) return billingMonth;
  return `${Number(match[1])} 年 ${Number(match[2])} 月`;
}

export function currentSiteBillingMonth(now: Date, timeZone: string): string {
  const local = wallTime(now, timeZone);
  return `${local.year}-${String(local.month).padStart(2, '0')}`;
}

export function normalizeBillingMonth(
  raw: string | null | undefined,
  now: Date,
  timeZone: string,
): string {
  if (raw && MONTH_PATTERN.test(raw)) return raw;
  return currentSiteBillingMonth(now, timeZone);
}

/** Next month, current month, and past 11 months on the Site's calendar, newest first. */
export function billingMonthOptions(now: Date, timeZone: string): string[] {
  const local = wallTime(now, timeZone);
  return Array.from({ length: 13 }, (_, index) => {
    const offset = 1 - index; // +1 (next month), 0 (current), -1..-11
    const date = new Date(Date.UTC(local.year, local.month - 1 + offset, 1));
    return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`;
  });
}

export function shiftBillingMonth(billingMonth: string, deltaMonths: number): string {
  const match = MONTH_PATTERN.exec(billingMonth);
  if (!match) return billingMonth;
  const date = new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1 + deltaMonths, 1));
  return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`;
}

export type BillingMonthState = 'PAST' | 'CURRENT' | 'FUTURE';

export interface BillingMonthWindow {
  readonly billingMonth: string;
  readonly year: number;
  readonly month: number;
  readonly from: Date;
  readonly monthEnd: Date;
  readonly queryTo: Date;
  readonly daysInMonth: number;
  readonly elapsedDays: number;
  readonly state: BillingMonthState;
}

export function resolveBillingMonthWindow(
  billingMonth: string,
  now: Date,
  timeZone: string,
): BillingMonthWindow {
  const match = MONTH_PATTERN.exec(billingMonth);
  if (!match) {
    throw new Error(`Invalid billing month: ${billingMonth}`);
  }
  const year = Number(match[1]);
  const month = Number(match[2]);
  const from = siteMidnight(year, month, 1, timeZone);
  const monthEnd = siteMidnight(year, month + 1, 1, timeZone);
  const daysInMonth = new Date(Date.UTC(year, month, 0)).getUTCDate();

  if (now.getTime() <= from.getTime()) {
    return {
      billingMonth,
      year,
      month,
      from,
      monthEnd,
      queryTo: from,
      daysInMonth,
      elapsedDays: 0,
      state: 'FUTURE',
    };
  }
  if (now.getTime() >= monthEnd.getTime()) {
    return {
      billingMonth,
      year,
      month,
      from,
      monthEnd,
      queryTo: monthEnd,
      daysInMonth,
      elapsedDays: daysInMonth,
      state: 'PAST',
    };
  }
  const elapsedDays = Math.max(0, Math.min(daysInMonth, (now.getTime() - from.getTime()) / DAY_MS));
  return {
    billingMonth,
    year,
    month,
    from,
    monthEnd,
    queryTo: now,
    daysInMonth,
    elapsedDays,
    state: 'CURRENT',
  };
}

export type BillingPricingState = 'NO_DATA' | 'PRICED' | 'PARTIAL_UNPRICED' | 'UNPRICED';

export interface DailyBudgetPoint {
  readonly dayOfMonth: number;
  readonly dayLabel: string;
  readonly dateKey: string;
  readonly dailyCostCny: number | null;
  readonly cumulativeCostCny: number | null;
  readonly linearBudgetCny: number | null;
}

export interface TouCostShareRow {
  readonly code: PeriodCode;
  readonly label: string;
  readonly energyKWh: number;
  readonly costCny: number;
  readonly shareRatio: number;
}

export interface MonthlyBudgetProgressSummary {
  readonly window: BillingMonthWindow;
  readonly pricingState: BillingPricingState;
  readonly totalEnergyKWh: number | null;
  readonly unpricedKWh: number;
  readonly pricedPortionCny: number;
  readonly cumulativeEstimatedCny: number | null;
  readonly projectedMonthEndCny: number | null;
  readonly budgetAmountCny: number | null;
  readonly usageRatio: number | null;
  readonly projectedOverrunCny: number | null;
  readonly excludedIntervals: number;
  readonly dailySeries: readonly DailyBudgetPoint[];
  readonly touPeriods: readonly TouCostShareRow[];
}

export type BillingSeriesPointInput = Omit<EnergySeriesResponse['points'][number], 'cost'> & {
  readonly cost?: CostInput;
};

export type BillingCrossGapInput = Omit<NonNullable<EnergySeriesResponse['crossGap']>, 'cost'> & {
  readonly cost?: CostInput;
};

export type BillingElectricitySeriesInput = Omit<EnergySeriesResponse, 'points' | 'crossGap'> & {
  readonly points: readonly BillingSeriesPointInput[];
  readonly crossGap?: BillingCrossGapInput;
};

function hasSeriesElectricity(electricity: BillingElectricitySeriesInput | null): boolean {
  return Boolean(
    electricity &&
      (electricity.points.length > 0 ||
        (electricity.crossGap !== undefined && electricity.crossGap.energyKWh > 0)),
  );
}

function classifyPricingState(
  electricity: BillingElectricitySeriesInput | null,
  energyKWh: number,
  unpricedKWh: number,
): BillingPricingState {
  if (!hasSeriesElectricity(electricity)) return 'NO_DATA';
  if (unpricedKWh <= 0) return 'PRICED';
  if (energyKWh > 0 && unpricedKWh >= energyKWh) return 'UNPRICED';
  return 'PARTIAL_UNPRICED';
}

function summarizeElectricitySeries(electricity: BillingElectricitySeriesInput | null) {
  const buckets = [
    ...(electricity?.points ?? []).map((point) => ({
      electricityKWh: point.energyKWh,
      cost: point.cost ?? null,
    })),
    ...(electricity?.crossGap && electricity.crossGap.energyKWh > 0
      ? [{ electricityKWh: electricity.crossGap.energyKWh, cost: electricity.crossGap.cost ?? null }]
      : []),
  ];
  const tou = sumCosts(buckets);
  const pricingState = classifyPricingState(electricity, tou.energyKWh, tou.unpricedKWh);
  const totalEnergyKWh = hasSeriesElectricity(electricity) ? round2(tou.energyKWh) : null;
  const unpricedKWh = round2(tou.unpricedKWh);
  const pricedPortionCny = round2(tou.total);
  const estimatedCostCny = pricingState === 'PRICED' ? pricedPortionCny : null;
  const excludedIntervals = electricity
    ? electricity.metadata.qualitySummary.suspect + electricity.metadata.qualitySummary.invalid
    : 0;

  return {
    tou,
    pricingState,
    totalEnergyKWh,
    unpricedKWh,
    pricedPortionCny,
    estimatedCostCny,
    excludedIntervals,
  };
}

export function summarizeMonthlyBudgetProgress(input: {
  readonly electricity: BillingElectricitySeriesInput | null;
  readonly billingMonth: string;
  readonly budget: MonthlyElectricityBudget | null;
  readonly now: Date;
  readonly timeZone: string;
}): MonthlyBudgetProgressSummary {
  const { electricity, billingMonth, budget, now, timeZone } = input;
  const window = resolveBillingMonthWindow(billingMonth, now, timeZone);
  const {
    tou,
    pricingState,
    totalEnergyKWh,
    unpricedKWh,
    pricedPortionCny,
    estimatedCostCny: cumulativeEstimatedCny,
    excludedIntervals,
  } = summarizeElectricitySeries(electricity);

  const budgetAmountCny = budget ? budget.budgetAmountCny : null;

  let projectedMonthEndCny: number | null = null;
  if (pricingState === 'PRICED' && cumulativeEstimatedCny !== null) {
    if (window.state === 'PAST') {
      projectedMonthEndCny = cumulativeEstimatedCny;
    } else if (window.state === 'CURRENT' && window.elapsedDays >= 1) {
      const watermarkMs = electricity?.metadata.dataWatermark
        ? Math.min(Date.parse(electricity.metadata.dataWatermark), now.getTime())
        : now.getTime();
      const watermarkDays = (watermarkMs - window.from.getTime()) / DAY_MS;
      const effectiveElapsedDays =
        Number.isFinite(watermarkDays) && watermarkDays >= 1
          ? Math.min(window.elapsedDays, watermarkDays)
          : window.elapsedDays;
      projectedMonthEndCny = round2((cumulativeEstimatedCny / effectiveElapsedDays) * window.daysInMonth);
    }
  }

  const usageRatio =
    budgetAmountCny !== null && budgetAmountCny > 0 && cumulativeEstimatedCny !== null
      ? cumulativeEstimatedCny / budgetAmountCny
      : null;

  const projectedOverrunCny =
    budgetAmountCny !== null && projectedMonthEndCny !== null
      ? round2(projectedMonthEndCny - budgetAmountCny)
      : null;

  const costByDay = new Map<number, number>();
  let latestPointDay = 0;
  for (const point of electricity?.points ?? []) {
    const local = wallTime(new Date(point.periodStart), timeZone);
    if (local.year !== window.year || local.month !== window.month) continue;
    latestPointDay = Math.max(latestPointDay, local.day);
    if (point.cost && point.cost.unpricedKWh <= 0) {
      costByDay.set(local.day, round2((costByDay.get(local.day) ?? 0) + point.cost.amountCNY));
    }
  }

  const cutoffDay =
    window.state === 'FUTURE'
      ? 0
      : window.state === 'PAST'
        ? window.daysInMonth
        : Math.max(latestPointDay, Math.min(window.daysInMonth, Math.floor(window.elapsedDays)));

  let runningCumulative = 0;
  const dailySeries: DailyBudgetPoint[] = Array.from({ length: window.daysInMonth }, (_, index) => {
    const dayOfMonth = index + 1;
    const dateKey = `${billingMonth}-${String(dayOfMonth).padStart(2, '0')}`;
    const dayLabel = `${window.month}/${dayOfMonth}`;
    const linearBudgetCny =
      budgetAmountCny !== null ? round2((budgetAmountCny * dayOfMonth) / window.daysInMonth) : null;

    if (pricingState !== 'PRICED' || dayOfMonth > cutoffDay) {
      return {
        dayOfMonth,
        dayLabel,
        dateKey,
        dailyCostCny: null,
        cumulativeCostCny: null,
        linearBudgetCny,
      };
    }

    const dailyCost = costByDay.get(dayOfMonth) ?? 0;
    runningCumulative = round2(runningCumulative + dailyCost);
    return {
      dayOfMonth,
      dayLabel,
      dateKey,
      dailyCostCny: costByDay.has(dayOfMonth) ? dailyCost : 0,
      cumulativeCostCny: runningCumulative,
      linearBudgetCny,
    };
  });

  const periodMap = new Map(tou.byPeriod.map((entry) => [entry.code, entry]));
  const touPeriods: TouCostShareRow[] = CANONICAL_PERIODS.map((code) => {
    const entry = periodMap.get(code);
    const costCny = entry ? round2(entry.cost) : 0;
    const energyKWh = entry ? round2(entry.energyKWh) : 0;
    return {
      code,
      label: PERIOD_LABEL[code],
      energyKWh,
      costCny,
      shareRatio: tou.total > 0 ? costCny / tou.total : 0,
    };
  });

  return {
    window,
    pricingState,
    totalEnergyKWh,
    unpricedKWh,
    pricedPortionCny,
    cumulativeEstimatedCny,
    projectedMonthEndCny,
    budgetAmountCny,
    usageRatio,
    projectedOverrunCny,
    excludedIntervals,
    dailySeries,
    touPeriods,
  };
}

export interface ReadingDateWindow {
  readonly from: Date;
  readonly to: Date;
  readonly dayCount: number;
}

function parseCalendarDate(value: string): { year: number; month: number; day: number } | null {
  const match = DATE_PATTERN.exec(value);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const check = new Date(Date.UTC(year, month - 1, day));
  if (
    check.getUTCFullYear() !== year ||
    check.getUTCMonth() + 1 !== month ||
    check.getUTCDate() !== day
  ) {
    return null;
  }
  return { year, month, day };
}

/** Inclusive Site-local dates [startDate 00:00, endDate+1 00:00), e.g. 09-25..10-24 = 30 days. */
export function resolveReadingDateWindow(
  readingStartDate: string,
  readingEndDate: string,
  timeZone: string,
): ReadingDateWindow | null {
  const start = parseCalendarDate(readingStartDate);
  const end = parseCalendarDate(readingEndDate);
  if (!start || !end) return null;
  const startUtcDays = Date.UTC(start.year, start.month - 1, start.day) / DAY_MS;
  const endUtcDays = Date.UTC(end.year, end.month - 1, end.day) / DAY_MS;
  if (endUtcDays < startUtcDays) return null;
  const dayCount = Math.round(endUtcDays - startUtcDays) + 1;
  const from = siteMidnight(start.year, start.month, start.day, timeZone);
  const to = siteMidnight(end.year, end.month, end.day + 1, timeZone);
  return { from, to, dayCount };
}

export type RecalculateWindowValidationResult =
  | { readonly ok: true; readonly window: ReadingDateWindow }
  | { readonly ok: false; readonly field: 'fromDate' | 'toDate'; readonly message: string };

export function validateRecalculateWindow(
  fromDate: string,
  toDate: string,
  timeZone: string,
  options: { readonly maxDate?: string; readonly maxDays?: number } = {},
): RecalculateWindowValidationResult {
  const start = parseCalendarDate(fromDate);
  if (!start) {
    return { ok: false, field: 'fromDate', message: '请输入有效的开始日期（YYYY-MM-DD）' };
  }
  const end = parseCalendarDate(toDate);
  if (!end) {
    return { ok: false, field: 'toDate', message: '请输入有效的结束日期（YYYY-MM-DD）' };
  }
  if (options.maxDate && parseCalendarDate(options.maxDate) && toDate > options.maxDate) {
    return { ok: false, field: 'toDate', message: '重算结束日期不能晚于站点当前日期' };
  }
  const window = resolveReadingDateWindow(fromDate, toDate, timeZone);
  if (!window) {
    return { ok: false, field: 'toDate', message: '重算结束日期不能早于开始日期' };
  }
  const maxDays = options.maxDays ?? 366;
  if (window.dayCount > maxDays) {
    return { ok: false, field: 'toDate', message: `单次重算日期范围不能超过 ${maxDays} 天` };
  }
  return { ok: true, window };
}

export function defaultRecalcMonthRange(
  billingMonth: string,
  now: Date,
  timeZone: string,
): { readonly fromDate: string; readonly toDate: string } {
  const todayKey = siteDayKey(now, timeZone);
  const match = MONTH_PATTERN.exec(billingMonth);
  if (!match) {
    return { fromDate: todayKey, toDate: todayKey };
  }
  const year = Number(match[1]);
  const month = Number(match[2]);
  const days = new Date(Date.UTC(year, month, 0)).getUTCDate();
  const fromDate = `${billingMonth}-01`;
  const monthEnd = `${billingMonth}-${String(days).padStart(2, '0')}`;
  if (fromDate > todayKey) {
    return { fromDate: todayKey, toDate: todayKey };
  }
  return { fromDate, toDate: monthEnd > todayKey ? todayKey : monthEnd };
}

export function formatRecalcWindowLabel(
  windowStart: string,
  windowEnd: string,
  timeZone: string,
): string {
  if (parseCalendarDate(windowStart) && parseCalendarDate(windowEnd)) {
    return windowStart === windowEnd ? windowStart : `${windowStart} 至 ${windowEnd}`;
  }
  const startMs = Date.parse(windowStart);
  const endMs = Date.parse(windowEnd);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) {
    return `${windowStart} 至 ${windowEnd}`;
  }
  const startLocal = wallTime(new Date(startMs), timeZone);
  const endLocal = wallTime(new Date(endMs), timeZone);
  if (
    startLocal.hour === 0 &&
    startLocal.minute === 0 &&
    endLocal.hour === 0 &&
    endLocal.minute === 0 &&
    endMs > startMs
  ) {
    const startDay = siteDayKey(new Date(startMs), timeZone);
    const endDay = siteDayKey(new Date(endMs - 1), timeZone);
    return startDay === endDay ? startDay : `${startDay} 至 ${endDay}`;
  }
  return `${formatTime(windowStart, timeZone)} 至 ${formatTime(windowEnd, timeZone)}`;
}

export function computeUtilityBillTotalPayable(parts: {
  readonly energyChargeCny: number;
  readonly basicChargeCny: number;
  readonly powerFactorAdjustmentCny: number;
  readonly otherChargesCny: number;
}): number {
  const toCents = (amount: number) => Math.round((Number.isFinite(amount) ? amount : 0) * 100);
  const totalCents =
    toCents(parts.energyChargeCny) +
    toCents(parts.basicChargeCny) +
    toCents(parts.powerFactorAdjustmentCny) +
    toCents(parts.otherChargesCny);
  return totalCents / 100;
}

interface UtilityBillReconciliationBase {
  readonly bill: UtilityBill;
  readonly nonEnergyChargeCny: number;
  readonly nonEnergyShareRatio: number | null;
  readonly plantPricingState: BillingPricingState;
  readonly plantEnergyKwh: number | null;
  readonly plantUnpricedKwh: number;
  readonly plantEstimatedEnergyCostCny: number | null;
  readonly excludedIntervals: number;
}

export interface DirectVarianceReconciliation extends UtilityBillReconciliationBase {
  readonly mode: 'DIRECT_VARIANCE';
  readonly energyVarianceKwh: number | null;
  readonly energyVarianceRatio: number | null;
  readonly energyChargeVarianceCny: number | null;
  readonly energyChargeVarianceRatio: number | null;
}

export interface BuildingAllocationReconciliation extends UtilityBillReconciliationBase {
  readonly mode: 'BUILDING_ALLOCATION';
  readonly shareSource: 'FINANCE_OVERRIDE' | 'MEASURED_ENERGY_RATIO';
  readonly measuredChillerShareRatio: number | null;
  readonly meterExceedsBuilding: boolean;
  readonly effectiveShareRatio: number | null;
  readonly averageEnergyUnitPriceCnyPerKwh: number | null;
  readonly blendedUnitPriceCnyPerKwh: number | null;
  readonly allocatedChillerTotalCostCny: number | null;
  readonly allocatedChillerEnergyChargeCny: number | null;
  readonly allocatedChillerNonEnergyChargeCny: number | null;
}

export type UtilityBillReconciliation =
  | DirectVarianceReconciliation
  | BuildingAllocationReconciliation;

export function reconcileUtilityBill(
  bill: UtilityBill,
  windowElectricity: BillingElectricitySeriesInput | null,
): UtilityBillReconciliation {
  const nonEnergyCents =
    Math.round(bill.basicChargeCny * 100) +
    Math.round(bill.powerFactorAdjustmentCny * 100) +
    Math.round(bill.otherChargesCny * 100);
  const nonEnergyChargeCny = nonEnergyCents / 100;
  const nonEnergyShareRatio =
    bill.totalPayableCny > 0 ? nonEnergyChargeCny / bill.totalPayableCny : null;

  const {
    pricingState: plantPricingState,
    totalEnergyKWh: plantEnergyKwh,
    unpricedKWh: plantUnpricedKwh,
    estimatedCostCny: plantEstimatedEnergyCostCny,
    excludedIntervals,
  } = summarizeElectricitySeries(windowElectricity);

  const base: UtilityBillReconciliationBase = {
    bill,
    nonEnergyChargeCny,
    nonEnergyShareRatio,
    plantPricingState,
    plantEnergyKwh,
    plantUnpricedKwh,
    plantEstimatedEnergyCostCny,
    excludedIntervals,
  };

  if (bill.meterScope === 'CHILLER_PLANT') {
    const energyVarianceKwh =
      plantEnergyKwh !== null ? round2(bill.billedEnergyKwh - plantEnergyKwh) : null;
    const energyVarianceRatio =
      plantEnergyKwh !== null && plantEnergyKwh > 0
        ? (bill.billedEnergyKwh - plantEnergyKwh) / plantEnergyKwh
        : null;
    const energyChargeVarianceCny =
      plantEstimatedEnergyCostCny !== null
        ? round2(bill.energyChargeCny - plantEstimatedEnergyCostCny)
        : null;
    const energyChargeVarianceRatio =
      plantEstimatedEnergyCostCny !== null && plantEstimatedEnergyCostCny > 0
        ? (bill.energyChargeCny - plantEstimatedEnergyCostCny) / plantEstimatedEnergyCostCny
        : null;

    return {
      ...base,
      mode: 'DIRECT_VARIANCE',
      energyVarianceKwh,
      energyVarianceRatio,
      energyChargeVarianceCny,
      energyChargeVarianceRatio,
    };
  }

  const rawMeasuredRatio =
    plantEnergyKwh !== null && bill.billedEnergyKwh > 0
      ? plantEnergyKwh / bill.billedEnergyKwh
      : null;
  const meterExceedsBuilding = rawMeasuredRatio !== null && rawMeasuredRatio > 1;
  const measuredChillerShareRatio =
    rawMeasuredRatio !== null && !meterExceedsBuilding ? rawMeasuredRatio : null;

  const shareSource =
    bill.chillerShareOverrideRatio !== null ? 'FINANCE_OVERRIDE' : 'MEASURED_ENERGY_RATIO';
  const effectiveShareRatio =
    bill.chillerShareOverrideRatio !== null
      ? bill.chillerShareOverrideRatio
      : measuredChillerShareRatio;
  const averageEnergyUnitPriceCnyPerKwh =
    bill.billedEnergyKwh > 0 ? bill.energyChargeCny / bill.billedEnergyKwh : null;
  const blendedUnitPriceCnyPerKwh =
    bill.billedEnergyKwh > 0 ? bill.totalPayableCny / bill.billedEnergyKwh : null;

  const allocatedChillerTotalCostCny =
    effectiveShareRatio !== null ? round2(bill.totalPayableCny * effectiveShareRatio) : null;
  const allocatedChillerEnergyChargeCny =
    effectiveShareRatio !== null ? round2(bill.energyChargeCny * effectiveShareRatio) : null;
  const allocatedChillerNonEnergyChargeCny =
    allocatedChillerTotalCostCny !== null && allocatedChillerEnergyChargeCny !== null
      ? round2(allocatedChillerTotalCostCny - allocatedChillerEnergyChargeCny)
      : null;

  return {
    ...base,
    mode: 'BUILDING_ALLOCATION',
    shareSource,
    measuredChillerShareRatio,
    meterExceedsBuilding,
    effectiveShareRatio,
    averageEnergyUnitPriceCnyPerKwh,
    blendedUnitPriceCnyPerKwh,
    allocatedChillerTotalCostCny,
    allocatedChillerEnergyChargeCny,
    allocatedChillerNonEnergyChargeCny,
  };
}

export function describeBillingApiError(error: unknown, fallback: string): string {
  if (error instanceof PlatformApiError) {
    const { status, code } = error.problem;
    if (status === 403 || code === 'BILLING_MANAGE_FORBIDDEN') {
      return '当前账号没有维护月度电费预算或结算电费单的权限，请联系站点管理员';
    }
    if (code === 'ANALYTICS_ACCESS_DENIED') {
      return '当前账号没有查看该站点能耗与估算电费的权限';
    }
    if (status === 404 || code === 'RESOURCE_NOT_FOUND') {
      return '未找到对应的结算电费单或站点记录，请刷新页面后重试';
    }
    if (status === 409) {
      if (code === 'UTILITY_BILL_STATE_CONFLICT') {
        return '该结算电费单已确认归档，不能再次修改或重复归档';
      }
      if (code === 'REGISTRY_BINDING_CONFLICT') {
        return '同一账期与同一表计范围已存在已确认归档的结算电费单，不能重复确认归档';
      }
      if (code === 'REGISTRY_REVISION_CONFLICT') {
        return '该记录已被其他成员更新，请刷新页面核对最新内容后再提交';
      }
      if (code === 'REGISTRY_IDEMPOTENCY_CONFLICT') {
        return '请勿重复提交不同的账单变更，请刷新页面后重试';
      }
      return '当前记录状态不允许执行此操作，请刷新页面后重试';
    }
    if (status === 400 || code === 'REGISTRY_MUTATION_INVALID') {
      return '填写的账期、抄表起止日期或电费构成金额不符合校验规则，请核对后重试';
    }
    return fallback;
  }
  return fallback;
}

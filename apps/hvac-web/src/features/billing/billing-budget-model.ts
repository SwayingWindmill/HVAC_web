import type {
  EnergySeriesResponse,
  MonthlyElectricityBudget,
  UtilityBillBasicChargeMode,
  UtilityBillMeterScope,
  UtilityBillStatus,
  UtilityBillTouBreakdown,
} from '../../api/generated/platformGateway.gen.ts';
import type { AccountingRecalcGranularity } from '../../api/accounting-recalc.ts';
import { formatDecimal } from '../../lib/operator-format.ts';
import { siteMidnight, wallTime } from '../../lib/site-time.ts';
import { PERIOD_LABEL, PERIOD_ORDER, sumCosts, type CostInput, type PeriodCode } from '../tariff/tariff-model.ts';

export const DAY_MS = 86_400_000;
export const MONTH_PATTERN = /^(\d{4})-(0[1-9]|1[0-2])$/;

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

export function summarizeElectricitySeries(electricity: BillingElectricitySeriesInput | null) {
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

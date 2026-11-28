import { PlatformApiError, type UtilityBill } from '../../api/generated/platformGateway.gen.ts';
import { formatTime } from '../../lib/operator-format.ts';
import { siteMidnight, wallTime } from '../../lib/site-time.ts';
import { siteDayKey } from '../energy-analysis/consumption/model.ts';
import {
  DAY_MS,
  MONTH_PATTERN,
  round2,
  summarizeElectricitySeries,
  type BillingElectricitySeriesInput,
  type BillingPricingState,
} from './billing-budget-model.ts';

export {
  ACCOUNTING_RECALC_GRANULARITY_LABEL,
  ACCOUNTING_RECALC_GRANULARITY_SHORT_LABEL,
  BASIC_CHARGE_MODE_LABEL,
  CANONICAL_PERIODS,
  METER_SCOPE_LABEL,
  UTILITY_BILL_STATUS_LABEL,
  billingMonthOptions,
  currentSiteBillingMonth,
  formatBillingMonthLabel,
  formatTouBreakdownSummary,
  hasTouBreakdownValues,
  normalizeBillingMonth,
  resolveBillingMonthWindow,
  round2,
  shiftBillingMonth,
  summarizeMonthlyBudgetProgress,
  type BillingCrossGapInput,
  type BillingElectricitySeriesInput,
  type BillingMonthState,
  type BillingMonthWindow,
  type BillingPricingState,
  type BillingSeriesPointInput,
  type DailyBudgetPoint,
  type MonthlyBudgetProgressSummary,
  type TouCostShareRow,
  type UtilityBillTouBreakdownWithDeepValley,
} from './billing-budget-model.ts';

const DATE_PATTERN = /^(\d{4})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;

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

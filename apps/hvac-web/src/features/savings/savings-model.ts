import { wallTime } from '../energy-analysis/consumption/model.ts';

// Savings verification (ADR 0021): a Site's energy conservation measures, each with a baseline
// frozen at implementation and the days verified against it. mv-service owns all of it; the page
// only reads it and asks the owner to register or move a measure along.

export type MeasureKind = 'SETPOINT_RESET' | 'CONTROL_SEQUENCE' | 'EQUIPMENT_RETROFIT' | 'OPERATING_SCHEDULE' | 'OTHER';
export type MeasureStatus = 'PLANNED' | 'IMPLEMENTED' | 'ENDED';

/** The fitted line energy = interceptKWh + slope × cooling, with how well it fits. */
export interface BaselineModel {
  readonly interceptKWh: number;
  readonly slope: number;
  readonly cvRmse: number;
  readonly nmbe: number;
}

export interface Baseline {
  readonly from: string;
  readonly to: string;
  readonly samples: number;
  readonly expectedHours: number;
  /** Absent when the period held too little data to fit a line. */
  readonly model: BaselineModel | null;
  readonly trusted: boolean;
  readonly reason?: BaselineReason;
}

export type BaselineReason = 'TOO_FEW_HOURS' | 'LOW_COVERAGE' | 'POOR_FIT';

/** Why a baseline is not trusted, in the operator's words. */
export const REASON_TEXT: Record<BaselineReason, string> = {
  TOO_FEW_HOURS: `基线期完整小时不足 72 小时`,
  LOW_COVERAGE: '基线期数据覆盖率低于 90%',
  POOR_FIT: '模型未通过 ASHRAE Guideline 14 检验',
};

export interface SavingsTotals {
  readonly days: number;
  /** Hours verified without an energy rate; their avoided cost is not counted. */
  readonly unpricedHours: number;
  readonly adjustedBaselineKWh: number;
  readonly actualKWh: number;
  readonly avoidedKWh: number;
  readonly avoidedCost: number;
}

export interface VerifiedDay {
  readonly day: string;
  readonly expectedHours: number;
  readonly hours: number;
  readonly unpricedHours: number;
  readonly adjustedBaselineKWh: number;
  readonly actualKWh: number;
  readonly avoidedKWh: number;
  readonly avoidedCost: number;
}

export interface Measure {
  readonly id: string;
  readonly siteId: string;
  readonly name: string;
  readonly kind: MeasureKind;
  readonly description: string;
  readonly evidence: string;
  /** The executed command the measure came from, when it came from one. */
  readonly evidenceCommandId: string | null;
  readonly status: MeasureStatus;
  readonly plannedAt: string;
  readonly implementedAt: string | null;
  readonly endedAt: string | null;
  readonly investmentCny: number | null;
  readonly expectedAnnualKWh: number | null;
  readonly baseline: Baseline | null;
  readonly totals: SavingsTotals;
  readonly days?: readonly VerifiedDay[];
  readonly createdAt: string;
  readonly updatedAt: string;
}

export interface SavingsSummary {
  readonly from: string;
  readonly to: string;
  readonly totals: SavingsTotals;
}

export const KIND_LABEL: Record<MeasureKind, string> = {
  SETPOINT_RESET: '设定值优化',
  CONTROL_SEQUENCE: '控制策略',
  EQUIPMENT_RETROFIT: '设备改造',
  OPERATING_SCHEDULE: '运行时间表',
  OTHER: '其他',
};

export const STATUS_LABEL: Record<MeasureStatus, string> = { PLANNED: '计划中', IMPLEMENTED: '已实施', ENDED: '已结束' };

// ASHRAE Guideline 14 for hourly models, and the data a baseline needs before it is fitted.
export const LIMITS = { cvRmse: 0.3, nmbe: 0.1, minimumHours: 72, minimumCoverage: 0.9 } as const;

export interface FitCheck {
  readonly label: string;
  readonly value: string;
  readonly limit: string;
  /** Null when there is no model to judge. */
  readonly pass: boolean | null;
}

const percent = (value: number, digits = 1) => `${(value * 100).toFixed(digits)}%`;

/** The three checks a frozen baseline is judged by, in the order an engineer reads them. */
export function fitChecks(baseline: Pick<Baseline, 'samples' | 'expectedHours' | 'model'>): readonly FitCheck[] {
  const coverage = baseline.expectedHours > 0 ? baseline.samples / baseline.expectedHours : 0;
  const model = baseline.model;
  return [
    {
      label: '有效小时',
      value: `${baseline.samples} / ${baseline.expectedHours}（${percent(coverage, 0)}）`,
      limit: `≥ ${LIMITS.minimumHours} 小时且覆盖 ≥ ${percent(LIMITS.minimumCoverage, 0)}`,
      pass: baseline.samples >= LIMITS.minimumHours && coverage >= LIMITS.minimumCoverage,
    },
    {
      label: 'CV(RMSE)', value: model ? percent(model.cvRmse) : '未拟合', limit: `≤ ${percent(LIMITS.cvRmse, 0)}`,
      pass: model ? model.cvRmse <= LIMITS.cvRmse : null,
    },
    {
      label: 'NMBE', value: model ? percent(model.nmbe) : '未拟合', limit: `±${percent(LIMITS.nmbe, 0)} 以内`,
      pass: model ? Math.abs(model.nmbe) <= LIMITS.nmbe : null,
    },
  ];
}

export type MeasureAction = 'implement' | 'refit' | 'end';

/** What can still be done to a measure: only a baseline that failed is refitted, and only before any day is published. */
export function measureActions(measure: Pick<Measure, 'status' | 'totals' | 'baseline'>): readonly MeasureAction[] {
  switch (measure.status) {
    case 'PLANNED':
      return ['implement'];
    case 'IMPLEMENTED':
      return measure.totals.days === 0 && !measure.baseline?.trusted ? ['refit', 'end'] : ['end'];
    case 'ENDED':
      return [];
  }
}

export interface DayRow {
  readonly day: string;
  readonly label: string;
  readonly adjusted: number;
  readonly actual: number;
  readonly avoided: number;
  readonly coverage: number;
}

/** Verified days oldest first, as the chart and the day table read them. */
export function dayRows(days: readonly VerifiedDay[]): readonly DayRow[] {
  return [...days].sort((left, right) => left.day.localeCompare(right.day)).map((day) => ({
    day: day.day,
    label: `${Number(day.day.slice(5, 7))}/${Number(day.day.slice(8, 10))}`,
    adjusted: day.adjustedBaselineKWh,
    actual: day.actualKWh,
    avoided: day.avoidedKWh,
    coverage: day.expectedHours > 0 ? day.hours / day.expectedHours : 0,
  }));
}

/** How the avoided cost was priced: energy rates only, and which hours had none. */
export function costBasis(totals: Pick<SavingsTotals, 'unpricedHours'>): string {
  return totals.unpricedHours > 0
    ? `${totals.unpricedHours} 小时没有电价未计入；不含需量电费`
    : '按逐时分时电度电价，不含需量电费';
}

/** Avoided energy as a share of what the plant would have used without the measure. */
export function savingsRate(totals: SavingsTotals): number | null {
  return totals.adjustedBaselineKWh > 0 ? totals.avoidedKWh / totals.adjustedBaselineKWh : null;
}

const pad = (value: number) => String(value).padStart(2, '0');

/** The Site-local month so far, as the YYYY-MM-DD bounds the summary takes. */
export function monthToDate(now: Date, timeZone: string): { readonly from: string; readonly to: string } {
  const today = wallTime(now, timeZone);
  return { from: `${today.year}-${pad(today.month)}-01`, to: `${today.year}-${pad(today.month)}-${pad(today.day)}` };
}

/** An instant as a datetime-local value on the Site's clock. */
export function siteInputValue(instant: Date, timeZone: string): string {
  const shown = wallTime(instant, timeZone);
  return `${shown.year}-${pad(shown.month)}-${pad(shown.day)}T${pad(shown.hour)}:${pad(shown.minute)}`;
}

/** A datetime-local value read on the Site's clock, whatever the browser's own zone. */
export function instantFromSiteInput(value: string, timeZone: string): Date {
  const [date, time] = value.split('T');
  const [year, month, day] = date.split('-').map(Number);
  const [hour, minute] = time.split(':').map(Number);
  const wall = Date.UTC(year, month - 1, day, hour, minute);
  // The zone's offset is read at the instant first guessed, then at the one it gives, so a time
  // after a daylight-saving change takes the offset in force then.
  const offsetAt = (instant: number) => {
    const shown = wallTime(new Date(instant), timeZone);
    return Date.UTC(shown.year, shown.month - 1, shown.day, shown.hour, shown.minute) - instant;
  };
  const first = wall - offsetAt(wall);
  return new Date(wall - offsetAt(first));
}

import type { Alarm, AlarmSeverity } from '../alarms/alarm-api';
import type { WorkOrder } from '../../api/work-orders';
import type { Finding } from '../diagnostics/diagnostics-model';
import { siteMidnight, wallTime } from '../energy-analysis/consumption/model.ts';

// A Site's monthly report. A closed month reads from its snapshot, kept by report-service once the
// month was over (#483); the running month, and a closed month without one, are assembled on demand
// from the owners' records. Demand, alarms and work orders are never in a snapshot.

export interface ReportWindow {
  readonly from: Date;
  /** The month's end, or now while the month runs. */
  readonly to: Date;
  readonly complete: boolean;
  readonly previousFrom: Date;
  /** The whole month before, or its same span while this month runs. */
  readonly previousTo: Date;
  /** Calendar days in the month. */
  readonly days: number;
}

/** `month` is YYYY-MM on the Site's calendar. */
export function reportWindow(month: string, now: Date, timeZone: string): ReportWindow {
  const [year, number] = month.split('-').map(Number);
  const from = siteMidnight(year, number, 1, timeZone);
  const end = siteMidnight(year, number + 1, 1, timeZone);
  const previousFrom = siteMidnight(year, number - 1, 1, timeZone);
  const complete = end.getTime() <= now.getTime();
  const to = complete ? end : now;
  return {
    from,
    to,
    complete,
    previousFrom,
    previousTo: complete ? from : new Date(previousFrom.getTime() + (to.getTime() - from.getTime())),
    days: new Date(Date.UTC(year, number, 0)).getUTCDate(),
  };
}

/** The current Site month and the eleven before it, newest first, as YYYY-MM. */
export function recentMonths(now: Date, timeZone: string): string[] {
  const today = wallTime(now, timeZone);
  return Array.from({ length: 12 }, (_, index) => {
    const date = new Date(Date.UTC(today.year, today.month - 1 - index, 1));
    return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`;
  });
}

const within = (instant: string, from: Date, to: Date) => {
  const at = Date.parse(instant);
  return at >= from.getTime() && at < to.getTime();
};

export interface AlarmSummary {
  readonly raised: number;
  readonly cleared: number;
  readonly meanMinutesToClear: number | null;
  readonly bySeverity: Readonly<Record<AlarmSeverity, number>>;
}

/** Alarms raised in the window, by the severity they carry now. */
export function alarmSummary(alarms: readonly Pick<Alarm, 'firstOccurredAt' | 'currentSeverity' | 'clearedAt'>[], from: Date, to: Date): AlarmSummary {
  const raised = alarms.filter((alarm) => within(alarm.firstOccurredAt, from, to));
  const cleared = raised.filter((alarm) => alarm.clearedAt);
  const bySeverity: Record<AlarmSeverity, number> = { CRITICAL: 0, MAJOR: 0, MINOR: 0, WARNING: 0, INFO: 0 };
  for (const alarm of raised) bySeverity[alarm.currentSeverity] += 1;
  const minutes = cleared.map((alarm) => (Date.parse(alarm.clearedAt!) - Date.parse(alarm.firstOccurredAt)) / 60_000);
  return {
    raised: raised.length,
    cleared: cleared.length,
    meanMinutesToClear: minutes.length === 0 ? null : minutes.reduce((sum, value) => sum + value, 0) / minutes.length,
    bySeverity,
  };
}

/** Orders opened in the window, orders completed in it, and how many of the opened are still open. */
export function workOrderSummary(workOrders: readonly Pick<WorkOrder, 'createdAt' | 'status' | 'timeline'>[], from: Date, to: Date) {
  const opened = workOrders.filter((order) => within(order.createdAt, from, to));
  return {
    opened: opened.length,
    completed: workOrders.filter((order) => order.timeline.some((event) => event.operation === 'COMPLETE' && within(event.occurredAt, from, to))).length,
    stillOpen: opened.filter((order) => order.status !== 'COMPLETED' && order.status !== 'CANCELLED').length,
  };
}

export function findingSummary(findings: readonly Pick<Finding, 'evaluationTo' | 'findingType'>[], from: Date, to: Date) {
  const inMonth = findings.filter((finding) => within(finding.evaluationTo, from, to));
  const counts = new Map<string, number>();
  for (const finding of inMonth) counts.set(finding.findingType, (counts.get(finding.findingType) ?? 0) + 1);
  return {
    count: inMonth.length,
    byType: [...counts.entries()].sort(([, left], [, right]) => right - left).map(([findingType, count]) => ({ findingType, count })),
  };
}

export interface ReportSnapshot {
  readonly schemaVersion: 1;
  readonly month: string;
  readonly timezone: string;
  readonly daysInMonth: number;
  readonly daysWithData: number;
  readonly electricityKWh: number;
  readonly coolingKWh: number;
  readonly cop: number | null;
  /** Time-of-use energy cost; null without an import Tariff or with an hour no period priced. */
  readonly energyCostCNY: number | null;
  /** Why the cost is there or not: priced, no import Tariff, or hours no period priced. */
  readonly energyCostStatus: 'PRICED' | 'NO_TARIFF' | 'UNPRICED_HOURS';
  readonly tariffPeriods: readonly { readonly code: string; readonly electricityKWh: number; readonly costCNY: number }[];
  readonly days: readonly { readonly date: string; readonly electricityKWh: number; readonly coolingKWh: number; readonly cop: number | null; readonly energyCostCNY: number | null }[];
  readonly findings: { readonly count: number; readonly byType: readonly { readonly findingType: string; readonly count: number }[] };
  /** Every measure's verified days; days and the plant's electricity count each day once. */
  readonly savings: { readonly days: number; readonly adjustedBaselineKWh: number; readonly avoidedKWh: number; readonly avoidedCost: number; readonly unpricedHours: number; readonly plantKWh: number };
}

/** One kept version of a month's snapshot. */
export interface StoredReport {
  readonly id: string;
  readonly month: string;
  readonly version: number;
  readonly generatedAt: string;
  readonly report: ReportSnapshot;
}

const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
const dayLabel = (date: string) => {
  const [year, month, day] = date.split('-').map(Number);
  return `${month}/${day} ${WEEKDAYS[new Date(Date.UTC(year, month - 1, day)).getUTCDay()]}`;
};

/** Every day of the snapshot's month on the chart axis, with gaps for days without metering. */
export function snapshotDays(report: ReportSnapshot): { label: string; electricityKWh: number | null; cop: number | null }[] {
  const byDate = new Map(report.days.map((day) => [day.date, day]));
  const monthNumber = Number(report.month.slice(5, 7));
  return Array.from({ length: report.daysInMonth }, (_, index) => {
    const day = byDate.get(`${report.month}-${String(index + 1).padStart(2, '0')}`);
    return {
      label: `${monthNumber}/${index + 1}`,
      electricityKWh: day ? Math.round(day.electricityKWh) : null,
      cop: day?.cop == null ? null : Number(day.cop.toFixed(2)),
    };
  });
}

export function snapshotPeak(report: ReportSnapshot): { label: string; electricityKWh: number } | null {
  const peak = report.days.reduce<ReportSnapshot['days'][number] | null>((best, day) => (!best || day.electricityKWh > best.electricityKWh ? day : best), null);
  return peak ? { label: dayLabel(peak.date), electricityKWh: peak.electricityKWh } : null;
}

/** January to the Site's current month, or all twelve once the year is over, as YYYY-MM. */
export function yearMonths(year: number, now: Date, timeZone: string): string[] {
  const today = wallTime(now, timeZone);
  const last = year < today.year ? 12 : year === today.year ? today.month : 0;
  return Array.from({ length: last }, (_, index) => `${year}-${String(index + 1).padStart(2, '0')}`);
}

export interface LiveMonth {
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  readonly energyCost: number | null;
}

export interface YearRow {
  readonly month: string;
  /** Where the month's figures come from: its latest snapshot, the live series, or nowhere. */
  readonly source: 'snapshot' | 'live' | 'none';
  readonly version: number | null;
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  readonly cop: number | null;
  readonly energyCost: number | null;
  /** Days with metering, known only from a snapshot. */
  readonly daysWithData: number | null;
  readonly daysInMonth: number;
}

const daysIn = (month: string) => new Date(Date.UTC(Number(month.slice(0, 4)), Number(month.slice(5, 7)), 0)).getUTCDate();
const ratioOf = (cooling: number | null, electricity: number | null) => (cooling === null || electricity === null || electricity <= 0 ? null : cooling / electricity);

export function yearRows(months: readonly string[], snapshots: readonly StoredReport[], live: ReadonlyMap<string, LiveMonth>): YearRow[] {
  const bySnapshot = new Map(snapshots.map((stored) => [stored.month, stored]));
  return months.map((month): YearRow => {
    const stored = bySnapshot.get(month);
    if (stored) {
      const report = stored.report;
      return {
        month, source: 'snapshot', version: stored.version, electricityKWh: report.electricityKWh, coolingKWh: report.coolingKWh,
        cop: report.cop, energyCost: report.energyCostCNY, daysWithData: report.daysWithData, daysInMonth: report.daysInMonth,
      };
    }
    const figures = live.get(month);
    if (!figures || (figures.electricityKWh === null && figures.coolingKWh === null)) {
      return { month, source: 'none', version: null, electricityKWh: null, coolingKWh: null, cop: null, energyCost: null, daysWithData: null, daysInMonth: daysIn(month) };
    }
    return {
      month, source: 'live', version: null, electricityKWh: figures.electricityKWh, coolingKWh: figures.coolingKWh,
      cop: ratioOf(figures.coolingKWh, figures.electricityKWh), energyCost: figures.energyCost, daysWithData: null, daysInMonth: daysIn(month),
    };
  });
}

/** The year's totals; its cost only when every month with energy has one. */
export function yearTotals(rows: readonly YearRow[]): { electricityKWh: number | null; coolingKWh: number | null; cop: number | null; energyCost: number | null } {
  const withData = rows.filter((row) => row.source !== 'none');
  if (withData.length === 0) return { electricityKWh: null, coolingKWh: null, cop: null, energyCost: null };
  const electricityKWh = withData.reduce((sum, row) => sum + (row.electricityKWh ?? 0), 0);
  const coolingKWh = withData.reduce((sum, row) => sum + (row.coolingKWh ?? 0), 0);
  const priced = withData.every((row) => row.energyCost !== null || !row.electricityKWh);
  return {
    electricityKWh, coolingKWh, cop: ratioOf(coolingKWh, electricityKWh),
    energyCost: priced ? withData.reduce((sum, row) => sum + (row.energyCost ?? 0), 0) : null,
  };
}

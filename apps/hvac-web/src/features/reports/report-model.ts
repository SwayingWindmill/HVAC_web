import type { Alarm, AlarmSeverity } from '../alarms/alarm-api';
import type { WorkOrder } from '../../api/work-orders';
import type { Finding } from '../diagnostics/diagnostics-model';
import { siteMidnight, wallTime } from '../energy-analysis/consumption/model.ts';

// A Site's monthly report, assembled on demand from the owners' records for the month; nothing is
// stored, so a month's report is as current as the records behind it.

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

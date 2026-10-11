import type { DeviceHistoryAggregateBucket } from '../../api/generated/s2Telemetry.gen';
import { periodWindow, previousWindow, siteDayKey } from '../energy-analysis/consumption/model.ts';

// Demand is the plant's average active power over each quarter-hour, the interval demand
// charges are billed on. A quarter-hour still running is shown, never counted as a peak.

const QUARTER_HOUR_MS = 15 * 60_000;
/** The history owner returns at most 1000 buckets, about ten days of quarter-hours. */
const CHUNK_MS = 10 * 86_400_000;

export interface QuarterHour {
  readonly start: string;
  readonly demandKW: number;
  readonly complete: boolean;
}

export interface DemandPeak {
  readonly at: string;
  readonly demandKW: number;
}

/** The windows to read: from the earlier of the month's and yesterday's start, up to now, in owner-sized chunks. */
export function demandWindows(now: Date, timeZone: string): { from: Date; to: Date }[] {
  const from = Math.min(periodWindow('month', now, timeZone).from.getTime(), previousWindow('today', now, timeZone).from.getTime());
  const windows: { from: Date; to: Date }[] = [];
  for (let start = from; start < now.getTime(); start += CHUNK_MS) {
    windows.push({ from: new Date(start), to: new Date(Math.min(start + CHUNK_MS, now.getTime())) });
  }
  return windows;
}

export function quarterHours(buckets: readonly DeviceHistoryAggregateBucket[], now: Date): QuarterHour[] {
  return buckets
    .filter((bucket) => bucket.gauge)
    .map((bucket) => ({
      start: new Date(bucket.periodStart).toISOString(),
      demandKW: bucket.gauge!.average,
      complete: Date.parse(bucket.periodEnd) - Date.parse(bucket.periodStart) === QUARTER_HOUR_MS && Date.parse(bucket.periodEnd) <= now.getTime(),
    }))
    .sort((left, right) => Date.parse(left.start) - Date.parse(right.start));
}

function peakOf(hours: readonly QuarterHour[]): DemandPeak | null {
  let peak: QuarterHour | null = null;
  for (const hour of hours) if (hour.complete && (!peak || hour.demandKW > peak.demandKW)) peak = hour;
  return peak ? { at: peak.start, demandKW: peak.demandKW } : null;
}

export interface DemandSummary {
  /** The latest quarter-hour, possibly still running. */
  readonly current: QuarterHour | null;
  readonly today: DemandPeak | null;
  readonly yesterday: DemandPeak | null;
  readonly month: DemandPeak | null;
  /** Today's average demand over its peak; how evenly the plant draws power. */
  readonly todayLoadFactor: number | null;
  /** Each Site-local day of the month so far with its peak. */
  readonly dailyPeaks: readonly { day: string; peak: DemandPeak | null }[];
}

export function demandSummary(hours: readonly QuarterHour[], now: Date, timeZone: string): DemandSummary {
  const today = periodWindow('today', now, timeZone).from.getTime();
  const yesterday = previousWindow('today', now, timeZone).from.getTime();
  const month = periodWindow('month', now, timeZone).from.getTime();
  const at = (hour: QuarterHour) => Date.parse(hour.start);
  const todays = hours.filter((hour) => at(hour) >= today);
  const todayPeak = peakOf(todays);
  const completeToday = todays.filter((hour) => hour.complete);
  const byDay = new Map<string, QuarterHour[]>();
  for (const hour of hours) {
    if (at(hour) < month) continue;
    const day = siteDayKey(new Date(hour.start), timeZone);
    byDay.set(day, [...(byDay.get(day) ?? []), hour]);
  }
  return {
    current: hours.at(-1) ?? null,
    today: todayPeak,
    yesterday: peakOf(hours.filter((hour) => at(hour) >= yesterday && at(hour) < today)),
    month: peakOf(hours.filter((hour) => at(hour) >= month)),
    todayLoadFactor: todayPeak && todayPeak.demandKW > 0 && completeToday.length > 0
      ? completeToday.reduce((sum, hour) => sum + hour.demandKW, 0) / completeToday.length / todayPeak.demandKW
      : null,
    dailyPeaks: [...byDay.entries()].map(([day, dayHours]) => ({ day, peak: peakOf(dayHours) })),
  };
}

export interface DemandChartRow {
  /** Minutes after Site-local midnight. */
  readonly minute: number;
  readonly today: number | null;
  readonly yesterday: number | null;
}

/** Today's and yesterday's quarter-hours side by side on the Site's clock. */
export function demandChartRows(hours: readonly QuarterHour[], now: Date, timeZone: string): DemandChartRow[] {
  const today = periodWindow('today', now, timeZone).from.getTime();
  const yesterday = previousWindow('today', now, timeZone).from.getTime();
  const slot = (start: number, from: number) => Math.round((start - from) / QUARTER_HOUR_MS);
  const rows = Array.from({ length: 96 }, (_, index) => ({ minute: index * 15, today: null as number | null, yesterday: null as number | null }));
  for (const hour of hours) {
    const start = Date.parse(hour.start);
    if (start >= today) {
      const index = slot(start, today);
      if (index < 96) rows[index].today = hour.demandKW;
    } else if (start >= yesterday) {
      const index = slot(start, yesterday);
      if (index < 96) rows[index].yesterday = hour.demandKW;
    }
  }
  return rows;
}

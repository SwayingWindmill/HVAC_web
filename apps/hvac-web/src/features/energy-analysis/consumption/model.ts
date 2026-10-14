import type { EnergySeriesQuery, EnergySeriesResponse } from '../../../api/generated/platformGateway.gen';

export const ENERGY_PERIODS = { today: '今日', '7d': '近 7 天', month: '本月', year: '本年' } as const;
export type EnergyPeriod = keyof typeof ENERGY_PERIODS;
export type Granularity = EnergySeriesQuery['granularity'];
export const BUCKET_NOUN: Record<Granularity, string> = { hour: '时段', day: '日', month: '月' };

interface WallTime { year: number; month: number; day: number; hour: number; minute: number }

export function wallTime(instant: Date, timeZone: string): WallTime {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-US', {
    timeZone, year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', hourCycle: 'h23',
  }).formatToParts(instant).map((part) => [part.type, Number(part.value)]));
  return { year: parts.year, month: parts.month, day: parts.day, hour: parts.hour, minute: parts.minute };
}

/** The instant at which the Site's wall clock shows the given local midnight. */
export function siteMidnight(year: number, month: number, day: number, timeZone: string): Date {
  const guess = Date.UTC(year, month - 1, day);
  const shown = wallTime(new Date(guess), timeZone);
  const offset = Date.UTC(shown.year, shown.month - 1, shown.day, shown.hour, shown.minute) - guess;
  return new Date(guess - offset);
}

/** Site-local window of a period, ending now; buckets are hours today, days within a month, months within a year. */
export function periodWindow(period: EnergyPeriod, now: Date, timeZone: string): { granularity: Granularity; from: Date; to: Date } {
  const today = wallTime(now, timeZone);
  switch (period) {
    case 'today':
      return { granularity: 'hour', from: siteMidnight(today.year, today.month, today.day, timeZone), to: now };
    case '7d':
      return { granularity: 'day', from: siteMidnight(today.year, today.month, today.day - 6, timeZone), to: now };
    case 'month':
      return { granularity: 'day', from: siteMidnight(today.year, today.month, 1, timeZone), to: now };
    case 'year':
      return { granularity: 'month', from: siteMidnight(today.year, 1, 1, timeZone), to: now };
  }
}

export interface EnergyRow {
  readonly periodStart: string;
  readonly label: string;
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  /** Cooling delivered per unit of HVAC electricity in the bucket; null without both energies. */
  readonly cop: number | null;
}

export interface EnergySummary {
  readonly granularity: Granularity;
  readonly rows: readonly EnergyRow[];
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  readonly cop: number | null;
  readonly peak: EnergyRow | null;
  /** Latest instant both energies are known up to; null when either has no data. */
  readonly dataWatermark: string | null;
  /** Suspect or invalid meter intervals left out of every total. */
  readonly excludedIntervals: number;
}

const HOUR_MS = 3_600_000;
const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

export function bucketLabel(periodStart: string, granularity: Granularity, timeZone: string): string {
  const instant = new Date(periodStart);
  const local = wallTime(instant, timeZone);
  if (granularity === 'hour') return `${String(local.hour).padStart(2, '0')}:00`;
  if (granularity === 'month') return `${local.month} 月`;
  const weekday = WEEKDAYS[new Date(Date.UTC(local.year, local.month - 1, local.day)).getUTCDay()];
  return `${local.month}/${local.day} ${weekday}`;
}

const ratio = (cooling: number | null, electricity: number | null) =>
  cooling === null || electricity === null || electricity <= 0 ? null : cooling / electricity;

const total = (series: EnergySeriesResponse) =>
  series.points.length === 0 ? null : series.points.reduce((sum, point) => sum + point.energyKWh, 0);

const earlier = (left?: string, right?: string) =>
  !left || !right ? null : new Date(left) <= new Date(right) ? left : right;

export function summarizeEnergy(
  electricity: EnergySeriesResponse,
  cooling: EnergySeriesResponse,
  granularity: Granularity,
  timeZone: string,
): EnergySummary {
  const byStart = new Map<string, { electricity: number | null; cooling: number | null }>();
  const at = (periodStart: string) => {
    const key = new Date(periodStart).toISOString();
    const entry = byStart.get(key) ?? { electricity: null, cooling: null };
    byStart.set(key, entry);
    return entry;
  };
  for (const point of electricity.points) at(point.periodStart).electricity = point.energyKWh;
  for (const point of cooling.points) at(point.periodStart).cooling = point.energyKWh;
  const rows = [...byStart.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([periodStart, energy]): EnergyRow => ({
      periodStart,
      label: bucketLabel(periodStart, granularity, timeZone),
      electricityKWh: energy.electricity,
      coolingKWh: energy.cooling,
      cop: ratio(energy.cooling, energy.electricity),
    }));
  const complete = rows.filter((row) => row.cop !== null);
  const sum = (values: readonly (number | null)[]) => values.reduce<number>((acc, value) => acc + (value ?? 0), 0);
  let peak: EnergyRow | null = null;
  for (const row of rows) if (row.electricityKWh !== null && (peak === null || row.electricityKWh > peak.electricityKWh!)) peak = row;
  const excluded = (series: EnergySeriesResponse) => series.metadata.qualitySummary.suspect + series.metadata.qualitySummary.invalid;
  return {
    granularity,
    rows,
    electricityKWh: total(electricity),
    coolingKWh: total(cooling),
    // Only buckets with both energies count, so a cooling meter gap does not understate efficiency.
    cop: complete.length === 0 ? null : ratio(sum(complete.map((row) => row.coolingKWh)), sum(complete.map((row) => row.electricityKWh))),
    peak,
    dataWatermark: earlier(electricity.metadata.dataWatermark, cooling.metadata.dataWatermark),
    excludedIntervals: excluded(electricity) + excluded(cooling),
  };
}

/**
 * The same span of the period before, ending where the current period starts: yesterday up to
 * this time, the seven days before, last month or last year to the same point.
 */
export function previousWindow(period: EnergyPeriod, now: Date, timeZone: string): { granularity: Granularity; from: Date; to: Date } {
  const current = periodWindow(period, now, timeZone);
  const start = wallTime(current.from, timeZone);
  const from = period === 'today' ? siteMidnight(start.year, start.month, start.day - 1, timeZone)
    : period === '7d' ? siteMidnight(start.year, start.month, start.day - 7, timeZone)
    : period === 'month' ? siteMidnight(start.year, start.month - 1, 1, timeZone)
    : siteMidnight(start.year - 1, 1, 1, timeZone);
  const to = new Date(Math.min(from.getTime() + (now.getTime() - current.from.getTime()), current.from.getTime()));
  return { granularity: current.granularity, from, to };
}

/** A bucket's place in its period: hour of the day, day of the window or month of the year. */
function bucketOrdinal(periodStart: string, from: Date, granularity: Granularity, timeZone: string): number {
  const at = wallTime(new Date(periodStart), timeZone);
  const start = wallTime(from, timeZone);
  if (granularity === 'month') return (at.year - start.year) * 12 + at.month - start.month;
  const days = (Date.UTC(at.year, at.month - 1, at.day) - Date.UTC(start.year, start.month - 1, start.day)) / 86_400_000;
  return granularity === 'hour' ? days * 24 + at.hour : days;
}

/** The start of the bucket at an ordinal of the period starting at `from`. */
function bucketAt(from: Date, granularity: Granularity, ordinal: number, timeZone: string): Date {
  const start = wallTime(from, timeZone);
  if (granularity === 'month') return siteMidnight(start.year, start.month + ordinal, 1, timeZone);
  if (granularity === 'day') return siteMidnight(start.year, start.month, start.day + ordinal, timeZone);
  return new Date(siteMidnight(start.year, start.month, start.day, timeZone).getTime() + ordinal * HOUR_MS);
}

/**
 * The label of the current period's bucket that an instant of a period starting at `start` lines
 * up with: hours of a day, days of a window or months of a year, matched by their place.
 */
export function alignedLabel(periodStart: string, start: Date, from: Date, granularity: Granularity, timeZone: string): string {
  return bucketLabel(bucketAt(from, granularity, bucketOrdinal(periodStart, start, granularity, timeZone), timeZone).toISOString(), granularity, timeZone);
}

export interface BucketComparison {
  readonly label: string;
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  readonly cop: number | null;
  readonly previousElectricityKWh: number | null;
  readonly previousCoolingKWh: number | null;
  readonly previousCop: number | null;
  /** The current bucket has ended; the one still running holds partial energy. */
  readonly complete: boolean;
}

/**
 * The current period's buckets against the previous period's, lined up by their place in the
 * period and labelled as the current period's buckets.
 */
export function comparePeriods(
  current: EnergySummary,
  previous: EnergySummary,
  { from, previousFrom, now, timeZone }: { from: Date; previousFrom: Date; now: Date; timeZone: string },
): BucketComparison[] {
  const { granularity } = current;
  const byOrdinal = (summary: EnergySummary, start: Date) =>
    new Map(summary.rows.map((row) => [bucketOrdinal(row.periodStart, start, granularity, timeZone), row]));
  const currentRows = byOrdinal(current, from);
  const previousRows = byOrdinal(previous, previousFrom);
  return [...new Set([...currentRows.keys(), ...previousRows.keys()])].sort((left, right) => left - right).map((ordinal) => {
    const row = currentRows.get(ordinal);
    const before = previousRows.get(ordinal);
    return {
      label: bucketLabel(bucketAt(from, granularity, ordinal, timeZone).toISOString(), granularity, timeZone),
      electricityKWh: row?.electricityKWh ?? null,
      coolingKWh: row?.coolingKWh ?? null,
      cop: row?.cop ?? null,
      previousElectricityKWh: before?.electricityKWh ?? null,
      previousCoolingKWh: before?.coolingKWh ?? null,
      previousCop: before?.cop ?? null,
      complete: bucketAt(from, granularity, ordinal + 1, timeZone) <= now,
    };
  });
}

/** Buckets for a chart: a bucket still running holds partial energy that would read as a drop, so its current values are left out. */
export function completeOnly(buckets: readonly BucketComparison[]): BucketComparison[] {
  return buckets.map((bucket) => (bucket.complete ? bucket : { ...bucket, electricityKWh: null, coolingKWh: null, cop: null }));
}

/** The fractional change from `previous` to `current`; null when either is unknown or the base is zero. */
export function relativeChange(current: number | null, previous: number | null): number | null {
  if (current === null || previous === null || previous === 0) return null;
  return (current - previous) / previous;
}

export interface HeatmapDay {
  readonly label: string;
  /** Energy per Site-local hour 0–23; null where the hour has no reading yet. */
  readonly values: readonly (number | null)[];
}

/** Hourly energy laid out as Site-local days by hour of day, oldest day first. */
export function hourlyHeatmap(series: EnergySeriesResponse, timeZone: string, days: readonly string[]): HeatmapDay[] {
  const byDay = new Map(days.map((day) => [day, Array.from({ length: 24 }, () => null as number | null)]));
  for (const point of series.points) {
    const local = wallTime(new Date(point.periodStart), timeZone);
    const key = `${local.year}-${String(local.month).padStart(2, '0')}-${String(local.day).padStart(2, '0')}`;
    const hours = byDay.get(key);
    if (hours) hours[local.hour] = point.energyKWh;
  }
  return days.map((day) => {
    const [year, month, date] = day.split('-').map(Number);
    const weekday = WEEKDAYS[new Date(Date.UTC(year, month - 1, date)).getUTCDay()];
    return { label: `${month}/${date} ${weekday}`, values: byDay.get(day)! };
  });
}

/** The last `count` Site-local days as YYYY-MM-DD, oldest first, today last. */
/** The Site-local calendar day an instant falls on, as YYYY-MM-DD. */
export function siteDayKey(instant: Date, timeZone: string): string {
  const local = wallTime(instant, timeZone);
  return `${local.year}-${String(local.month).padStart(2, '0')}-${String(local.day).padStart(2, '0')}`;
}

export function siteDays(now: Date, timeZone: string, count: number): string[] {
  return Array.from({ length: count }, (_, index) => siteDayKey(new Date(now.getTime() - (count - 1 - index) * 86_400_000), timeZone));
}

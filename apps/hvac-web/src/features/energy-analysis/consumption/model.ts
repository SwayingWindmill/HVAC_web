import type { EnergySeriesQuery, EnergySeriesResponse } from '../../../api/generated/platformGateway.gen';

export const ENERGY_PERIODS = { today: '今日', '7d': '近 7 天', month: '本月', year: '本年' } as const;
export type EnergyPeriod = keyof typeof ENERGY_PERIODS;
export type Granularity = EnergySeriesQuery['granularity'];
export const BUCKET_NOUN: Record<Granularity, string> = { hour: '时段', day: '日', month: '月' };

interface WallTime { year: number; month: number; day: number; hour: number; minute: number }

function wallTime(instant: Date, timeZone: string): WallTime {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-US', {
    timeZone, year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', hourCycle: 'h23',
  }).formatToParts(instant).map((part) => [part.type, Number(part.value)]));
  return { year: parts.year, month: parts.month, day: parts.day, hour: parts.hour, minute: parts.minute };
}

/** The instant at which the Site's wall clock shows the given local midnight. */
function siteMidnight(year: number, month: number, day: number, timeZone: string): Date {
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

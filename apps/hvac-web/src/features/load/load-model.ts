import type { SiteTariff } from '../../api/generated/platformGateway.gen';
import { siteMidnight, wallTime } from '../energy-analysis/consumption/model.ts';
import { daySchedule, periodAt, type PeriodCode } from '../tariff/tariff-model.ts';
import type { QuarterHour } from './demand-model.ts';
import type { LoadForecastPoint } from './forecast-model.ts';

// The plant's load on one clock: metered quarter-hours up to now and the forecast after it, today
// and tomorrow on the Site's calendar. Load is the quarter-hour average active power, the same
// figure a demand charge is billed on.

const QUARTER_HOUR_MS = 15 * 60_000;
const HOUR_MS = 3_600_000;

export interface LoadDays {
  readonly yesterday: number;
  readonly today: number;
  readonly tomorrow: number;
  /** The midnight that ends tomorrow. */
  readonly end: number;
}

export function loadDays(now: Date, timeZone: string): LoadDays {
  const local = wallTime(now, timeZone);
  const midnight = (offset: number) => siteMidnight(local.year, local.month, local.day + offset, timeZone).getTime();
  return { yesterday: midnight(-1), today: midnight(0), tomorrow: midnight(1), end: midnight(2) };
}

/** The quarter-hour nearest an instant; forecast points start at the forecast origin, which is not on one. */
export function nearestQuarter(at: string): number {
  return Math.round(Date.parse(at) / QUARTER_HOUR_MS) * QUARTER_HOUR_MS;
}

export interface LoadCurveRow {
  readonly at: number;
  /** Metered load; the quarter-hour still running shows what it has averaged so far. */
  readonly actualKW: number | null;
  /** The same quarter-hour of yesterday, drawn under today only. */
  readonly previousKW: number | null;
  readonly forecastKW: number | null;
  /** Lower and upper bound; null where the point has no band. */
  readonly band: readonly [number, number] | null;
}

/** Every quarter-hour of today and tomorrow, with the forecast points on the same grid. */
export function loadCurveRows(quarters: readonly QuarterHour[], forecast: readonly LoadForecastPoint[], days: LoadDays): LoadCurveRow[] {
  const actual = new Map(quarters.map((quarter) => [Date.parse(quarter.start), quarter.demandKW]));
  const ahead = new Map(forecast.map((point) => [Date.parse(point.at), point]));
  const dayLength = days.today - days.yesterday;
  const rows: LoadCurveRow[] = [];
  for (let at = days.today; at < days.end; at += QUARTER_HOUR_MS) {
    const point = ahead.get(at);
    rows.push({
      at,
      actualKW: actual.get(at) ?? null,
      previousKW: at < days.tomorrow ? actual.get(at - dayLength) ?? null : null,
      forecastKW: point?.loadKW ?? null,
      band: point && point.lowerKW !== null && point.upperKW !== null ? [point.lowerKW, point.upperKW] : null,
    });
  }
  return rows;
}

/** The average of an hour's complete quarter-hours; null when it has none. */
export function meteredHour(quarters: readonly QuarterHour[], hourStart: number): number | null {
  const inHour = quarters.filter((quarter) => quarter.complete && Date.parse(quarter.start) >= hourStart && Date.parse(quarter.start) < hourStart + HOUR_MS);
  return inHour.length ? inHour.reduce((sum, quarter) => sum + quarter.demandKW, 0) / inHour.length : null;
}

/** The highest-priced periods of the Tariff, the ones worth moving load out of. */
const EXPENSIVE: readonly PeriodCode[] = ['SUPER_PEAK', 'PEAK'];

/** Today's and tomorrow's peak-priced spans as instants, shaded on the curve. */
export function expensiveSpans(tariff: SiteTariff, days: LoadDays, timeZone: string): { from: number; to: number; code: PeriodCode }[] {
  return [days.today, days.tomorrow].flatMap((midnight) => daySchedule(tariff, new Date(midnight + HOUR_MS * 12), timeZone)
    .filter((period) => EXPENSIVE.includes(period.periodCode))
    .map((period) => ({ from: midnight + period.startMinute * 60_000, to: midnight + period.endMinute * 60_000, code: period.periodCode })));
}

/** Forecast energy over the points, and the share of it that falls in peak-priced periods. */
export function forecastEnergy(points: readonly LoadForecastPoint[], tariff: SiteTariff | undefined, timeZone: string) {
  let energyKWh = 0;
  let expensiveKWh = 0;
  for (const point of points) {
    const kWh = point.loadKW * 0.25;
    energyKWh += kWh;
    const period = tariff ? periodAt(tariff, new Date(point.at), timeZone) : null;
    if (period && EXPENSIVE.includes(period.periodCode)) expensiveKWh += kWh;
  }
  return { energyKWh, expensiveShare: tariff && energyKWh > 0 ? expensiveKWh / energyKWh : null };
}

// Today so far against yesterday up to the same time of day, worked out like the web overview's
// 今日用能 (apps/hvac-web/src/features/energy-analysis/consumption/model.ts) so the two show the
// same numbers at the same moment (#664). The Energy analytics owner cuts partial hours, while
// siteDayBounds resolves local midnight from fixed offsets or verified Intl support and falls
// back to recentDays when WeChat lacks Intl time zone data for a DST zone.

import type { Gateway } from './gateway';

const DAY_MS = 86_400_000;
const MINUTE_MS = 60_000;

export interface SiteScope {
  readonly id: string;
  readonly tenantId: string;
  readonly timezone: string;
}

export interface EnergySeriesPayload {
  readonly points: HourPoint[];
  readonly devices?: { readonly deviceId: string; readonly points: DayPoint[] }[];
}

export function queryEnergySeries(
  client: Pick<Gateway, 'post'>,
  site: SiteScope,
  energyType: 'electricity' | 'cooling',
  granularity: 'hour' | 'day',
  window: Window,
  breakdown?: 'device',
): Promise<EnergySeriesPayload> {
  return client.post<EnergySeriesPayload>('/api/v1/analytics/energy-series', {
    tenantId: site.tenantId,
    siteId: site.id,
    energyType,
    granularity,
    timezone: site.timezone,
    from: window.from,
    to: window.to,
    qualityPolicy: 'VALID_ONLY',
    ...(breakdown ? { breakdown } : {}),
  });
}

export function queryNonEmptyHourly(
  client: Pick<Gateway, 'post'>,
  site: SiteScope,
  energyType: 'electricity' | 'cooling',
  window: Window,
): Promise<HourPoint[]> {
  if (Date.parse(window.to) <= Date.parse(window.from)) {
    return Promise.resolve([] as HourPoint[]);
  }
  return queryEnergySeries(client, site, energyType, 'hour', window).then((response) => response.points);
}

export const fetchRecentDays = (
  client: Pick<Gateway, 'post'>,
  site: SiteScope,
  now: Date,
): Promise<HourPoint[]> =>
  queryEnergySeries(
    client,
    site,
    'electricity',
    'day',
    { from: new Date(now.getTime() - 2 * DAY_MS).toISOString(), to: now.toISOString() },
  ).then((response) => response.points);

// Fixed-offset IANA zones that never observe DST, keeping Site midnight derivation independent of
// phone Intl timeZone support (used on all Chinese Sites and standard fixed zones).
const FIXED_OFFSET_MINUTES: Readonly<Record<string, number>> = {
  UTC: 0,
  'Etc/UTC': 0,
  'Etc/GMT': 0,
  GMT: 0,
  GMT0: 0,
  Greenwich: 0,
  Universal: 0,
  Zulu: 0,
  'Asia/Shanghai': 480,
  'Asia/Chongqing': 480,
  'Asia/Chungking': 480,
  'Asia/Harbin': 480,
  'Asia/Urumqi': 480,
  'Asia/Kashgar': 480,
  'Asia/Hong_Kong': 480,
  'Asia/Macau': 480,
  'Asia/Macao': 480,
  'Asia/Taipei': 480,
  'Asia/Singapore': 480,
  'Asia/Kuala_Lumpur': 480,
  'Asia/Kuching': 480,
  'Asia/Manila': 480,
  'Asia/Makassar': 480,
  'Asia/Brunei': 480,
  'Asia/Ulaanbaatar': 480,
  'Australia/Perth': 480,
  PRC: 480,
  ROC: 480,
  Hongkong: 480,
  Singapore: 480,
  'Asia/Tokyo': 540,
  'Asia/Seoul': 540,
  'Asia/Pyongyang': 540,
  'Asia/Jayapura': 540,
  Japan: 540,
  ROK: 540,
  'Asia/Bangkok': 420,
  'Asia/Ho_Chi_Minh': 420,
  'Asia/Saigon': 420,
  'Asia/Jakarta': 420,
  'Asia/Pontianak': 420,
  'Asia/Phnom_Penh': 420,
  'Asia/Vientiane': 420,
  'Asia/Novosibirsk': 420,
  'Asia/Yangon': 390,
  'Asia/Rangoon': 390,
  'Asia/Dhaka': 360,
  'Asia/Dacca': 360,
  'Asia/Kathmandu': 345,
  'Asia/Katmandu': 345,
  'Asia/Kolkata': 330,
  'Asia/Calcutta': 330,
  'Asia/Colombo': 330,
  'Asia/Karachi': 300,
  'Asia/Tashkent': 300,
  'Asia/Dubai': 240,
  'Asia/Muscat': 240,
  'Asia/Riyadh': 180,
  'Asia/Kuwait': 180,
  'Asia/Qatar': 180,
  'Asia/Bahrain': 180,
  'Asia/Baghdad': 180,
  'Pacific/Honolulu': -600,
  'Asia/Honolulu': -600,
  'Pacific/Kiritimati': 840,
};

function fixedOffsetMinutes(timezone: string): number | null {
  if (timezone in FIXED_OFFSET_MINUTES) return FIXED_OFFSET_MINUTES[timezone];
  const gmtMatch = /^Etc\/GMT([+-])(\d{1,2})$/.exec(timezone);
  if (gmtMatch !== null) {
    const hours = Number(gmtMatch[2]);
    // POSIX Etc/GMT signs are inverted relative to ISO offsets (Etc/GMT-8 is UTC+8).
    const signed = gmtMatch[1] === '-' ? hours : -hours;
    if (signed >= -12 && signed <= 14) return signed * 60;
  }
  return null;
}

interface WallParts {
  readonly year: number;
  readonly month: number;
  readonly day: number;
  readonly hour: number;
  readonly minute: number;
  readonly second: number;
}

function intlWallParts(instant: Date, timeZone: string): WallParts | null {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: 'numeric',
      minute: 'numeric',
      second: 'numeric',
      hourCycle: 'h23',
    }).formatToParts(instant);
    const values: Record<string, number> = {};
    for (const part of parts) {
      if (part.type !== 'literal') values[part.type] = Number(part.value);
    }
    if (!Number.isFinite(values.year) || !Number.isFinite(values.month) || !Number.isFinite(values.day)) return null;
    return {
      year: values.year,
      month: values.month,
      day: values.day,
      hour: values.hour % 24,
      minute: values.minute,
      second: values.second,
    };
  } catch (error) {
    if (error instanceof RangeError) return null;
    throw error;
  }
}

function supportsIntlTimeZone(): boolean {
  const probe = new Date(Date.UTC(2026, 0, 1, 0, 0, 0));
  const utc = intlWallParts(probe, 'UTC');
  const shanghai = intlWallParts(probe, 'Asia/Shanghai');
  return utc !== null && shanghai !== null && utc.day === 1 && utc.hour === 0 && shanghai.day === 1 && shanghai.hour === 8;
}

function intlMidnightMs(year: number, month: number, day: number, timeZone: string): number | null {
  const guess = Date.UTC(year, month - 1, day);
  const shown = intlWallParts(new Date(guess), timeZone);
  if (shown === null) return null;
  const offset = Date.UTC(shown.year, shown.month - 1, shown.day, shown.hour, shown.minute, shown.second) - guess;
  let candidate = guess - offset;
  const check = intlWallParts(new Date(candidate), timeZone);
  if (check === null) return null;
  if (check.year !== year || check.month !== month || check.day !== day || check.hour !== 0 || check.minute !== 0) {
    const drift = Date.UTC(check.year, check.month - 1, check.day, check.hour, check.minute, check.second) - guess;
    const adjusted = candidate - drift;
    const recheck = intlWallParts(new Date(adjusted), timeZone);
    if (recheck !== null && recheck.year === year && recheck.month === month && recheck.day === day) {
      candidate = adjusted;
    }
  }
  return candidate;
}

export interface DayBounds {
  readonly todayStart: string;
  readonly yesterdayStart: string;
}

/** Site local midnight instants for today and yesterday, or null if the runtime lacks Intl support for a non-fixed zone. */
export function siteDayBounds(now: Date, timezone: string): DayBounds | null {
  const fixedMinutes = fixedOffsetMinutes(timezone);
  if (fixedMinutes !== null) {
    const offsetMs = fixedMinutes * MINUTE_MS;
    const todayMs = Math.floor((now.getTime() + offsetMs) / DAY_MS) * DAY_MS - offsetMs;
    return {
      todayStart: new Date(todayMs).toISOString(),
      yesterdayStart: new Date(todayMs - DAY_MS).toISOString(),
    };
  }
  if (!timezone || !supportsIntlTimeZone()) return null;
  const today = intlWallParts(now, timezone);
  if (today === null) return null;
  const todayMs = intlMidnightMs(today.year, today.month, today.day, timezone);
  const previousDate = new Date(Date.UTC(today.year, today.month - 1, today.day - 1));
  const yesterdayMs = intlMidnightMs(previousDate.getUTCFullYear(), previousDate.getUTCMonth() + 1, previousDate.getUTCDate(), timezone);
  if (todayMs === null || yesterdayMs === null) return null;
  return {
    todayStart: new Date(todayMs).toISOString(),
    yesterdayStart: new Date(yesterdayMs).toISOString(),
  };
}

export interface DayPoint {
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly energyKWh: number;
}

export interface HourPoint extends DayPoint {
  readonly cost?: { readonly amountCNY: number; readonly unpricedKWh: number };
}

export interface EnergySummary {
  /** Null when the meter has no reading in the window, never zero. */
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  /** Cooling per unit of electricity over the hours both meters read. */
  readonly cop: number | null;
  /** The estimated charge of the hours the Site's tariff priced; null when none was. */
  readonly costCNY: number | null;
}

export interface Window {
  readonly from: string;
  readonly to: string;
}

/** Where today began at the Site: the start of the day bucket holding now, else the end of the one before it. */
export function todayStart(days: readonly DayPoint[], now: Date): string | null {
  const at = now.getTime();
  const holding = days.find((point) => Date.parse(point.periodStart) <= at && at < Date.parse(point.periodEnd));
  if (holding !== undefined) return holding.periodStart;
  // Just after midnight the owner may not have a bucket for today yet.
  const ended = days.find((point) => Date.parse(point.periodEnd) <= at && at - Date.parse(point.periodEnd) < DAY_MS);
  return ended === undefined ? null : ended.periodEnd;
}

/** Today from the Site's midnight to now, and yesterday from the midnight before to the same time of day. */
export function sameHoursWindows(
  start: string,
  now: Date,
  yesterdayStart?: string,
): { readonly today: Window; readonly yesterday: Window } {
  const from = Date.parse(start);
  const yesterdayFrom = yesterdayStart === undefined ? from - DAY_MS : Date.parse(yesterdayStart);
  const elapsed = Math.max(0, Math.min(now.getTime() - from, from - yesterdayFrom));
  return {
    today: { from: new Date(from).toISOString(), to: now.toISOString() },
    yesterday: { from: new Date(yesterdayFrom).toISOString(), to: new Date(yesterdayFrom + elapsed).toISOString() },
  };
}

export function hasRecentHourlyReading(hours: readonly DayPoint[], bounds: DayBounds, now: Date): boolean {
  const yesterdayMs = Date.parse(bounds.yesterdayStart);
  const nowMs = now.getTime();
  return hours.some((point) => {
    const startMs = Date.parse(point.periodStart);
    const endMs = Date.parse(point.periodEnd);
    return Number.isFinite(startMs) && Number.isFinite(endMs) && endMs > yesterdayMs && startMs < nowMs;
  });
}

const sumOf = (values: readonly number[]) => values.reduce((total, value) => total + value, 0);

export function summarize(electricity: readonly HourPoint[], cooling: readonly DayPoint[]): EnergySummary {
  const coolingAt = new Map(cooling.map((point) => [Date.parse(point.periodStart), point.energyKWh]));
  const both = electricity.filter((point) => point.energyKWh > 0 && coolingAt.has(Date.parse(point.periodStart)));
  const priced = electricity.filter((point) => point.cost !== undefined && point.energyKWh > point.cost.unpricedKWh);
  return {
    electricityKWh: electricity.length === 0 ? null : sumOf(electricity.map((point) => point.energyKWh)),
    coolingKWh: cooling.length === 0 ? null : sumOf(cooling.map((point) => point.energyKWh)),
    cop: both.length === 0 ? null
      : sumOf(both.map((point) => coolingAt.get(Date.parse(point.periodStart)) ?? 0)) / sumOf(both.map((point) => point.energyKWh)),
    costCNY: priced.length === 0 ? null : sumOf(priced.map((point) => point.cost?.amountCNY ?? 0)),
  };
}

const relativeChange = (current: number | null, previous: number | null) =>
  current === null || previous === null || previous === 0 ? null : (current - previous) / previous;

export function compareWithYesterday(today: EnergySummary, yesterday: EnergySummary) {
  return {
    electricity: relativeChange(today.electricityKWh, yesterday.electricityKWh),
    cooling: relativeChange(today.coolingKWh, yesterday.coolingKWh),
    cop: relativeChange(today.cop, yesterday.cop),
    cost: relativeChange(today.costCNY, yesterday.costCNY),
  };
}

const pad2 = (value: number) => String(value).padStart(2, '0');

const HOUR_MS = 3_600_000;

/**
 * Where a moment falls at the Site, read from the Site's midnight that the owner cut rather than the
 * phone's time zone: that midnight gives the Site's offset from UTC (UTC+8 when it falls at 16:00
 * UTC), and the offset gives the Site's calendar date and clock. A midnight alone cannot tell UTC−10
 * from UTC+14 (they differ by a whole day); offsets east of +14 hours are read as west of Greenwich.
 */
function siteTime(at: number, start: string) {
  const midnight = Date.parse(start);
  const east = ((-midnight % DAY_MS) + DAY_MS) % DAY_MS;
  const offset = east > 14 * HOUR_MS ? east - DAY_MS : east;
  const local = new Date(at + offset);
  return {
    days: Math.floor((at - midnight) / DAY_MS),
    year: local.getUTCFullYear(),
    month: local.getUTCMonth() + 1,
    date: local.getUTCDate(),
    hours: pad2(local.getUTCHours()),
    minutes: pad2(local.getUTCMinutes()),
    seconds: pad2(local.getUTCSeconds()),
  };
}

/** A reading's time as Site clock time to the second: 23:29:52, 昨天 23:29:52, 3 天前. */
export function siteClock(at: string, start: string): string {
  const { days, hours, minutes, seconds } = siteTime(Date.parse(at), start);
  const clock = `${hours}:${minutes}:${seconds}`;
  if (days === 0) return clock;
  return days === -1 ? `昨天 ${clock}` : `${-days} 天前`;
}

/** When something happened at the Site, to the minute: 今天 14:20, 昨天 14:20, 9/29 14:20. */
export function siteWhen(at: string | number, start: string): string {
  const { days, month, date, hours, minutes } = siteTime(typeof at === 'number' ? at : Date.parse(at), start);
  const clock = `${hours}:${minutes}`;
  if (days === 0) return `今天 ${clock}`;
  return days === -1 ? `昨天 ${clock}` : `${month}/${date} ${clock}`;
}

/** A moment in full at the Site: 2026年9月29日 16:26. */
export function siteDateTime(at: string, start: string): string {
  const { year, month, date, hours, minutes } = siteTime(Date.parse(at), start);
  return `${year}年${month}月${date}日 ${hours}:${minutes}`;
}

/** The Site day a moment falls on: 今天, 昨天, 9 月 29 日; and its clock time, 14:20. */
export function siteDay(at: string, start: string): { readonly day: string; readonly time: string } {
  const { days, month, date, hours, minutes } = siteTime(Date.parse(at), start);
  return { day: days === 0 ? '今天' : days === -1 ? '昨天' : `${month} 月 ${date} 日`, time: `${hours}:${minutes}` };
}

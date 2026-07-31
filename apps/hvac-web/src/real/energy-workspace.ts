import type { EnergyGranularity, EnergyQualityPolicy } from '@/api/energy-analytics';

export type EnergyWorkspacePeriod = 'day' | 'week' | 'month' | 'year';

export interface EnergyWorkspaceState {
  readonly period: EnergyWorkspacePeriod;
  readonly anchor: string;
  readonly qualityPolicy: EnergyQualityPolicy;
}

export interface EnergyWorkspaceWindow {
  readonly state: EnergyWorkspaceState;
  readonly granularity: EnergyGranularity;
  readonly from: string;
  readonly to: string;
  readonly previousFrom: string;
  readonly previousTo: string;
  readonly previousAnchor: string;
  readonly nextAnchor: string;
  readonly label: string;
  readonly previousLabel: string;
  readonly drillDownPeriod: Exclude<EnergyWorkspacePeriod, 'year'> | null;
}

export type EnergyComparison =
  | { readonly kind: 'unavailable' }
  | { readonly kind: 'baseline-zero'; readonly differenceKWh: number }
  | { readonly kind: 'percentage'; readonly differenceKWh: number; readonly percentage: number };

const DATE_ONLY_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
const PERIODS = new Set<EnergyWorkspacePeriod>(['day', 'week', 'month', 'year']);
const QUALITY_POLICIES = new Set<EnergyQualityPolicy>(['VALID_ONLY', 'VALID_AND_SUSPECT']);

interface CalendarDate {
  year: number;
  month: number;
  day: number;
}

function parseDateOnly(value: string): CalendarDate | null {
  if (!DATE_ONLY_PATTERN.test(value)) return null;
  const [year, month, day] = value.split('-').map(Number);
  const candidate = new Date(Date.UTC(year, month - 1, day));
  if (
    candidate.getUTCFullYear() !== year
    || candidate.getUTCMonth() !== month - 1
    || candidate.getUTCDate() !== day
  ) return null;
  return { year, month, day };
}

function dateOnly(value: CalendarDate): string {
  return `${String(value.year).padStart(4, '0')}-${String(value.month).padStart(2, '0')}-${String(value.day).padStart(2, '0')}`;
}

function calendarDate(value: string): CalendarDate {
  const parsed = parseDateOnly(value);
  if (!parsed) throw new Error(`Invalid calendar date: ${value}`);
  return parsed;
}

function shiftCalendarDate(value: CalendarDate, options: { days?: number; months?: number; years?: number }): CalendarDate {
  const shifted = new Date(Date.UTC(
    value.year + (options.years ?? 0),
    value.month - 1 + (options.months ?? 0),
    value.day + (options.days ?? 0),
  ));
  return {
    year: shifted.getUTCFullYear(),
    month: shifted.getUTCMonth() + 1,
    day: shifted.getUTCDate(),
  };
}

function weekday(value: CalendarDate): number {
  return new Date(Date.UTC(value.year, value.month - 1, value.day)).getUTCDay();
}

function periodStart(period: EnergyWorkspacePeriod, anchor: CalendarDate): CalendarDate {
  if (period === 'day') return anchor;
  if (period === 'week') {
    const distanceFromMonday = (weekday(anchor) + 6) % 7;
    return shiftCalendarDate(anchor, { days: -distanceFromMonday });
  }
  if (period === 'month') return { year: anchor.year, month: anchor.month, day: 1 };
  return { year: anchor.year, month: 1, day: 1 };
}

function shiftPeriod(period: EnergyWorkspacePeriod, start: CalendarDate, direction: -1 | 1): CalendarDate {
  if (period === 'day') return shiftCalendarDate(start, { days: direction });
  if (period === 'week') return shiftCalendarDate(start, { days: 7 * direction });
  if (period === 'month') return shiftCalendarDate(start, { months: direction });
  return shiftCalendarDate(start, { years: direction });
}

function partsInTimeZone(instant: Date, timeZone: string): Required<CalendarDate> & { hour: number; minute: number; second: number } {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(instant);
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return {
    year: Number(values.year),
    month: Number(values.month),
    day: Number(values.day),
    hour: Number(values.hour),
    minute: Number(values.minute),
    second: Number(values.second),
  };
}

function zonedMidnight(value: CalendarDate, timeZone: string): Date {
  const desiredAsUTC = Date.UTC(value.year, value.month - 1, value.day, 0, 0, 0);
  let candidate = desiredAsUTC;
  for (let iteration = 0; iteration < 4; iteration += 1) {
    const observed = partsInTimeZone(new Date(candidate), timeZone);
    const observedAsUTC = Date.UTC(
      observed.year,
      observed.month - 1,
      observed.day,
      observed.hour,
      observed.minute,
      observed.second,
    );
    const correction = desiredAsUTC - observedAsUTC;
    candidate += correction;
    if (correction === 0) break;
  }
  return new Date(candidate);
}

function dateAtInstant(instant: Date, timeZone: string): string {
  const parts = partsInTimeZone(instant, timeZone);
  return dateOnly(parts);
}

function formatWindowLabel(period: EnergyWorkspacePeriod, start: CalendarDate, endExclusive: CalendarDate, timeZone: string): string {
  const startInstant = zonedMidnight(start, timeZone);
  const endInstant = new Date(zonedMidnight(endExclusive, timeZone).getTime() - 1);
  const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
  if (period === 'day') return dateFormatter.format(startInstant);
  if (period === 'month') {
    return new Intl.DateTimeFormat('zh-CN', { timeZone, year: 'numeric', month: 'long' }).format(startInstant);
  }
  if (period === 'year') {
    return new Intl.DateTimeFormat('zh-CN', { timeZone, year: 'numeric' }).format(startInstant);
  }
  return `${dateFormatter.format(startInstant)} — ${dateFormatter.format(endInstant)}`;
}

function granularityFor(period: EnergyWorkspacePeriod): EnergyGranularity {
  if (period === 'day') return 'hour';
  if (period === 'year') return 'month';
  return 'day';
}

function drillDownFor(period: EnergyWorkspacePeriod): EnergyWorkspaceWindow['drillDownPeriod'] {
  if (period === 'year') return 'month';
  if (period === 'month' || period === 'week') return 'day';
  return null;
}

export function currentDateInTimeZone(timeZone: string, now = new Date()): string {
  return dateAtInstant(now, timeZone);
}

export function parseEnergyWorkspaceSearch(
  search: string,
  timeZone: string,
  now = new Date(),
): EnergyWorkspaceState {
  const parameters = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
  const periodCandidate = parameters.get('period') as EnergyWorkspacePeriod | null;
  const qualityCandidate = parameters.get('quality') as EnergyQualityPolicy | null;
  const period = periodCandidate && PERIODS.has(periodCandidate) ? periodCandidate : 'month';
  const anchorCandidate = parameters.get('anchor') ?? currentDateInTimeZone(timeZone, now);
  const anchor = parseDateOnly(anchorCandidate) ? anchorCandidate : currentDateInTimeZone(timeZone, now);
  const qualityPolicy = qualityCandidate && QUALITY_POLICIES.has(qualityCandidate)
    ? qualityCandidate
    : 'VALID_ONLY';
  const canonicalAnchor = dateOnly(periodStart(period, calendarDate(anchor)));
  return Object.freeze({ period, anchor: canonicalAnchor, qualityPolicy });
}

export function energyWorkspaceSearch(state: EnergyWorkspaceState): string {
  const parameters = new URLSearchParams({
    period: state.period,
    anchor: state.anchor,
    quality: state.qualityPolicy,
  });
  return `?${parameters.toString()}`;
}

export function buildEnergyWorkspaceWindow(
  state: EnergyWorkspaceState,
  timeZone: string,
): EnergyWorkspaceWindow {
  const start = periodStart(state.period, calendarDate(state.anchor));
  const end = shiftPeriod(state.period, start, 1);
  const previous = shiftPeriod(state.period, start, -1);
  const canonicalState = Object.freeze({ ...state, anchor: dateOnly(start) });
  return Object.freeze({
    state: canonicalState,
    granularity: granularityFor(state.period),
    from: zonedMidnight(start, timeZone).toISOString(),
    to: zonedMidnight(end, timeZone).toISOString(),
    previousFrom: zonedMidnight(previous, timeZone).toISOString(),
    previousTo: zonedMidnight(start, timeZone).toISOString(),
    previousAnchor: dateOnly(previous),
    nextAnchor: dateOnly(end),
    label: formatWindowLabel(state.period, start, end, timeZone),
    previousLabel: formatWindowLabel(state.period, previous, start, timeZone),
    drillDownPeriod: drillDownFor(state.period),
  });
}

export function shiftEnergyWorkspaceState(
  state: EnergyWorkspaceState,
  direction: -1 | 1,
  timeZone: string,
): EnergyWorkspaceState {
  const window = buildEnergyWorkspaceWindow(state, timeZone);
  return Object.freeze({
    ...state,
    anchor: direction === -1 ? window.previousAnchor : window.nextAnchor,
  });
}

export function currentEnergyWorkspaceState(
  period: EnergyWorkspacePeriod,
  qualityPolicy: EnergyQualityPolicy,
  timeZone: string,
  now = new Date(),
): EnergyWorkspaceState {
  return parseEnergyWorkspaceSearch(
    energyWorkspaceSearch({ period, anchor: currentDateInTimeZone(timeZone, now), qualityPolicy }),
    timeZone,
    now,
  );
}

export function drillDownEnergyWorkspaceState(
  state: EnergyWorkspaceState,
  periodStartInstant: string,
  timeZone: string,
): EnergyWorkspaceState | null {
  const window = buildEnergyWorkspaceWindow(state, timeZone);
  if (!window.drillDownPeriod) return null;
  return Object.freeze({
    period: window.drillDownPeriod,
    anchor: dateAtInstant(new Date(periodStartInstant), timeZone),
    qualityPolicy: state.qualityPolicy,
  });
}

export function compareEnergyTotals(current: number | null, previous: number | null): EnergyComparison {
  if (current === null || previous === null) return { kind: 'unavailable' };
  const differenceKWh = current - previous;
  if (previous === 0) return { kind: 'baseline-zero', differenceKWh };
  return {
    kind: 'percentage',
    differenceKWh,
    percentage: (differenceKWh / previous) * 100,
  };
}

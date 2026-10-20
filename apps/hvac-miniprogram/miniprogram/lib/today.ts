// Today so far against yesterday up to the same time of day, worked out exactly like the web
// overview's 今日用能 (apps/hvac-web/src/features/energy-analysis/consumption/model.ts) so the two
// show the same numbers at the same moment (#664). The Energy analytics owner cuts the Site's days
// and the partial hours, so the mini program needs no time zone arithmetic: WeChat's JavaScript
// runtime does not reliably support Intl time zones on every phone.

const DAY_MS = 86_400_000;

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
export function sameHoursWindows(start: string, now: Date): { readonly today: Window; readonly yesterday: Window } {
  const from = Date.parse(start);
  const elapsed = Math.min(now.getTime() - from, DAY_MS);
  return {
    today: { from: new Date(from).toISOString(), to: now.toISOString() },
    yesterday: { from: new Date(from - DAY_MS).toISOString(), to: new Date(from - DAY_MS + elapsed).toISOString() },
  };
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

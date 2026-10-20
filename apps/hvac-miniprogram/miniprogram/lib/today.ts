// Today and yesterday from the Energy analytics owner's Site-local day buckets (#664). The owner
// cuts the days in the Site's time zone, so the mini program needs no time zone arithmetic: WeChat's
// JavaScript runtime does not reliably support Intl time zones on every phone.

export interface DayPoint {
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly energyKWh: number;
}

export interface HourPoint extends DayPoint {
  readonly cost?: { readonly periods: readonly { readonly periodCode: string; readonly energyKWh: number; readonly amountCNY: number }[] };
}

export interface SoFar {
  readonly electricityKWh: number;
  readonly coolingKWh: number;
  readonly cop: number | null;
  readonly costCNY: number;
  /** The share of electricity used in peak tariff hours, two decimals; null without electricity. */
  readonly peakShare: number | null;
}

const round = (value: number, digits: number) => Math.round(value * 10 ** digits) / 10 ** digits;

const sumWithin = (electricity: readonly HourPoint[], cooling: readonly DayPoint[], from: number, to: number) => {
  const inside = (point: DayPoint) => Date.parse(point.periodStart) >= from && Date.parse(point.periodStart) < to;
  const hours = electricity.filter(inside);
  const kWh = hours.reduce((total, point) => total + point.energyKWh, 0);
  const periods = hours.flatMap((point) => point.cost?.periods ?? []);
  return {
    kWh,
    cooling: cooling.filter(inside).reduce((total, point) => total + point.energyKWh, 0),
    cost: periods.reduce((total, period) => total + period.amountCNY, 0),
    peak: periods.filter((period) => period.periodCode === 'PEAK').reduce((total, period) => total + period.energyKWh, 0),
  };
};

const changeOf = (now: number, before: number) => (before > 0 ? round(now / before - 1, 3) : null);

/**
 * Today so far against yesterday up to the same time of day, from the owner's hourly buckets:
 * yesterday's span is today's shifted back one day, so no time zone is needed here either.
 */
export function againstYesterday(electricity: readonly HourPoint[], cooling: readonly DayPoint[], todayStart: string, now: Date) {
  const start = Date.parse(todayStart);
  const at = now.getTime();
  const today = sumWithin(electricity, cooling, start, at);
  const yesterday = sumWithin(electricity, cooling, start - 86_400_000, at - 86_400_000);
  const copToday = today.kWh > 0 && today.cooling > 0 ? today.cooling / today.kWh : null;
  const copYesterday = yesterday.kWh > 0 && yesterday.cooling > 0 ? yesterday.cooling / yesterday.kWh : null;
  const soFar: SoFar = {
    electricityKWh: round(today.kWh, 1), coolingKWh: round(today.cooling, 1), cop: copToday === null ? null : round(copToday, 2),
    costCNY: round(today.cost, 2), peakShare: today.kWh > 0 ? round(today.peak / today.kWh, 2) : null,
  };
  return {
    today: soFar,
    change: {
      electricity: changeOf(today.kWh, yesterday.kWh),
      cooling: changeOf(today.cooling, yesterday.cooling),
      cop: copToday !== null && copYesterday !== null ? changeOf(copToday, copYesterday) : null,
      cost: changeOf(today.cost, yesterday.cost),
    },
    yesterdayPeakShare: yesterday.kWh > 0 ? round(yesterday.peak / yesterday.kWh, 2) : null,
  };
}

/** Where today began at the Site: the start of the day bucket holding now, else the end of the one before it. */
export function todayStart(days: readonly DayPoint[], now: Date): string | null {
  const at = now.getTime();
  const holding = days.find((point) => Date.parse(point.periodStart) <= at && at < Date.parse(point.periodEnd));
  if (holding !== undefined) return holding.periodStart;
  // Just after midnight the owner may not have a bucket for today yet.
  const ended = days.find((point) => Date.parse(point.periodEnd) <= at && at - Date.parse(point.periodEnd) < 86_400_000);
  return ended === undefined ? null : ended.periodEnd;
}

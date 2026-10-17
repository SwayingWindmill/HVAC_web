import type { SiteTariff, TariffPeriod } from '../../api/generated/platformGateway.gen';
import { wallTime, type EnergyCost } from '../energy-analysis/consumption/model.ts';

// The Site's Tariff periods, and the costs the Energy analytics owner priced at them. Settlement
// stays the authority on the bill; the demand charge is still estimated here from the published rates.

export type PeriodCode = TariffPeriod['periodCode'];

export const PERIOD_LABEL: Record<PeriodCode, string> = { SUPER_PEAK: '尖峰', PEAK: '峰', FLAT: '平', VALLEY: '谷' };
const PERIOD_ORDER: readonly PeriodCode[] = ['SUPER_PEAK', 'PEAK', 'FLAT', 'VALLEY'];
const byPeriodOrder = (left: PeriodCode, right: PeriodCode) => PERIOD_ORDER.indexOf(left) - PERIOD_ORDER.indexOf(right);

/** Holidays and special days price as their weekday until Tariffs carry a calendar. */
function dayType(local: { year: number; month: number; day: number }): TariffPeriod['dayType'] {
  const weekday = new Date(Date.UTC(local.year, local.month - 1, local.day)).getUTCDay();
  return weekday === 0 || weekday === 6 ? 'WEEKEND' : 'WEEKDAY';
}

/** The periods pricing the Site-local day an instant falls on, in clock order. */
export function daySchedule(tariff: SiteTariff, instant: Date, timeZone: string): TariffPeriod[] {
  const type = dayType(wallTime(instant, timeZone));
  return tariff.periods.filter((period) => period.dayType === type).sort((left, right) => left.startMinute - right.startMinute);
}

/** The period pricing an instant on the Site's clock; null where no period covers it. */
export function periodAt(tariff: SiteTariff, instant: Date, timeZone: string): TariffPeriod | null {
  const local = wallTime(instant, timeZone);
  const minute = local.hour * 60 + local.minute;
  return daySchedule(tariff, instant, timeZone).find((period) => period.startMinute <= minute && minute < period.endMinute) ?? null;
}

export interface PeriodUsage {
  readonly code: PeriodCode;
  readonly energyKWh: number;
  readonly cost: number;
}

export interface TouCost {
  readonly energyKWh: number;
  /** Energy cost of the priced energy, in the Tariff's currency. */
  readonly total: number;
  /** Energy and cost by period, peak first; only periods that saw energy. */
  readonly byPeriod: readonly PeriodUsage[];
  /** Energy in minutes no period of the day covers. */
  readonly unpricedKWh: number;
}

/**
 * The energy charge the Energy analytics owner priced for these buckets (#575), peak periods first.
 * Electricity it did not price counts as unpriced, never as free.
 */
export function sumCosts(buckets: readonly { readonly electricityKWh: number | null; readonly cost: EnergyCost | null }[]): TouCost {
  const usage = new Map<PeriodCode, { energyKWh: number; cost: number }>();
  let energyKWh = 0;
  let total = 0;
  let unpricedKWh = 0;
  for (const bucket of buckets) {
    if (bucket.electricityKWh === null) continue;
    energyKWh += bucket.electricityKWh;
    if (!bucket.cost) {
      unpricedKWh += bucket.electricityKWh;
      continue;
    }
    total += bucket.cost.amountCNY;
    unpricedKWh += bucket.cost.unpricedKWh;
    for (const period of bucket.cost.periods) {
      const entry = usage.get(period.periodCode) ?? { energyKWh: 0, cost: 0 };
      entry.energyKWh += period.energyKWh;
      entry.cost += period.amountCNY;
      usage.set(period.periodCode, entry);
    }
  }
  const byPeriod = [...usage.entries()]
    .sort(([left], [right]) => byPeriodOrder(left, right))
    .map(([code, entry]) => ({ code, energyKWh: entry.energyKWh, cost: entry.cost }));
  return { energyKWh, total, byPeriod, unpricedKWh };
}

/** The clock ranges a period covers on the Site-local day of an instant, e.g. 08:00–11:00、18:00–21:00. */
export function periodRanges(tariff: SiteTariff, instant: Date, timeZone: string): Map<PeriodCode, string> {
  const clock = (minute: number) => `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`;
  const ranges = new Map<PeriodCode, string[]>();
  for (const period of daySchedule(tariff, instant, timeZone)) {
    ranges.set(period.periodCode, [...(ranges.get(period.periodCode) ?? []), `${clock(period.startMinute)}–${clock(period.endMinute)}`]);
  }
  return new Map([...ranges.entries()].map(([code, list]) => [code, list.join('、')]));
}

/** Each bucket's energy charge by its label, for the buckets with electricity. */
export function costsByLabel(rows: readonly { readonly label: string; readonly electricityKWh: number | null; readonly cost: EnergyCost | null }[]): Map<string, number> {
  return new Map(rows.flatMap((row) => (row.electricityKWh === null ? [] : [[row.label, sumCosts([row]).total] as const])));
}

export interface DemandCharge {
  readonly code: PeriodCode;
  readonly ratePerKW: number;
  /** The period's highest complete quarter-hour in the cycle; null before it has one. */
  readonly peak: { readonly at: string; readonly demandKW: number } | null;
  readonly charge: number | null;
}

/**
 * The demand charge of each period that carries a demand rate, as Settlement bills it: the
 * period's highest complete quarter-hour since the cycle began, times its rate.
 */
export function demandCharges(
  quarterHours: readonly { start: string; demandKW: number; complete: boolean }[],
  tariff: SiteTariff,
  timeZone: string,
  cycleStart: Date,
): DemandCharge[] {
  const rates = new Map<PeriodCode, number>();
  for (const period of tariff.periods) {
    if (period.demandRatePerKW > 0) rates.set(period.periodCode, Math.max(rates.get(period.periodCode) ?? 0, period.demandRatePerKW));
  }
  const peaks = new Map<PeriodCode, { at: string; demandKW: number }>();
  for (const hour of quarterHours) {
    const instant = new Date(hour.start);
    if (!hour.complete || instant < cycleStart) continue;
    const period = periodAt(tariff, instant, timeZone);
    if (!period || !rates.has(period.periodCode)) continue;
    const peak = peaks.get(period.periodCode);
    if (!peak || hour.demandKW > peak.demandKW) peaks.set(period.periodCode, { at: hour.start, demandKW: hour.demandKW });
  }
  return [...rates.entries()]
    .sort(([left], [right]) => byPeriodOrder(left, right))
    .map(([code, ratePerKW]) => {
      const peak = peaks.get(code) ?? null;
      return { code, ratePerKW, peak, charge: peak ? peak.demandKW * ratePerKW : null };
    });
}

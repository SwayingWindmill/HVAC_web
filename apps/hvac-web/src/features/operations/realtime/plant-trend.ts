import type { HourlyPoint } from '../systems-devices/device-history';

// The plant's headline trend readings and how they are compared over time, shared by the realtime
// page's metric tabs and the digital twin's figures.

export const POWER_KEY = 'hvac_meter.active_power';
export const COOLING_KEY = 'btu_meter.instant_cooling_capacity';
const HOUR_MS = 3_600_000;

/** COP per quarter: cooling over power where both were recorded and power is positive. */
export function copSeries(power: readonly HourlyPoint[], cooling: readonly HourlyPoint[]): { periodStart: string; value: number }[] {
  const coolingAt = new Map(cooling.map((point) => [point.periodStart, point.average]));
  return power
    .filter((point) => point.average > 0 && coolingAt.has(point.periodStart))
    .map((point) => ({ periodStart: point.periodStart, value: coolingAt.get(point.periodStart)! / point.average }));
}

/** The value an hour ago: the last quarter that started at least an hour before now. */
export function hourAgo(points: readonly { periodStart: string; value: number }[]): number | null {
  const cutoff = Date.now() - HOUR_MS;
  const before = points.filter((point) => Date.parse(point.periodStart) <= cutoff);
  return before.length > 0 ? before[before.length - 1].value : null;
}

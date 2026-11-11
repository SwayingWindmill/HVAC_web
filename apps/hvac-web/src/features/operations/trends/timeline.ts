import { siteMidnight, wallTime } from '../../energy-analysis/consumption/model.ts';
import type { HourlyPoint } from '../systems-devices/device-history.ts';

export type TrendGranularity = 'MINUTE' | 'FIVE_MINUTE' | 'QUARTER_HOUR' | 'HOUR' | 'DAY';

export interface TimeSlot {
  readonly startMs: number;
  readonly endMs: number;
}

export interface GaugeSlot {
  readonly startMs: number;
  readonly endMs: number;
  readonly average: number | null;
  readonly minimum: number | null;
  readonly maximum: number | null;
}

export interface ScalarSlot {
  readonly startMs: number;
  readonly endMs: number;
  readonly value: number | null;
}

const MINUTE_MS = 60_000;

const SUBDAY_STEP_MS: Readonly<Record<Exclude<TrendGranularity, 'DAY'>, number>> = {
  MINUTE: MINUTE_MS,
  FIVE_MINUTE: 5 * MINUTE_MS,
  QUARTER_HOUR: 15 * MINUTE_MS,
  HOUR: 60 * MINUTE_MS,
};

/**
 * Contiguous calendar/clock buckets covering `[fromMs, toMs)` in the Site's timezone,
 * aligned to the same bucket starts ClickHouse emits on `device-series:aggregate`.
 */
export function buildTimeSlots(input: {
  readonly fromMs: number;
  readonly toMs: number;
  readonly granularity: TrendGranularity;
  readonly timeZone: string;
}): readonly TimeSlot[] {
  const { fromMs, toMs, granularity, timeZone } = input;
  if (!Number.isFinite(fromMs) || !Number.isFinite(toMs) || toMs <= fromMs) {
    return [];
  }

  if (granularity === 'DAY') {
    const startLocal = wallTime(new Date(fromMs), timeZone);
    const slots: TimeSlot[] = [];
    for (let offset = 0; ; offset += 1) {
      const startMs = siteMidnight(startLocal.year, startLocal.month, startLocal.day + offset, timeZone).getTime();
      if (startMs >= toMs) break;
      const endMs = siteMidnight(startLocal.year, startLocal.month, startLocal.day + offset + 1, timeZone).getTime();
      if (endMs > fromMs) {
        slots.push({ startMs, endMs });
      }
    }
    return slots;
  }

  const stepMs = SUBDAY_STEP_MS[granularity];
  const local = wallTime(new Date(fromMs), timeZone);
  const minuteFloorMs = Math.floor(fromMs / MINUTE_MS) * MINUTE_MS;
  const remainderMinutes =
    granularity === 'HOUR'
      ? local.minute
      : granularity === 'QUARTER_HOUR'
        ? local.minute % 15
        : granularity === 'FIVE_MINUTE'
          ? local.minute % 5
          : 0;

  let cursorMs = minuteFloorMs - remainderMinutes * MINUTE_MS;
  const slots: TimeSlot[] = [];
  while (cursorMs < toMs) {
    const endMs = cursorMs + stepMs;
    if (endMs > fromMs) {
      slots.push({ startMs: cursorMs, endMs });
    }
    cursorMs = endMs;
  }
  return slots;
}

/**
 * Aligns sparse gauge buckets onto contiguous time slots.
 * Slots without observations carry `null` (never `0`) so charts break across outages.
 */
export function fillGaugeSlots(
  slots: readonly TimeSlot[],
  points: readonly HourlyPoint[],
): readonly GaugeSlot[] {
  const byStart = new Map<number, HourlyPoint>();
  for (const point of points) {
    const startMs = Date.parse(point.periodStart);
    if (Number.isFinite(startMs)) {
      byStart.set(startMs, point);
    }
  }
  return slots.map((slot) => {
    const point = byStart.get(slot.startMs);
    if (!point) {
      return {
        startMs: slot.startMs,
        endMs: slot.endMs,
        average: null,
        minimum: null,
        maximum: null,
      };
    }
    return {
      startMs: slot.startMs,
      endMs: slot.endMs,
      average: point.average,
      minimum: point.minimum,
      maximum: point.maximum,
    };
  });
}

/**
 * Aligns sparse scalar points (such as derived COP or single-value series) onto contiguous time slots,
 * leaving `null` in missing buckets.
 */
export function fillScalarSlots(
  slots: readonly TimeSlot[],
  points: readonly { readonly periodStart: string; readonly value: number }[],
): readonly ScalarSlot[] {
  const byStart = new Map<number, number>();
  for (const point of points) {
    const startMs = Date.parse(point.periodStart);
    if (Number.isFinite(startMs)) {
      byStart.set(startMs, point.value);
    }
  }
  return slots.map((slot) => ({
    startMs: slot.startMs,
    endMs: slot.endMs,
    value: byStart.get(slot.startMs) ?? null,
  }));
}

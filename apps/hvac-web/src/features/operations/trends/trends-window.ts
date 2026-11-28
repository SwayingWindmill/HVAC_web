import { siteMidnight, wallTime } from '../../../lib/site-time.ts';
import { buildTimeSlots, type TimeSlot, type TrendGranularity } from './timeline.ts';

export const MAX_TREND_BUCKETS_PER_POINT = 720;
export const DEFAULT_VIEWPORT_MAX_BUCKETS_PER_POINT = 360;

export type TrendWindowPreset = '1h' | '6h' | 'today' | '7d' | 'custom';

export interface ResolvedTrendWindow {
  readonly preset: TrendWindowPreset;
  readonly fromMs: number;
  readonly toMs: number;
  readonly granularity: TrendGranularity;
  readonly allowedGranularities: readonly TrendGranularity[];
  readonly slots: readonly TimeSlot[];
  readonly compareFromMs: number;
  readonly compareToMs: number;
  readonly compareSlots: readonly TimeSlot[];
  readonly compareLabel: '对比昨日同时段' | '对比上周同时段';
}

const ALL_GRANULARITIES: readonly TrendGranularity[] = [
  'MINUTE',
  'FIVE_MINUTE',
  'QUARTER_HOUR',
  'HOUR',
  'DAY',
];

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;
const MAX_RANGE_MS = 366 * DAY_MS;

function snapMinute(ms: number): number {
  return Math.floor(ms / MINUTE_MS) * MINUTE_MS;
}

function snapFiveMinute(ms: number): number {
  const step = 5 * MINUTE_MS;
  return Math.floor(ms / step) * step;
}

function snapHour(ms: number): number {
  return Math.floor(ms / HOUR_MS) * HOUR_MS;
}

function autoGranularityForPreset(window: Exclude<TrendWindowPreset, 'custom'>, spanMs: number): TrendGranularity {
  if (window === '1h') return 'MINUTE';
  if (window === '6h') return 'FIVE_MINUTE';
  if (window === '7d') return 'HOUR';
  if (spanMs <= 2 * HOUR_MS) return 'MINUTE';
  if (spanMs <= 8 * HOUR_MS) return 'FIVE_MINUTE';
  return 'QUARTER_HOUR';
}

export function recommendViewportGranularity(
  allowedGranularities: readonly TrendGranularity[],
  slotCounts: ReadonlyMap<TrendGranularity, number>,
  maxBucketsPerKey: number = DEFAULT_VIEWPORT_MAX_BUCKETS_PER_POINT,
): TrendGranularity {
  const cap = maxBucketsPerKey > 0 ? maxBucketsPerKey : DEFAULT_VIEWPORT_MAX_BUCKETS_PER_POINT;
  for (const candidate of allowedGranularities) {
    const count = slotCounts.get(candidate);
    if (count !== undefined && count <= cap) {
      return candidate;
    }
  }
  return allowedGranularities[allowedGranularities.length - 1] ?? 'DAY';
}

export function resolveTrendWindow(input: {
  readonly window: TrendWindowPreset;
  readonly from?: string;
  readonly to?: string;
  readonly granularity?: TrendGranularity | 'auto';
  readonly now: Date;
  readonly timezone: string;
}): ResolvedTrendWindow {
  const nowMs = input.now.getTime();
  let preset = input.window;
  let fromMs: number;
  let toMs: number;

  if (preset === 'custom' && input.from && input.to) {
    const parsedFrom = Date.parse(input.from);
    const parsedTo = Date.parse(input.to);
    if (Number.isFinite(parsedFrom) && Number.isFinite(parsedTo) && parsedTo > parsedFrom && parsedTo - parsedFrom <= MAX_RANGE_MS) {
      fromMs = parsedFrom;
      toMs = parsedTo;
    } else {
      preset = '6h';
      toMs = snapFiveMinute(nowMs) || nowMs;
      fromMs = toMs - 6 * HOUR_MS;
    }
  } else if (preset === '1h') {
    toMs = snapMinute(nowMs);
    fromMs = toMs - HOUR_MS;
  } else if (preset === '7d') {
    toMs = snapHour(nowMs);
    fromMs = toMs - 7 * DAY_MS;
  } else if (preset === 'today') {
    const local = wallTime(input.now, input.timezone);
    const midnightMs = siteMidnight(local.year, local.month, local.day, input.timezone).getTime();
    toMs = snapMinute(nowMs);
    fromMs = midnightMs;
    if (toMs - fromMs < 5 * MINUTE_MS) {
      toMs = fromMs + 5 * MINUTE_MS;
    }
  } else {
    preset = '6h';
    toMs = snapFiveMinute(nowMs);
    fromMs = toMs - 6 * HOUR_MS;
  }

  const slotCounts = new Map<TrendGranularity, number>();
  const allowedGranularities = ALL_GRANULARITIES.filter((candidate) => {
    const count = buildTimeSlots({
      fromMs,
      toMs,
      granularity: candidate,
      timeZone: input.timezone,
    }).length;
    slotCounts.set(candidate, count);
    return count >= 1 && count <= MAX_TREND_BUCKETS_PER_POINT;
  });

  let granularity: TrendGranularity;
  if (input.granularity && input.granularity !== 'auto') {
    granularity = allowedGranularities.includes(input.granularity)
      ? input.granularity
      : (allowedGranularities[0] ?? 'DAY');
  } else if (preset === 'custom') {
    granularity = recommendViewportGranularity(allowedGranularities, slotCounts);
  } else {
    const presetGranularity = autoGranularityForPreset(preset, toMs - fromMs);
    const presetCount = slotCounts.get(presetGranularity);
    granularity =
      allowedGranularities.includes(presetGranularity) &&
      presetCount !== undefined &&
      presetCount <= DEFAULT_VIEWPORT_MAX_BUCKETS_PER_POINT
        ? presetGranularity
        : recommendViewportGranularity(allowedGranularities, slotCounts);
  }

  const slots = buildTimeSlots({
    fromMs,
    toMs,
    granularity,
    timeZone: input.timezone,
  });

  const isWeeklyCompare = preset === '7d' || toMs - fromMs > DAY_MS;
  const compareShiftDays = isWeeklyCompare ? 7 : 1;
  const compareShiftMs = compareShiftDays * DAY_MS;
  const compareFromMs = fromMs - compareShiftMs;
  const compareToMs = toMs - compareShiftMs;
  const compareSlots = buildTimeSlots({
    fromMs: compareFromMs,
    toMs: compareToMs,
    granularity,
    timeZone: input.timezone,
  });

  return {
    preset,
    fromMs,
    toMs,
    granularity,
    allowedGranularities,
    slots,
    compareFromMs,
    compareToMs,
    compareSlots,
    compareLabel: isWeeklyCompare ? '对比上周同时段' : '对比昨日同时段',
  };
}

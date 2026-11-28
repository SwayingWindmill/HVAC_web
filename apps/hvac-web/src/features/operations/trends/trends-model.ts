import type {
  DeviceHistoryAggregateBucket,
  DeviceHistoryStateAggregate,
} from '../../../api/generated/s2Telemetry.gen.ts';
import {
  MAX_TREND_POINTS,
  buildTrendPointRef,
  type TrendPointItem,
} from './trends-catalog.ts';
import type { ResolvedTrendWindow } from './trends-window.ts';

export {
  MAX_TREND_POINTS,
  buildTrendCatalog,
  buildTrendPointRef,
  deriveBuiltInPresets,
  findAllBySourceKey,
  findFirstBySourceKey,
  groupTrendDeviceRequests,
} from './trends-catalog.ts';
export type {
  TrendCatalog,
  TrendPointItem,
  TrendPreset,
  TrendPresetId,
} from './trends-catalog.ts';
export {
  DEFAULT_VIEWPORT_MAX_BUCKETS_PER_POINT,
  MAX_TREND_BUCKETS_PER_POINT,
  recommendViewportGranularity,
  resolveTrendWindow,
} from './trends-window.ts';
export type {
  ResolvedTrendWindow,
  TrendWindowPreset,
} from './trends-window.ts';

const MAX_SERIES_PER_LANE = 4;

export interface TrendSeriesMeta {
  readonly ref: string;
  readonly label: string;
  readonly deviceName: string;
  readonly pointLabel: string;
  readonly unit: string;
  readonly precision: number;
  readonly colorVar: string;
  readonly isSetpoint: boolean;
}

export interface TrendGaugeCell {
  readonly value: number | null;
  readonly minimum: number | null;
  readonly maximum: number | null;
  readonly band: readonly [number, number] | null;
  readonly compareValue: number | null;
}

export interface TrendGaugeRow {
  readonly at: number;
  readonly endMs: number;
  readonly values: Readonly<Record<string, TrendGaugeCell>>;
}

export interface TrendGaugeLane {
  readonly id: string;
  readonly unit: string;
  readonly title: string;
  readonly series: readonly TrendSeriesMeta[];
  readonly rows: readonly TrendGaugeRow[];
}

export interface TrendSummaryExtreme {
  readonly value: number;
  readonly at: string;
}

export interface TrendSummaryRow {
  readonly ref: string;
  readonly label: string;
  readonly deviceName: string;
  readonly pointLabel: string;
  readonly unit: string;
  readonly precision: number;
  readonly isSetpoint: boolean;
  readonly latest: number | null;
  readonly average: number | null;
  readonly minimum: TrendSummaryExtreme | null;
  readonly maximum: TrendSummaryExtreme | null;
  readonly validBuckets: number;
  readonly totalBuckets: number;
  readonly coverageRatio: number;
}

export type TrendStateKind = 'RUNNING' | 'STOPPED' | 'FAULT' | 'OTHER';

export interface TrendNormalizedState {
  readonly kind: TrendStateKind;
  readonly label: string;
}

export interface TrendStateSlot {
  readonly startMs: number;
  readonly endMs: number;
  readonly state: TrendNormalizedState | null;
  readonly breakdown?: string;
}

export interface TrendStateSegment {
  readonly startMs: number;
  readonly endMs: number;
  readonly kind: TrendStateKind;
  readonly label: string;
  readonly startPercent: number;
  readonly widthPercent: number;
}

export interface TrendStateTrack {
  readonly ref: string;
  readonly label: string;
  readonly deviceName: string;
  readonly pointLabel: string;
  readonly slots: readonly TrendStateSlot[];
  readonly segments: readonly TrendStateSegment[];
  readonly latestState: TrendNormalizedState | null;
  readonly runningRatio: number;
}

export interface TrendViewModel {
  readonly gaugeLanes: readonly TrendGaugeLane[];
  readonly summaryRows: readonly TrendSummaryRow[];
  readonly statePoints: readonly TrendPointItem[];
  readonly stateTracks: readonly TrendStateTrack[];
}

const CHART_COLOR_VARS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
] as const;

const STATE_KIND_PRIORITY: Readonly<Record<TrendStateKind, number>> = {
  FAULT: 3,
  RUNNING: 2,
  OTHER: 1,
  STOPPED: 0,
};

const UNIT_ORDER: Readonly<Record<string, number>> = {
  kW: 10,
  '°C': 20,
  'm³/h': 30,
  '%': 40,
  Hz: 50,
  '%RH': 60,
  '': 70,
};

const UNIT_LANE_TITLES: Readonly<Record<string, string>> = {
  kW: '功率 / 负荷',
  '°C': '温度 / 温差',
  'm³/h': '流量',
  '%': '比例 / 转速',
  Hz: '运行频率',
  '%RH': '相对湿度',
  '': '无量纲指标',
};

function normalizeDiscreteState(raw: unknown): TrendNormalizedState | null {
  if (raw === null || raw === undefined) return null;
  if (typeof raw === 'boolean') {
    return raw ? { kind: 'RUNNING', label: '运行' } : { kind: 'STOPPED', label: '停止' };
  }
  if (typeof raw === 'number') {
    if (!Number.isFinite(raw)) return null;
    return raw > 0 ? { kind: 'RUNNING', label: '运行' } : { kind: 'STOPPED', label: '停止' };
  }
  if (typeof raw === 'string') {
    const trimmed = raw.trim();
    if (!trimmed) return null;
    const upper = trimmed.toUpperCase();
    if (upper === 'RUNNING' || upper === 'RUN' || upper === 'ON' || upper === 'TRUE' || upper === '1' || upper === 'ACTIVE' || upper === 'OPEN' || upper === 'ENABLED') {
      return { kind: 'RUNNING', label: '运行' };
    }
    if (upper === 'STANDBY') {
      return { kind: 'STOPPED', label: '待机' };
    }
    if (upper === 'STOPPED' || upper === 'STOP' || upper === 'OFF' || upper === 'FALSE' || upper === '0' || upper === 'IDLE' || upper === 'CLOSED' || upper === 'DISABLED') {
      return { kind: 'STOPPED', label: '停止' };
    }
    if (upper === 'FAULT' || upper === 'ERROR' || upper === 'ALARM' || upper === 'TRIP') {
      return { kind: 'FAULT', label: '故障' };
    }
    return { kind: 'OTHER', label: trimmed };
  }
  return { kind: 'OTHER', label: JSON.stringify(raw) };
}

function projectStateBucket(state: DeviceHistoryStateAggregate): {
  readonly dominant: TrendNormalizedState | null;
  readonly latest: TrendNormalizedState | null;
  readonly runningSamples: number;
  readonly totalSamples: number;
  readonly breakdown?: string;
} {
  const latest = normalizeDiscreteState(state.lastValue);
  if (state.stateCounts && state.stateCounts.length > 0) {
    const merged = new Map<string, { state: TrendNormalizedState; count: number }>();
    let runningSamples = 0;
    let totalSamples = 0;

    for (const entry of state.stateCounts) {
      if (entry.count <= 0) continue;
      const norm = normalizeDiscreteState(entry.value);
      if (!norm) continue;
      totalSamples += entry.count;
      if (norm.kind === 'RUNNING') {
        runningSamples += entry.count;
      }
      const key = `${norm.kind}:${norm.label}`;
      const existing = merged.get(key);
      if (existing) {
        merged.set(key, { state: norm, count: existing.count + entry.count });
      } else {
        merged.set(key, { state: norm, count: entry.count });
      }
    }

    if (merged.size > 0) {
      const sorted = [...merged.values()].sort((left, right) => {
        if (left.count !== right.count) return right.count - left.count;
        const priDiff = STATE_KIND_PRIORITY[right.state.kind] - STATE_KIND_PRIORITY[left.state.kind];
        if (priDiff !== 0) return priDiff;
        return left.state.label.localeCompare(right.state.label, 'zh-CN');
      });
      const dominant = sorted[0]!.state;
      const breakdown =
        sorted.length > 1
          ? sorted.map((item) => `${item.state.label} ${item.count}`).join(' · ')
          : undefined;
      return {
        dominant,
        latest: latest ?? dominant,
        runningSamples,
        totalSamples,
        breakdown,
      };
    }
  }

  if (!latest) {
    return { dominant: null, latest: null, runningSamples: 0, totalSamples: 0 };
  }
  const weight = Math.max(1, state.sampleCount);
  return {
    dominant: latest,
    latest,
    runningSamples: latest.kind === 'RUNNING' ? weight : 0,
    totalSamples: weight,
  };
}

export function resolveTrackStateAt(track: TrendStateTrack, hoverAtMs: number | null): TrendNormalizedState | null {
  if (hoverAtMs === null) return track.latestState;
  const hit = track.slots.find((slot) => hoverAtMs >= slot.startMs && hoverAtMs < slot.endMs);
  return hit ? hit.state : null;
}

export function buildTrendViewModel(input: {
  readonly selectedPoints: readonly TrendPointItem[];
  readonly window: ResolvedTrendWindow;
  readonly currentBucketsByDevice: ReadonlyMap<string, readonly DeviceHistoryAggregateBucket[]>;
  readonly compareBucketsByDevice?: ReadonlyMap<string, readonly DeviceHistoryAggregateBucket[]>;
}): TrendViewModel {
  const capped = input.selectedPoints.slice(0, MAX_TREND_POINTS);
  const gaugePoints = capped.filter((point) => point.kind === 'gauge');
  const statePoints = capped.filter((point) => point.kind === 'state');

  const currentMap = new Map<string, Map<number, DeviceHistoryAggregateBucket>>();
  for (const [deviceId, buckets] of input.currentBucketsByDevice.entries()) {
    for (const bucket of buckets) {
      const ref = buildTrendPointRef(deviceId, bucket.telemetryKey);
      const startMs = Date.parse(bucket.periodStart);
      if (!Number.isFinite(startMs)) continue;
      const inner = currentMap.get(ref) ?? new Map<number, DeviceHistoryAggregateBucket>();
      inner.set(startMs, bucket);
      currentMap.set(ref, inner);
    }
  }

  const compareShiftMs = input.window.fromMs - input.window.compareFromMs;
  const compareMap = new Map<string, Map<number, DeviceHistoryAggregateBucket>>();
  if (input.compareBucketsByDevice) {
    for (const [deviceId, buckets] of input.compareBucketsByDevice.entries()) {
      for (const bucket of buckets) {
        const ref = buildTrendPointRef(deviceId, bucket.telemetryKey);
        const shiftedStartMs = Date.parse(bucket.periodStart) + compareShiftMs;
        if (!Number.isFinite(shiftedStartMs)) continue;
        const inner = compareMap.get(ref) ?? new Map<number, DeviceHistoryAggregateBucket>();
        inner.set(shiftedStartMs, bucket);
        compareMap.set(ref, inner);
      }
    }
  }

  // Group gauge points by normalized physical unit, splitting into 4-series lanes if >4 points share one unit
  // so categorical palette chart-1..chart-4 is assigned in fixed order without looping (DESIGN.md §2.1.1).
  const groupsByUnit = new Map<string, TrendPointItem[]>();
  for (const point of gaugePoints) {
    const list = groupsByUnit.get(point.unit) ?? [];
    list.push(point);
    groupsByUnit.set(point.unit, list);
  }

  const orderedUnits = [...groupsByUnit.keys()].sort((left, right) => {
    const leftOrder = UNIT_ORDER[left] ?? 99;
    const rightOrder = UNIT_ORDER[right] ?? 99;
    if (leftOrder !== rightOrder) return leftOrder - rightOrder;
    return left.localeCompare(right, 'zh-CN');
  });

  const gaugeLanes: TrendGaugeLane[] = [];
  for (const unit of orderedUnits) {
    const allUnitPoints = groupsByUnit.get(unit)!;
    const chunkCount = Math.ceil(allUnitPoints.length / MAX_SERIES_PER_LANE);
    const baseTitle = UNIT_LANE_TITLES[unit] ?? (unit ? `读数 (${unit})` : '指标');

    for (let chunkIndex = 0; chunkIndex < chunkCount; chunkIndex += 1) {
      const unitPoints = allUnitPoints.slice(
        chunkIndex * MAX_SERIES_PER_LANE,
        (chunkIndex + 1) * MAX_SERIES_PER_LANE,
      );
      const series: TrendSeriesMeta[] = unitPoints.map((point, index) => ({
        ref: point.ref,
        label: point.seriesLabel,
        deviceName: point.deviceName,
        pointLabel: point.pointLabel,
        unit: point.unit,
        precision: point.precision,
        colorVar: CHART_COLOR_VARS[index] ?? CHART_COLOR_VARS[0],
        isSetpoint: point.isSetpoint,
      }));

      const rows: TrendGaugeRow[] = input.window.slots.map((slot) => {
        const values: Record<string, TrendGaugeCell> = {};
        for (const point of unitPoints) {
          const bucket = currentMap.get(point.ref)?.get(slot.startMs);
          const compareBucket = compareMap.get(point.ref)?.get(slot.startMs);
          const primaryValue = bucket?.gauge
            ? point.isSetpoint
              ? bucket.gauge.last
              : bucket.gauge.average
            : null;
          const minimum = bucket?.gauge ? bucket.gauge.minimum : null;
          const maximum = bucket?.gauge ? bucket.gauge.maximum : null;
          const compareValue = compareBucket?.gauge
            ? point.isSetpoint
              ? compareBucket.gauge.last
              : compareBucket.gauge.average
            : null;
          values[point.ref] = {
            value: primaryValue,
            minimum,
            maximum,
            band: minimum !== null && maximum !== null && !point.isSetpoint ? [minimum, maximum] : null,
            compareValue,
          };
        }
        return {
          at: slot.startMs,
          endMs: slot.endMs,
          values,
        };
      });

      const chunkSuffix = chunkCount > 1 ? ` · ${chunkIndex + 1}` : '';
      const title = unit ? `${baseTitle}${chunkSuffix} (${unit})` : `${baseTitle}${chunkSuffix}`;
      const baseId = unit || 'dimensionless';
      gaugeLanes.push({
        id: chunkCount > 1 ? `${baseId}-${chunkIndex + 1}` : baseId,
        unit,
        title,
        series,
        rows,
      });
    }
  }

  const totalBuckets = input.window.slots.length;
  const summaryRows: TrendSummaryRow[] = gaugePoints.map((point) => {
    let latest: number | null = null;
    let weightedSum = 0;
    let totalWeight = 0;
    let minimum: TrendSummaryExtreme | null = null;
    let maximum: TrendSummaryExtreme | null = null;
    let validBuckets = 0;

    for (const slot of input.window.slots) {
      const bucket = currentMap.get(point.ref)?.get(slot.startMs);
      if (!bucket?.gauge) continue;
      validBuckets += 1;
      const val = point.isSetpoint ? bucket.gauge.last : bucket.gauge.average;
      latest = val;
      const weight = Math.max(1, bucket.gauge.sampleCount);
      weightedSum += val * weight;
      totalWeight += weight;

      if (!minimum || bucket.gauge.minimum < minimum.value) {
        minimum = { value: bucket.gauge.minimum, at: bucket.periodStart };
      }
      if (!maximum || bucket.gauge.maximum > maximum.value) {
        maximum = { value: bucket.gauge.maximum, at: bucket.periodStart };
      }
    }

    return {
      ref: point.ref,
      label: point.seriesLabel,
      deviceName: point.deviceName,
      pointLabel: point.pointLabel,
      unit: point.unit,
      precision: point.precision,
      isSetpoint: point.isSetpoint,
      latest,
      average: totalWeight > 0 ? weightedSum / totalWeight : null,
      minimum,
      maximum,
      validBuckets,
      totalBuckets,
      coverageRatio: totalBuckets > 0 ? validBuckets / totalBuckets : 0,
    };
  });

  const spanMs = Math.max(1, input.window.toMs - input.window.fromMs);
  const stateTracks: TrendStateTrack[] = statePoints.map((point) => {
    let latestState: TrendNormalizedState | null = null;
    let runningSamples = 0;
    let totalSamples = 0;

    const slots: TrendStateSlot[] = input.window.slots.map((slot) => {
      const bucket = currentMap.get(point.ref)?.get(slot.startMs);
      if (!bucket?.state) {
        return {
          startMs: slot.startMs,
          endMs: slot.endMs,
          state: null,
        };
      }
      const projected = projectStateBucket(bucket.state);
      if (projected.latest) {
        latestState = projected.latest;
      }
      runningSamples += projected.runningSamples;
      totalSamples += projected.totalSamples;
      return {
        startMs: slot.startMs,
        endMs: slot.endMs,
        state: projected.dominant,
        ...(projected.breakdown ? { breakdown: projected.breakdown } : {}),
      };
    });

    const segments: TrendStateSegment[] = [];
    for (const slot of slots) {
      if (!slot.state) continue;
      const prev = segments[segments.length - 1];
      if (prev && prev.endMs === slot.startMs && prev.kind === slot.state.kind && prev.label === slot.state.label) {
        const nextEndMs = slot.endMs;
        segments[segments.length - 1] = {
          ...prev,
          endMs: nextEndMs,
          widthPercent: ((nextEndMs - prev.startMs) / spanMs) * 100,
        };
      } else {
        segments.push({
          startMs: slot.startMs,
          endMs: slot.endMs,
          kind: slot.state.kind,
          label: slot.state.label,
          startPercent: ((slot.startMs - input.window.fromMs) / spanMs) * 100,
          widthPercent: ((slot.endMs - slot.startMs) / spanMs) * 100,
        });
      }
    }

    return {
      ref: point.ref,
      label: point.seriesLabel,
      deviceName: point.deviceName,
      pointLabel: point.pointLabel,
      slots,
      segments,
      latestState,
      runningRatio: totalSamples > 0 ? runningSamples / totalSamples : 0,
    };
  });

  return {
    gaugeLanes,
    summaryRows,
    statePoints,
    stateTracks,
  };
}

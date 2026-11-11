import type { Asset, Device, SiteAssetModel, TelemetryPoint } from '../../../api/generated/platformGateway.gen.ts';
import type {
  DeviceHistoryAggregateBucket,
  DeviceHistoryStateAggregate,
} from '../../../api/generated/s2Telemetry.gen.ts';
import { formatTelemetryUnit, telemetryPointDefinition } from '../../../domain/centralPlantTelemetry.ts';
import { siteMidnight, wallTime } from '../../energy-analysis/consumption/model.ts';
import { buildTimeSlots, type TimeSlot, type TrendGranularity } from './timeline.ts';

export const MAX_TREND_POINTS = 8;
export const MAX_TREND_BUCKETS_PER_POINT = 720;
const MAX_SERIES_PER_LANE = 4;

export type TrendWindowPreset = '1h' | '6h' | 'today' | '7d' | 'custom';

export type TrendPresetId =
  | 'plant-overview'
  | 'chiller-comparison'
  | 'pump-hydraulics'
  | 'outdoor-load';

export interface TrendPointItem {
  readonly ref: string;
  readonly pointId: string;
  readonly deviceId: string;
  readonly deviceName: string;
  readonly assetId: string | null;
  readonly assetType: string | null;
  readonly pointCode: string;
  readonly sourceKey: string;
  readonly pointLabel: string;
  readonly seriesLabel: string;
  readonly unit: string;
  readonly precision: number;
  readonly kind: 'gauge' | 'state';
  readonly pointType: 'TELEMETRY' | 'SETTING' | 'STATE';
  readonly isSetpoint: boolean;
}

export interface TrendCatalog {
  readonly points: readonly TrendPointItem[];
  readonly byRef: ReadonlyMap<string, TrendPointItem>;
  readonly byDeviceId: ReadonlyMap<string, readonly TrendPointItem[]>;
}

export interface TrendPreset {
  readonly id: TrendPresetId;
  readonly title: string;
  readonly pointRefs: readonly string[];
  readonly unconnectedLabels: readonly string[];
}

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

const ALL_GRANULARITIES: readonly TrendGranularity[] = [
  'MINUTE',
  'FIVE_MINUTE',
  'QUARTER_HOUR',
  'HOUR',
  'DAY',
];

const EXTRA_POINT_LABELS: Readonly<Record<string, string>> = {
  'chiller.leaving_cooling_water_temperature': '冷却水出水温度',
};

const PLANT_LEVEL_PREFIXES = ['hvac_meter.', 'btu_meter.', 'weather.'] as const;

function normalizeTrendUnit(rawUnit: string | null | undefined, fallbackUnit?: string): string {
  const candidate = rawUnit ?? fallbackUnit ?? null;
  if (!candidate) return '';
  if (candidate === 'K') return '°C';
  return formatTelemetryUnit(candidate) ?? '';
}

function resolvePointLabel(point: TelemetryPoint): { label: string; defaultUnit?: string; precision: number } {
  const domain = telemetryPointDefinition(point.sourceKey);
  if (domain.label !== point.sourceKey) {
    return { label: domain.label, defaultUnit: domain.defaultUnit, precision: domain.precision ?? 1 };
  }
  const extra = EXTRA_POINT_LABELS[point.sourceKey];
  if (extra) {
    return { label: extra, defaultUnit: 'Cel', precision: 1 };
  }
  const cleanedDisplay = point.displayName.trim();
  return {
    label: cleanedDisplay || point.pointCode,
    defaultUnit: point.unit ?? undefined,
    precision: point.unit === 'm3/h' || point.unit === '%RH' ? 0 : 1,
  };
}

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

function cleanDeviceDisplayName(name: string): string {
  return name.replace(/\s*通信端点$/u, '').trim() || name;
}

export function buildTrendPointRef(deviceId: string, pointCode: string): string {
  return `${deviceId}:${pointCode}`;
}

export function buildTrendCatalog(model: Pick<SiteAssetModel, 'assets' | 'devices' | 'telemetryPoints' | 'relationships'>): TrendCatalog {
  const assetsById = new Map<string, Asset>(
    model.assets.filter((asset) => asset.status === 'ACTIVE').map((asset) => [asset.id, asset]),
  );
  const devicesById = new Map<string, Device>(
    model.devices.filter((device) => device.status === 'ACTIVE').map((device) => [device.id, device]),
  );
  const boundAssetByDevice = new Map<string, Asset>();
  for (const rel of model.relationships) {
    if (rel.status === 'ACTIVE' && rel.fromType === 'DEVICE' && rel.toType === 'ASSET') {
      const asset = assetsById.get(rel.toId);
      if (asset && !boundAssetByDevice.has(rel.fromId)) {
        boundAssetByDevice.set(rel.fromId, asset);
      }
    }
  }

  const items: TrendPointItem[] = [];
  for (const point of model.telemetryPoints) {
    if (point.status !== 'ACTIVE') continue;
    const device = devicesById.get(point.reportingDeviceId);
    if (!device || device.deviceType === 'GATEWAY') continue;

    const isGauge =
      (point.pointType === 'TELEMETRY' || point.pointType === 'SETTING') &&
      point.valueType === 'NUMBER';
    const isState =
      point.pointType === 'STATE' ||
      ((point.pointType === 'TELEMETRY' || point.pointType === 'SETTING') &&
        (point.valueType === 'STRING' || point.valueType === 'BOOLEAN'));

    if (!isGauge && !isState) continue;
    if (isState && point.sourceKey.endsWith('.fault_code')) continue;

    const boundAsset = boundAssetByDevice.get(device.id) ?? null;
    const deviceName = boundAsset ? boundAsset.displayName : cleanDeviceDisplayName(device.displayName);
    const resolved = resolvePointLabel(point);
    const unit = isGauge ? normalizeTrendUnit(point.unit, resolved.defaultUnit) : '';
    const isPlantWide = PLANT_LEVEL_PREFIXES.some((prefix) => point.sourceKey.startsWith(prefix));
    const seriesLabel = isPlantWide ? resolved.label : `${deviceName} · ${resolved.label}`;

    items.push({
      ref: buildTrendPointRef(device.id, point.pointCode),
      pointId: point.id,
      deviceId: device.id,
      deviceName,
      assetId: boundAsset?.id ?? null,
      assetType: boundAsset?.assetType ?? null,
      pointCode: point.pointCode,
      sourceKey: point.sourceKey,
      pointLabel: resolved.label,
      seriesLabel,
      unit,
      precision: resolved.precision,
      kind: isGauge ? 'gauge' : 'state',
      pointType: point.pointType as 'TELEMETRY' | 'SETTING' | 'STATE',
      isSetpoint: point.pointType === 'SETTING',
    });
  }

  items.sort((left, right) => {
    const devCmp = left.deviceName.localeCompare(right.deviceName, 'zh-CN');
    if (devCmp !== 0) return devCmp;
    if (left.kind !== right.kind) return left.kind === 'gauge' ? -1 : 1;
    return left.pointLabel.localeCompare(right.pointLabel, 'zh-CN');
  });

  const byRef = new Map<string, TrendPointItem>();
  const byDeviceId = new Map<string, TrendPointItem[]>();
  for (const item of items) {
    byRef.set(item.ref, item);
    const list = byDeviceId.get(item.deviceId) ?? [];
    list.push(item);
    byDeviceId.set(item.deviceId, list);
  }

  return { points: items, byRef, byDeviceId };
}

export function findFirstBySourceKey(items: readonly TrendPointItem[], sourceKey: string): TrendPointItem | undefined {
  return items.find((item) => item.sourceKey === sourceKey);
}

export function findAllBySourceKey(items: readonly TrendPointItem[], sourceKey: string): readonly TrendPointItem[] {
  return items.filter((item) => item.sourceKey === sourceKey);
}

export function deriveBuiltInPresets(catalog: TrendCatalog): readonly TrendPreset[] {
  const { points } = catalog;
  const presets: TrendPreset[] = [];

  // 1. 冷站功率与供回水 (default)
  {
    const refs: string[] = [];
    const unconnected: string[] = [];
    const totalPower = findFirstBySourceKey(points, 'hvac_meter.active_power');
    const supplyTemp =
      findFirstBySourceKey(points, 'btu_meter.supply_water_temperature') ??
      findFirstBySourceKey(points, 'chiller.leaving_chilled_water_temperature');
    const returnTemp =
      findFirstBySourceKey(points, 'btu_meter.return_water_temperature') ??
      findFirstBySourceKey(points, 'chiller.entering_chilled_water_temperature');
    const setpoint = findFirstBySourceKey(points, 'chiller.chilled_water_temperature_setpoint');

    if (totalPower) refs.push(totalPower.ref);
    else unconnected.push('中央空调总功率');

    if (supplyTemp) refs.push(supplyTemp.ref);
    else unconnected.push('供水温度');

    if (returnTemp && !refs.includes(returnTemp.ref)) refs.push(returnTemp.ref);
    else if (!returnTemp) unconnected.push('回水温度');

    if (setpoint && refs.length < MAX_TREND_POINTS) refs.push(setpoint.ref);

    const gaugeCount = refs.length;
    for (const stateItem of findAllBySourceKey(points, 'chiller.run_state')) {
      if (refs.length >= MAX_TREND_POINTS) break;
      refs.push(stateItem.ref);
    }

    if (gaugeCount > 0) {
      presets.push({
        id: 'plant-overview',
        title: '冷站功率与供回水',
        pointRefs: refs.slice(0, MAX_TREND_POINTS),
        unconnectedLabels: unconnected,
      });
    }
  }

  // 2. 各冷机运行对比
  {
    const refs: string[] = [];
    const unconnected: string[] = [];
    const leavingTemps = findAllBySourceKey(points, 'chiller.leaving_chilled_water_temperature');
    const powers = findAllBySourceKey(points, 'chiller.power');
    const runStates = findAllBySourceKey(points, 'chiller.run_state');

    for (const item of leavingTemps.slice(0, 4)) {
      if (refs.length < MAX_TREND_POINTS) refs.push(item.ref);
    }
    if (leavingTemps.length === 0) unconnected.push('冷冻水出水温度');

    for (const item of powers.slice(0, 4)) {
      if (refs.length < MAX_TREND_POINTS) refs.push(item.ref);
    }
    if (powers.length === 0) unconnected.push('主机功率');

    if (leavingTemps.length === 1) {
      const setpoint = findFirstBySourceKey(points, 'chiller.chilled_water_temperature_setpoint');
      if (setpoint && refs.length < MAX_TREND_POINTS) refs.push(setpoint.ref);
    }
    for (const stateItem of runStates) {
      if (refs.length >= MAX_TREND_POINTS) break;
      refs.push(stateItem.ref);
    }

    const hasChillerGauge = leavingTemps.length > 0 || powers.length > 0;
    if (hasChillerGauge) {
      presets.push({
        id: 'chiller-comparison',
        title: '各冷机运行对比',
        pointRefs: refs.slice(0, MAX_TREND_POINTS),
        unconnectedLabels: unconnected,
      });
    }
  }

  // 3. 水泵流量与温差
  {
    const refs: string[] = [];
    const unconnected: string[] = [];
    const flow =
      findFirstBySourceKey(points, 'btu_meter.flow_rate') ??
      findFirstBySourceKey(points, 'chwp.flow_rate');
    const deltaT = findFirstBySourceKey(points, 'btu_meter.temperature_difference');
    const chwpFreq = findAllBySourceKey(points, 'chwp.frequency');
    const cwpFreq = findAllBySourceKey(points, 'cwp.frequency');
    const pumpRunStates = [
      ...findAllBySourceKey(points, 'chwp.run_state'),
      ...findAllBySourceKey(points, 'cwp.run_state'),
    ];

    if (flow) refs.push(flow.ref);
    else unconnected.push('冷冻水流量');

    if (deltaT) refs.push(deltaT.ref);
    else unconnected.push('供回水温差');

    const freqs = [...chwpFreq, ...cwpFreq];
    for (const item of freqs) {
      if (refs.length < MAX_TREND_POINTS) refs.push(item.ref);
    }
    if (freqs.length === 0) unconnected.push('水泵频率');

    const gaugeCount = refs.length;
    for (const stateItem of pumpRunStates) {
      if (refs.length >= MAX_TREND_POINTS) break;
      refs.push(stateItem.ref);
    }

    if (gaugeCount > 0) {
      presets.push({
        id: 'pump-hydraulics',
        title: '水泵流量与温差',
        pointRefs: refs.slice(0, MAX_TREND_POINTS),
        unconnectedLabels: unconnected,
      });
    }
  }

  // 4. 室外环境与供冷负荷
  {
    const refs: string[] = [];
    const unconnected: string[] = [];
    const dryBulb = findFirstBySourceKey(points, 'weather.ambient_dry_bulb_temperature');
    const wetBulb = findFirstBySourceKey(points, 'weather.ambient_wet_bulb_temperature');
    const coolingCap = findFirstBySourceKey(points, 'btu_meter.instant_cooling_capacity');
    const activePower = findFirstBySourceKey(points, 'hvac_meter.active_power');

    if (dryBulb) refs.push(dryBulb.ref);
    else unconnected.push('室外干球温度');

    if (wetBulb) refs.push(wetBulb.ref);
    else unconnected.push('室外湿球温度');

    if (coolingCap) refs.push(coolingCap.ref);
    else unconnected.push('瞬时制冷量');

    if (activePower && refs.length < MAX_TREND_POINTS) refs.push(activePower.ref);

    if (dryBulb || wetBulb) {
      presets.push({
        id: 'outdoor-load',
        title: '室外环境与供冷负荷',
        pointRefs: refs.slice(0, MAX_TREND_POINTS),
        unconnectedLabels: unconnected,
      });
    }
  }

  return presets;
}

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

function autoGranularityForSpan(window: TrendWindowPreset, spanMs: number): TrendGranularity {
  if (window === '1h') return 'MINUTE';
  if (window === '6h') return 'FIVE_MINUTE';
  if (window === '7d') return 'HOUR';
  if (window === 'today') {
    if (spanMs <= 2 * HOUR_MS) return 'MINUTE';
    if (spanMs <= 8 * HOUR_MS) return 'FIVE_MINUTE';
    return 'QUARTER_HOUR';
  }
  if (spanMs <= 2 * HOUR_MS) return 'MINUTE';
  if (spanMs <= 12 * HOUR_MS) return 'FIVE_MINUTE';
  if (spanMs <= 48 * HOUR_MS) return 'QUARTER_HOUR';
  if (spanMs <= 30 * DAY_MS) return 'HOUR';
  return 'DAY';
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

  const allowedGranularities = ALL_GRANULARITIES.filter((candidate) => {
    const count = buildTimeSlots({
      fromMs,
      toMs,
      granularity: candidate,
      timeZone: input.timezone,
    }).length;
    return count >= 1 && count <= MAX_TREND_BUCKETS_PER_POINT;
  });

  const preferred =
    input.granularity && input.granularity !== 'auto'
      ? input.granularity
      : autoGranularityForSpan(preset, toMs - fromMs);

  const granularity = allowedGranularities.includes(preferred)
    ? preferred
    : (allowedGranularities[0] ?? 'DAY');

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

export function groupTrendDeviceRequests(
  points: readonly TrendPointItem[],
): readonly { readonly deviceId: string; readonly keys: readonly string[] }[] {
  const capped = points.slice(0, MAX_TREND_POINTS);
  const byDevice = new Map<string, Set<string>>();
  for (const point of capped) {
    const set = byDevice.get(point.deviceId) ?? new Set<string>();
    set.add(point.pointCode);
    byDevice.set(point.deviceId, set);
  }
  return [...byDevice.entries()].map(([deviceId, keys]) => ({
    deviceId,
    keys: [...keys],
  }));
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

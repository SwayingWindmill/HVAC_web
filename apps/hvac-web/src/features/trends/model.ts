import type { TelemetryPoint } from '@/api/generated/platformGateway.gen';
import type {
  DeviceHistoryObservation,
  DeviceHistoryResponse,
  S2TelemetryClient,
} from '@/api/generated/s2Telemetry.gen';

export type TrendRangePreset = '1h' | '6h' | '24h';

export interface TrendWindow {
  readonly from: string;
  readonly to: string;
  readonly label: string;
  readonly preset: TrendRangePreset | 'custom';
}

export interface TrendHistoryRequest {
  readonly deviceId: string;
  readonly keys: readonly string[];
  readonly from: string;
  readonly to: string;
}

export interface TrendPointDatum {
  readonly x: number;
  readonly value: number | null;
  readonly observation: DeviceHistoryObservation | null;
}

const RANGE_MILLISECONDS: Readonly<Record<TrendRangePreset, number>> = Object.freeze({
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
});

export function createTrendWindow(preset: TrendRangePreset, anchor = Date.now()): TrendWindow {
  const to = new Date(anchor);
  const from = new Date(anchor - RANGE_MILLISECONDS[preset]);
  return {
    from: from.toISOString(),
    to: to.toISOString(),
    label: preset === '1h' ? '最近 1 小时' : preset === '6h' ? '最近 6 小时' : '最近 24 小时',
    preset,
  };
}

export function resolveTrendWindow(timeStart: string | undefined, timeEnd: string | undefined): TrendWindow {
  if (!timeStart || !timeEnd) return createTrendWindow('6h');
  const from = Date.parse(timeStart);
  const to = Date.parse(timeEnd);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from || to - from > RANGE_MILLISECONDS['24h']) {
    return createTrendWindow('6h');
  }
  const duration = to - from;
  const exactPreset = (Object.entries(RANGE_MILLISECONDS) as [TrendRangePreset, number][])
    .find(([, milliseconds]) => Math.abs(duration - milliseconds) < 1000)?.[0] ?? 'custom';
  return {
    from: new Date(from).toISOString(),
    to: new Date(to).toISOString(),
    label: exactPreset === 'custom' ? '自定义证据窗口' : exactPreset === '1h' ? '最近 1 小时' : exactPreset === '6h' ? '最近 6 小时' : '最近 24 小时',
    preset: exactPreset,
  };
}

export function parseTrendSeries(value: string | undefined): readonly string[] {
  if (!value) return [];
  return [...new Set(value.split(',').map((item) => item.trim()).filter(Boolean))].slice(0, 4);
}

export function serializeTrendSeries(ids: readonly string[]): string | undefined {
  const unique = [...new Set(ids)].slice(0, 4);
  return unique.length > 0 ? unique.join(',') : undefined;
}

export function buildTrendHistoryRequests(points: readonly TelemetryPoint[], window: TrendWindow): readonly TrendHistoryRequest[] {
  const grouped = new Map<string, string[]>();
  for (const point of points) {
    const keys = grouped.get(point.reportingDeviceId) ?? [];
    if (!keys.includes(point.sourceKey)) keys.push(point.sourceKey);
    grouped.set(point.reportingDeviceId, keys);
  }
  return [...grouped.entries()].map(([deviceId, keys]) => ({ deviceId, keys, from: window.from, to: window.to }));
}

export async function loadTrendHistory(
  client: S2TelemetryClient,
  request: TrendHistoryRequest,
  csrfToken: string,
  expectedScope: { readonly tenantId: string; readonly siteId: string },
  signal?: AbortSignal,
): Promise<DeviceHistoryResponse> {
  const observations: DeviceHistoryObservation[] = [];
  let cursor: string | undefined;
  let projectionWatermark: string | null = null;
  for (let page = 0; page < 20; page += 1) {
    const response = await client.queryDeviceHistory({
      deviceId: request.deviceId,
      keys: [...request.keys],
      from: request.from,
      to: request.to,
      pageSize: 500,
      ...(cursor ? { cursor } : {}),
    }, { csrfToken, signal });
    if (response.tenantId !== expectedScope.tenantId || response.siteId !== expectedScope.siteId || response.deviceId !== request.deviceId) {
      throw new Error('趋势历史响应超出当前授权范围。');
    }
    if (response.metadata.requestedFrom !== request.from || response.metadata.requestedTo !== request.to) {
      throw new Error('趋势历史响应的时间范围与请求不一致。');
    }
    observations.push(...response.observations);
    projectionWatermark = response.metadata.projectionWatermark;
    cursor = response.metadata.nextCursor ?? undefined;
    if (!cursor) {
      return {
        ...response,
        observations,
        metadata: {
          ...response.metadata,
          returnedObservations: observations.length,
          nextCursor: null,
          projectionWatermark,
        },
      };
    }
  }
  throw new Error('趋势历史分页超过允许范围。');
}

export function observationsForPoint(
  responses: readonly DeviceHistoryResponse[],
  point: TelemetryPoint,
): readonly DeviceHistoryObservation[] {
  return responses
    .flatMap((response) => response.deviceId === point.reportingDeviceId ? response.observations : [])
    .filter((observation) => observation.telemetryKey === point.sourceKey)
    .sort((left, right) => Date.parse(left.sampledAt) - Date.parse(right.sampledAt));
}

function numericObservationValue(observation: DeviceHistoryObservation): number | null {
  if (observation.valueType === 'NUMBER' && typeof observation.value === 'number' && Number.isFinite(observation.value)) return observation.value;
  if (observation.valueType === 'BOOLEAN' && typeof observation.value === 'boolean') return observation.value ? 1 : 0;
  return null;
}

export function buildTrendData(point: TelemetryPoint, observations: readonly DeviceHistoryObservation[]): readonly TrendPointDatum[] {
  const result: TrendPointDatum[] = [];
  let previousAt: number | null = null;
  for (const observation of observations) {
    const timestamp = Date.parse(observation.sampledAt);
    const value = numericObservationValue(observation);
    if (!Number.isFinite(timestamp) || value === null) continue;
    if (previousAt !== null && timestamp - previousAt > point.sampleIntervalMs * 3) {
      result.push({ x: previousAt + Math.floor((timestamp - previousAt) / 2), value: null, observation: null });
    }
    result.push({ x: timestamp, value, observation });
    previousAt = timestamp;
  }
  return result;
}

export function trendInterpolation(point: TelemetryPoint): 'monotone' | 'linear' | 'stepAfter' {
  if (point.pointType === 'STATE' || point.pointType === 'SETTING' || point.pointType === 'COMMAND' || point.valueType === 'BOOLEAN') return 'stepAfter';
  return 'linear';
}

export function pointDisplayUnit(point: TelemetryPoint): string {
  if (point.valueType === 'BOOLEAN') return '状态 0/1';
  return point.unit ?? '无单位';
}

export function unitGroupKey(point: TelemetryPoint): string {
  return pointDisplayUnit(point);
}

export function formatTrendValue(observation: DeviceHistoryObservation | undefined, point: TelemetryPoint): string {
  if (!observation) return '—';
  if (observation.valueType === 'BOOLEAN' && typeof observation.value === 'boolean') return observation.value ? '开启' : '关闭';
  if (observation.valueType === 'NUMBER' && typeof observation.value === 'number') {
    const value = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(observation.value);
    return point.unit ? `${value} ${point.unit}` : value;
  }
  if (observation.valueType === 'STRING' && typeof observation.value === 'string') return observation.value;
  return observation.valueType === 'JSON' ? '结构化状态' : '—';
}

export function formatTrendInstant(value: string | number, timezone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

export function qualityLabel(value: DeviceHistoryObservation['quality']): string {
  switch (value) {
    case 'GOOD': return '良好';
    case 'PARTIAL': return '部分可信';
    case 'ESTIMATED': return '估算';
    case 'MANUAL': return '人工';
    case 'STALE': return '陈旧';
    case 'INVALID': return '无效';
  }
}

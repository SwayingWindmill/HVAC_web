import { S2TelemetryClientError } from '../../api/generated/s2Telemetry.gen.ts';
import type { DeviceHistoryPoint, S2TelemetryClient } from '../../api/generated/s2Telemetry.gen.ts';
import type * as S2TelemetryContract from '../../api/generated/s2Telemetry.gen.ts';
import {
  REAL_ASSETS_CATALOG_REVISION,
  type RealAssetsPointDefinition,
  type RealAssetsProfileResolution,
} from './catalog.ts';

export type RealAssetsHistoryRange = '1h' | '6h' | '24h';

export interface RealAssetsHistoryRangeDefinition {
  readonly label: string;
  readonly milliseconds: number;
  readonly maxPointsPerKey: number;
}

export const REAL_ASSETS_HISTORY_RANGES: Readonly<Record<RealAssetsHistoryRange, RealAssetsHistoryRangeDefinition>> = Object.freeze({
  '1h': Object.freeze({ label: '最近 1 小时', milliseconds: 60 * 60 * 1000, maxPointsPerKey: 240 }),
  '6h': Object.freeze({ label: '最近 6 小时', milliseconds: 6 * 60 * 60 * 1000, maxPointsPerKey: 360 }),
  '24h': Object.freeze({ label: '最近 24 小时', milliseconds: 24 * 60 * 60 * 1000, maxPointsPerKey: 500 }),
});

export interface RealAssetsHistoryQuery {
  readonly protectedGeneration: number;
  readonly sessionId: string;
  readonly actingOrganizationId: string;
  readonly owningOrganizationId: string;
  readonly siteId: string;
  readonly deviceId: string;
  readonly keys: readonly string[];
  readonly range: RealAssetsHistoryRange;
  readonly aggregation: 'RAW';
  readonly timezone: string;
  readonly catalogRevision: string;
  readonly routePolicyRevision: string;
  readonly from: string;
  readonly to: string;
  readonly maxPointsPerKey: number;
}

export interface LoadRealAssetsHistoryInput {
  readonly client: S2TelemetryClient;
  readonly query: RealAssetsHistoryQuery;
  readonly sessionCapability: string;
  readonly signal: AbortSignal;
}

export interface RealAssetsTrendDatum {
  readonly timestamp: number;
  readonly value: number | null;
  readonly quality: 'GOOD' | 'SUSPECT' | null;
  readonly pointId: string | null;
  readonly sensorId: string | null;
}

export interface RealAssetsHistoryFailure {
  readonly kind: 'not-visible' | 'unavailable' | 'timeout' | 'authorization';
  readonly title: string;
  readonly detail: string;
  readonly retryable: boolean;
  readonly traceId?: string;
}

function parseInstant(value: string, field: string): number {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) throw new Error(`Invalid Device history ${field}.`);
  return parsed;
}

function assertUnique(values: readonly string[], field: string): void {
  if (new Set(values).size !== values.length) throw new Error(`Device history ${field} must be unique.`);
}

function assertExactStringArray(actual: readonly string[], expected: readonly string[], field: string): void {
  if (actual.length !== expected.length || actual.some((value, index) => value !== expected[index])) {
    throw new Error(`Device history ${field} drifted from the requested order.`);
  }
}

const UUID_V7_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function validateHistoricalIdentity(point: DeviceHistoryPoint): void {
  if (!UUID_V7_PATTERN.test(point.pointId)) throw new Error('Device history Point identity is invalid.');
  if (point.sensorId !== null && !UUID_V7_PATTERN.test(point.sensorId)) throw new Error('Device history Sensor identity is invalid.');
}

function validatePoint(point: DeviceHistoryPoint, fromMs: number, toMs: number, previousSampledAt: number | null): number {
  validateHistoricalIdentity(point);
  const sampledAt = parseInstant(point.sampledAt, 'sampledAt');
  parseInstant(point.receivedAt, 'receivedAt');
  if (sampledAt < fromMs || sampledAt >= toMs) throw new Error('Device history point escaped the requested range.');
  if (previousSampledAt !== null && sampledAt < previousSampledAt) throw new Error('Device history points are not ordered.');
  if (!Number.isFinite(point.value)) throw new Error('Device history point value must be finite.');
  if (point.quality !== 'GOOD' && point.quality !== 'SUSPECT') throw new Error('Device history point quality is unsupported.');
  if (!Number.isInteger(point.revision) || point.revision <= 0) throw new Error('Device history point revision is invalid.');
  if (!Array.isArray(point.qualityReasons)) throw new Error('Device history quality reasons are invalid.');
  return sampledAt;
}

export function listRealAssetsTrendDefinitions(profile: RealAssetsProfileResolution): readonly RealAssetsPointDefinition[] {
  if (profile.state !== 'configured') return [];
  return profile.profile.points.filter((definition) => definition.showInDetail && definition.trendEligible);
}

export function createRealAssetsHistoryQuery(input: {
  readonly protectedGeneration: number;
  readonly sessionId: string;
  readonly actingOrganizationId: string;
  readonly owningOrganizationId: string;
  readonly siteId: string;
  readonly deviceId: string;
  readonly keys: readonly string[];
  readonly range: RealAssetsHistoryRange;
  readonly timezone: string;
  readonly routePolicyRevision: string | null;
  readonly asOf: number;
}): RealAssetsHistoryQuery {
  const rangeDefinition = REAL_ASSETS_HISTORY_RANGES[input.range];
  if (!Number.isFinite(input.asOf)) throw new Error('Device history anchor must be finite.');
  if (input.keys.length === 0 || input.keys.length > 8) throw new Error('Device history requires between 1 and 8 keys.');
  assertUnique(input.keys, 'keys');
  const to = new Date(input.asOf).toISOString();
  const from = new Date(input.asOf - rangeDefinition.milliseconds).toISOString();
  return Object.freeze({
    protectedGeneration: input.protectedGeneration,
    sessionId: input.sessionId,
    actingOrganizationId: input.actingOrganizationId,
    owningOrganizationId: input.owningOrganizationId,
    siteId: input.siteId,
    deviceId: input.deviceId,
    keys: Object.freeze([...input.keys]),
    range: input.range,
    aggregation: 'RAW',
    timezone: input.timezone,
    catalogRevision: REAL_ASSETS_CATALOG_REVISION,
    routePolicyRevision: input.routePolicyRevision ?? 'unavailable',
    from,
    to,
    maxPointsPerKey: rangeDefinition.maxPointsPerKey,
  });
}

export function realAssetsHistoryQueryKey(query: RealAssetsHistoryQuery): readonly unknown[] {
  return [
    'real-assets', query.protectedGeneration, query.actingOrganizationId, query.owningOrganizationId, query.siteId, 'history',
    query.sessionId, query.deviceId, [...query.keys], query.range, query.aggregation, query.timezone,
    query.catalogRevision, query.routePolicyRevision, query.from, query.to, query.maxPointsPerKey,
  ] as const;
}

export function realAssetsHistoryRevisionKey(query: RealAssetsHistoryQuery, response: S2TelemetryContract.DeviceHistoryResponse): readonly unknown[] {
  return [
    ...realAssetsHistoryQueryKey(query),
    'revision', response.metadata.datasetRevision, response.metadata.dataWatermark ?? 'no-watermark',
  ] as const;
}

export function validateRealAssetsHistoryResponse(response: S2TelemetryContract.DeviceHistoryResponse, query: RealAssetsHistoryQuery): S2TelemetryContract.DeviceHistoryResponse {
  if (response.schemaVersion !== 1) throw new Error('Device history schema version is unsupported.');
  if (response.owningOrganizationId !== query.owningOrganizationId
    || response.siteId !== query.siteId
    || response.deviceId !== query.deviceId) {
    throw new Error('Device history response escaped the authorized resource scope.');
  }
  assertExactStringArray(response.series.map((series) => series.key), query.keys, 'series');
  const fromMs = parseInstant(query.from, 'requestedFrom');
  const toMs = parseInstant(query.to, 'requestedTo');
  if (response.metadata.requestedFrom !== query.from || response.metadata.requestedTo !== query.to) {
    throw new Error('Device history response range drifted from the request.');
  }
  if (response.metadata.maxPointsPerKey !== query.maxPointsPerKey) {
    throw new Error('Device history point limit drifted from the request.');
  }
  if (!response.metadata.datasetRevision.trim()) throw new Error('Device history dataset revision is missing.');
  if (response.metadata.dataWatermark !== null) parseInstant(response.metadata.dataWatermark, 'dataWatermark');
  assertUnique(response.metadata.truncatedKeys, 'truncatedKeys');
  if (response.metadata.truncatedKeys.some((key) => !query.keys.includes(key))) {
    throw new Error('Device history response truncated an unrequested key.');
  }

  let returnedPoints = 0;
  const observationIds = new Set<string>();
  for (const series of response.series) {
    if (series.points.length > query.maxPointsPerKey) throw new Error('Device history series exceeded the requested point limit.');
    let previousSampledAt: number | null = null;
    for (const point of series.points) {
      if (observationIds.has(point.observationId)) throw new Error('Device history observation IDs must be unique.');
      observationIds.add(point.observationId);
      previousSampledAt = validatePoint(point, fromMs, toMs, previousSampledAt);
      returnedPoints += 1;
    }
  }
  if (response.metadata.returnedPoints !== returnedPoints) throw new Error('Device history returned point count is inconsistent.');
  if (returnedPoints > query.keys.length * query.maxPointsPerKey) throw new Error('Device history response exceeded the total point limit.');
  if (response.metadata.truncatedKeys.length > 0 && !response.metadata.partial) {
    throw new Error('Truncated Device history must be marked partial.');
  }
  return response;
}

export async function loadRealAssetsHistory(input: LoadRealAssetsHistoryInput): Promise<S2TelemetryContract.DeviceHistoryResponse> {
  const options = {
    ['csrf' + 'Token']: input.sessionCapability,
    signal: input.signal,
  };
  const response = await input.client.queryDeviceHistory({
    deviceId: input.query.deviceId,
    keys: [...input.query.keys],
    from: input.query.from,
    to: input.query.to,
    maxPointsPerKey: input.query.maxPointsPerKey,
  }, options);
  return validateRealAssetsHistoryResponse(response, input.query);
}

export function buildRealAssetsTrendData(
  points: readonly DeviceHistoryPoint[],
  range: RealAssetsHistoryRange,
  maxPointsPerKey: number,
): readonly RealAssetsTrendDatum[] {
  if (points.length === 0) return [];
  const rangeMs = REAL_ASSETS_HISTORY_RANGES[range].milliseconds;
  const expectedSpacing = rangeMs / Math.max(1, maxPointsPerKey);
  const gapThreshold = Math.max(60_000, expectedSpacing * 3);
  const result: RealAssetsTrendDatum[] = [];
  let previousTimestamp: number | null = null;
  let previousIdentity: string | null = null;
  for (const point of points) {
    const timestamp = parseInstant(point.sampledAt, 'sampledAt');
    const identity = `${point.pointId}:${point.sensorId ?? 'no-sensor'}`;
    if (previousTimestamp !== null && (timestamp - previousTimestamp > gapThreshold || identity !== previousIdentity)) {
      result.push(Object.freeze({
        timestamp: previousTimestamp + Math.floor((timestamp - previousTimestamp) / 2),
        value: null,
        quality: null,
        pointId: null,
        sensorId: null,
      }));
    }
    result.push(Object.freeze({
      timestamp,
      value: point.value,
      quality: point.quality,
      pointId: point.pointId,
      sensorId: point.sensorId,
    }));
    previousTimestamp = timestamp;
    previousIdentity = identity;
  }
  return Object.freeze(result);
}

export function historySeriesUnit(points: readonly DeviceHistoryPoint[], fallback?: string): string {
  const units = [...new Set(points.map((point) => point.unit).filter((unit): unit is string => Boolean(unit)))];
  if (units.length === 1) return units[0];
  if (units.length > 1) return 'mixed';
  return fallback ?? '无单位';
}

export function formatRealAssetsHistoryInstant(value: string | number | null, timezone: string): string {
  if (value === null) return '不可用';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '不可用';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(date);
}

export function classifyRealAssetsHistoryFailure(error: unknown): RealAssetsHistoryFailure {
  if (error instanceof S2TelemetryClientError) {
    const code = error.problem.code;
    const kind = code === 'RESOURCE_NOT_FOUND'
      ? 'not-visible'
      : code.includes('AUTHORIZATION') || code.includes('FORBIDDEN')
        ? 'authorization'
        : code.includes('TIMEOUT')
          ? 'timeout'
          : 'unavailable';
    return {
      kind,
      title: kind === 'not-visible' ? '设备历史不可见' : kind === 'authorization' ? '历史授权不可用' : kind === 'timeout' ? '历史查询超时' : '短历史暂不可用',
      detail: kind === 'not-visible'
        ? '当前授权范围无法验证该 Device 的历史数据。页面不会确认对象是否存在。'
        : error.problem.detail,
      retryable: error.problem.retryable,
      traceId: error.problem.traceId,
    };
  }
  return {
    kind: 'unavailable',
    title: '短历史连接失败',
    detail: '权威当前状态与 Registry 身份仍然可用；系统不会用历史最后一点冒充当前事实。',
    retryable: true,
  };
}

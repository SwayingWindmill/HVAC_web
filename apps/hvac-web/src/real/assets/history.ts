import { S2TelemetryClientError } from '../../api/generated/s2Telemetry.gen.ts';
import type { DeviceHistoryObservation, DeviceHistoryQuality, S2TelemetryClient } from '../../api/generated/s2Telemetry.gen.ts';
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
  readonly pageSize: number;
}

export const REAL_ASSETS_HISTORY_RANGES: Readonly<Record<RealAssetsHistoryRange, RealAssetsHistoryRangeDefinition>> = Object.freeze({
  '1h': Object.freeze({ label: '最近 1 小时', milliseconds: 60 * 60 * 1000, pageSize: 240 }),
  '6h': Object.freeze({ label: '最近 6 小时', milliseconds: 6 * 60 * 60 * 1000, pageSize: 360 }),
  '24h': Object.freeze({ label: '最近 24 小时', milliseconds: 24 * 60 * 60 * 1000, pageSize: 500 }),
});

export interface RealAssetsHistoryQuery {
  readonly protectedGeneration: number;
  readonly sessionId: string;
  readonly tenantId: string;
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
  readonly pageSize: number;
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
  readonly quality: DeviceHistoryQuality | null;
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

const UUID_V7_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function validateHistoricalIdentity(observation: DeviceHistoryObservation): void {
  if (!UUID_V7_PATTERN.test(observation.observationId)) throw new Error('Device history Observation identity is invalid.');
  if (!UUID_V7_PATTERN.test(observation.pointId)) throw new Error('Device history Point identity is invalid.');
  if (observation.sensorId !== null && !UUID_V7_PATTERN.test(observation.sensorId)) throw new Error('Device history Sensor identity is invalid.');
  if (!UUID_V7_PATTERN.test(observation.sourcePosition.eventId)) throw new Error('Device history source Event identity is invalid.');
}

function validateObservation(
  observation: DeviceHistoryObservation,
  query: RealAssetsHistoryQuery,
  fromMs: number,
  toMs: number,
  previous: DeviceHistoryObservation | null,
): void {
  validateHistoricalIdentity(observation);
  if (!query.keys.includes(observation.telemetryKey)) throw new Error('Device history observation escaped the requested keys.');
  const sampledAt = parseInstant(observation.sampledAt, 'sampledAt');
  parseInstant(observation.receivedAt, 'receivedAt');
  if (sampledAt < fromMs || sampledAt >= toMs) throw new Error('Device history observation escaped the requested range.');
  if (!Number.isInteger(observation.pointRevision) || observation.pointRevision <= 0) throw new Error('Device history Point revision is invalid.');
  if (!Number.isInteger(observation.sourcePosition.offset) || observation.sourcePosition.offset < 0) throw new Error('Device history source offset is invalid.');
  if (!Array.isArray(observation.qualityReasons)) throw new Error('Device history quality reasons are invalid.');
  if (observation.valueType === 'NUMBER' && (typeof observation.value !== 'number' || !Number.isFinite(observation.value))) {
    throw new Error('Device history numeric value must be finite.');
  }
  if (observation.valueType === 'STRING' && typeof observation.value !== 'string') throw new Error('Device history STRING value is invalid.');
  if (observation.valueType === 'BOOLEAN' && typeof observation.value !== 'boolean') throw new Error('Device history BOOLEAN value is invalid.');
  if (observation.valueType === 'JSON' && (typeof observation.value !== 'object' || observation.value === null)) {
    throw new Error('Device history JSON value must be an object or array.');
  }
  if (previous !== null) {
    const previousSampledAt = parseInstant(previous.sampledAt, 'previousSampledAt');
    const order = previous.telemetryKey.localeCompare(observation.telemetryKey)
      || previousSampledAt - sampledAt
      || previous.observationId.localeCompare(observation.observationId);
    if (order >= 0) throw new Error('Device history observations are not in stable order.');
  }
}

export function listRealAssetsTrendDefinitions(profile: RealAssetsProfileResolution): readonly RealAssetsPointDefinition[] {
  if (profile.state !== 'configured') return [];
  return profile.profile.points.filter((definition) => definition.showInDetail && definition.trendEligible);
}

export function createRealAssetsHistoryQuery(input: {
  readonly protectedGeneration: number;
  readonly sessionId: string;
  readonly tenantId: string;
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
    tenantId: input.tenantId,
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
    pageSize: rangeDefinition.pageSize,
  });
}

export function realAssetsHistoryQueryKey(query: RealAssetsHistoryQuery): readonly unknown[] {
  return [
    'real-assets', query.protectedGeneration, query.tenantId, query.siteId, 'history',
    query.sessionId, query.deviceId, [...query.keys], query.range, query.aggregation, query.timezone,
    query.catalogRevision, query.routePolicyRevision, query.from, query.to, query.pageSize,
  ] as const;
}

export function realAssetsHistoryRevisionKey(query: RealAssetsHistoryQuery, response: S2TelemetryContract.DeviceHistoryResponse): readonly unknown[] {
  return [
    ...realAssetsHistoryQueryKey(query),
    'projection', response.metadata.projectionWatermark ?? 'no-watermark', response.metadata.nextCursor ?? 'complete',
  ] as const;
}

export function validateRealAssetsHistoryResponse(response: S2TelemetryContract.DeviceHistoryResponse, query: RealAssetsHistoryQuery): S2TelemetryContract.DeviceHistoryResponse {
  if (response.schemaVersion !== 2) throw new Error('Device history schema version is unsupported.');
  if (response.tenantId !== query.tenantId || response.siteId !== query.siteId || response.deviceId !== query.deviceId) {
    throw new Error('Device history response escaped the authorized resource scope.');
  }
  const fromMs = parseInstant(query.from, 'requestedFrom');
  const toMs = parseInstant(query.to, 'requestedTo');
  if (response.metadata.requestedFrom !== query.from || response.metadata.requestedTo !== query.to) {
    throw new Error('Device history response range drifted from the request.');
  }
  if (response.metadata.pageSize !== query.pageSize) throw new Error('Device history page size drifted from the request.');
  if (response.metadata.projectionWatermark !== null) parseInstant(response.metadata.projectionWatermark, 'projectionWatermark');
  if (response.observations.length > query.pageSize || response.metadata.returnedObservations !== response.observations.length) {
    throw new Error('Device history returned observation count is inconsistent.');
  }
  const observationIds = new Set<string>();
  let previous: DeviceHistoryObservation | null = null;
  for (const observation of response.observations) {
    if (observationIds.has(observation.observationId)) throw new Error('Device history observation IDs must be unique.');
    observationIds.add(observation.observationId);
    validateObservation(observation, query, fromMs, toMs, previous);
    previous = observation;
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
    pageSize: input.query.pageSize,
  }, options);
  return validateRealAssetsHistoryResponse(response, input.query);
}

export function numericHistoryObservations(
  response: S2TelemetryContract.DeviceHistoryResponse,
  key: string,
): readonly DeviceHistoryObservation[] {
  return response.observations.filter((observation) => observation.telemetryKey === key && observation.valueType === 'NUMBER' && typeof observation.value === 'number');
}

export function buildRealAssetsTrendData(
  observations: readonly DeviceHistoryObservation[],
  range: RealAssetsHistoryRange,
  pageSize: number,
): readonly RealAssetsTrendDatum[] {
  if (observations.length === 0) return [];
  const rangeMs = REAL_ASSETS_HISTORY_RANGES[range].milliseconds;
  const expectedSpacing = rangeMs / Math.max(1, pageSize);
  const gapThreshold = Math.max(60_000, expectedSpacing * 3);
  const result: RealAssetsTrendDatum[] = [];
  let previousTimestamp: number | null = null;
  let previousIdentity: string | null = null;
  for (const observation of observations) {
    if (observation.valueType !== 'NUMBER' || typeof observation.value !== 'number' || !Number.isFinite(observation.value)) continue;
    const timestamp = parseInstant(observation.sampledAt, 'sampledAt');
    const identity = `${observation.pointId}:${observation.sensorId ?? 'no-sensor'}:${observation.pointRevision}`;
    if (previousTimestamp !== null && (timestamp - previousTimestamp > gapThreshold || identity !== previousIdentity)) {
      result.push(Object.freeze({ timestamp: previousTimestamp + Math.floor((timestamp - previousTimestamp) / 2), value: null, quality: null, pointId: null, sensorId: null }));
    }
    result.push(Object.freeze({ timestamp, value: observation.value, quality: observation.quality, pointId: observation.pointId, sensorId: observation.sensorId }));
    previousTimestamp = timestamp;
    previousIdentity = identity;
  }
  return Object.freeze(result);
}

export function historySeriesUnit(observations: readonly DeviceHistoryObservation[], fallback?: string): string {
  const units = [...new Set(observations.map((observation) => observation.unit).filter((unit): unit is string => Boolean(unit)))];
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

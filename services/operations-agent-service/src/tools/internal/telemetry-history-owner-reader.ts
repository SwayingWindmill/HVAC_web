import {
  OwnerReadError,
  type OwnerReadInput,
  type OwnerReadResult,
  type TelemetryHistoryAggregate,
  type TelemetryHistoryBucket,
  type TelemetryHistoryReadRequest,
  type TelemetryHistoryReader,
} from '../../application/index.js';
import {
  createOwnerHeaders,
  fetchOwnerJson,
  hasExactKeys,
  isInstant,
  isNonEmptyString,
  isRecord,
  normalizeOwnerReaderHttpConfig,
  type OwnerReaderHttpConfig,
} from './owner-http.js';

export type TelemetryHistoryOwnerReaderConfig = OwnerReaderHttpConfig;

const responseKeys = ['schemaVersion', 'tenantId', 'siteId', 'deviceId', 'buckets', 'metadata'] as const;
const metadataKeys = [
  'requestedFrom',
  'requestedTo',
  'granularity',
  'timezone',
  'qualityPolicy',
  'projectionWatermark',
  'returnedBuckets',
] as const;
const bucketBaseKeys = [
  'telemetryKey',
  'pointId',
  'pointRevision',
  'pointType',
  'unit',
  'periodStart',
  'periodEnd',
  'quality',
  'completeness',
] as const;
const resultKeyFor = { TELEMETRY: 'gauge', COUNTER: 'counter', STATE: 'state' } as const;
const maximumBuckets = 1_000;
const maximumKeys = 8;
const granularities = new Set(['QUARTER_HOUR', 'HOUR', 'DAY']);

const invalidResponse = (message: string): never => {
  throw new OwnerReadError('OWNER_RESPONSE_INVALID', message);
};

const isFiniteNumber = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);

const sameInstant = (left: unknown, right: string): boolean => (
  isInstant(left) && Date.parse(left) === Date.parse(right)
);

const assertHistoryRequest = (input: OwnerReadInput<TelemetryHistoryReadRequest>): void => {
  const query = input.request.input;
  const { scope } = input.context;
  if (query.tenantId !== scope.tenantId
    || query.siteId !== scope.siteId
    || query.deviceId !== scope.deviceId
    || query.keys.length < 1
    || query.keys.length > maximumKeys
    || new Set(query.keys).size !== query.keys.length
    || !isInstant(query.from)
    || !isInstant(query.to)
    || Date.parse(query.to) <= Date.parse(query.from)
    || !granularities.has(query.granularity)
    || !isNonEmptyString(query.timezone)
    || query.qualityPolicy !== 'VALID_ONLY') {
    throw new OwnerReadError('OWNER_REQUEST_INVALID', 'The Device History request is outside the fixed product contract.');
  }
};

const decodeBucket = (value: unknown, keys: ReadonlySet<string>): TelemetryHistoryBucket => {
  if (!isRecord(value)) return invalidResponse('The Device History bucket is invalid.');
  const pointType = value.pointType;
  if (pointType !== 'TELEMETRY' && pointType !== 'COUNTER' && pointType !== 'STATE') {
    return invalidResponse('The Device History bucket has an unsupported point type.');
  }
  const resultKey = resultKeyFor[pointType];
  const result = value[resultKey];
  if (!hasExactKeys(value, [...bucketBaseKeys, resultKey])
    || typeof value.telemetryKey !== 'string'
    || !keys.has(value.telemetryKey)
    || (value.unit !== null && typeof value.unit !== 'string')
    || !isInstant(value.periodStart)
    || !isInstant(value.periodEnd)
    || Date.parse(value.periodStart) >= Date.parse(value.periodEnd)
    || !isFiniteNumber(value.completeness)
    || value.completeness < 0
    || value.completeness > 1
    || !isRecord(result)) {
    return invalidResponse('The Device History bucket is outside the authorized selection.');
  }
  const base: Omit<TelemetryHistoryBucket, 'gauge' | 'counter' | 'state'> = {
    telemetryKey: value.telemetryKey,
    pointType,
    unit: value.unit,
    periodStart: value.periodStart,
    periodEnd: value.periodEnd,
    completeness: value.completeness,
  };
  if (pointType === 'TELEMETRY') {
    if (!isFiniteNumber(result.average) || !isFiniteNumber(result.minimum) || !isFiniteNumber(result.maximum)) {
      return invalidResponse('The Device History reading aggregate is invalid.');
    }
    return { ...base, gauge: { average: result.average, minimum: result.minimum, maximum: result.maximum } };
  }
  if (pointType === 'COUNTER') {
    if (!isFiniteNumber(result.deltaSum)) return invalidResponse('The Device History counter aggregate is invalid.');
    return { ...base, counter: { deltaSum: result.deltaSum } };
  }
  if (!('lastValue' in result)) return invalidResponse('The Device History state aggregate is invalid.');
  return { ...base, state: { lastValue: result.lastValue } };
};

const decodeHistory = (
  value: unknown,
  query: TelemetryHistoryReadRequest['input'],
): TelemetryHistoryAggregate => {
  if (!isRecord(value)
    || !hasExactKeys(value, responseKeys)
    || value.schemaVersion !== 1
    || value.tenantId !== query.tenantId
    || value.siteId !== query.siteId
    || value.deviceId !== query.deviceId
    || !Array.isArray(value.buckets)
    || value.buckets.length > maximumBuckets
    || !isRecord(value.metadata)) {
    return invalidResponse('The Device History Owner returned an invalid response.');
  }
  const metadata = value.metadata;
  const watermark = metadata.projectionWatermark;
  if (!hasExactKeys(metadata, metadataKeys)
    || !sameInstant(metadata.requestedFrom, query.from)
    || !sameInstant(metadata.requestedTo, query.to)
    || metadata.granularity !== query.granularity
    || metadata.timezone !== query.timezone
    || metadata.qualityPolicy !== query.qualityPolicy
    || metadata.returnedBuckets !== value.buckets.length
    || (watermark !== null && !isInstant(watermark))) {
    return invalidResponse('The Device History metadata contradicts the fixed request.');
  }
  const keys = new Set(query.keys);
  return {
    schemaVersion: 1,
    deviceId: query.deviceId,
    projectionWatermark: watermark,
    buckets: value.buckets.map((bucket) => decodeBucket(bucket, keys)),
  };
};

const qualityFor = (history: TelemetryHistoryAggregate, to: string): OwnerReadResult['quality'] => (
  history.projectionWatermark !== null
    && Date.parse(history.projectionWatermark) >= Date.parse(to)
    && history.buckets.every((bucket) => bucket.completeness === 1)
    ? 'GOOD'
    : 'UNCERTAIN'
);

export const createTelemetryHistoryOwnerReader = (
  input: TelemetryHistoryOwnerReaderConfig,
): TelemetryHistoryReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  return Object.freeze({
    async read(readInput: OwnerReadInput<TelemetryHistoryReadRequest>): Promise<OwnerReadResult> {
      assertHistoryRequest(readInput);
      const payload = await fetchOwnerJson(config, {
        path: '/internal/v1/telemetry/device-history:aggregate',
        method: 'POST',
        headers: createOwnerHeaders(readInput.request.requestId, readInput.context, {
          logicalTool: readInput.request.tool,
          includePolicyRevision: false,
          hasBody: true,
        }),
        body: JSON.stringify(readInput.request.input),
      });
      const history = decodeHistory(payload, readInput.request.input);
      return {
        requestId: readInput.request.requestId,
        owner: 'telemetry-query-service',
        scope: { ...readInput.context.scope },
        revision: `telemetry-history:${history.projectionWatermark ?? 'unprojected'}`,
        quality: qualityFor(history, readInput.request.input.to),
        provenance: 'telemetry-query-service:device-history-aggregate/v1',
        payload: history,
      };
    },
  });
};

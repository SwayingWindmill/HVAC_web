import {
  OwnerReadError,
  type EnergyAnalyticsReadRequest,
  type EnergyAnalyticsReader,
  type EnergyGranularity as OwnerEnergyGranularity,
  type EnergyQualityPolicy as OwnerEnergyQualityPolicy,
  type EnergyQualitySummary,
  type EnergySeries,
  type EnergySeriesMetadata,
  type EnergySeriesPoint,
  type OwnerReadInput,
  type OwnerReadResult,
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

export type EnergyGranularity = OwnerEnergyGranularity;
export type EnergyQualityPolicy = OwnerEnergyQualityPolicy;
export type EnergySeriesPointDto = EnergySeriesPoint;
export type EnergyQualitySummaryDto = EnergyQualitySummary;
export type EnergySeriesMetadataDto = EnergySeriesMetadata;
export type EnergySeriesResponseDto = EnergySeries;

export type EnergyAnalyticsOwnerReaderConfig = OwnerReaderHttpConfig;

const responseKeys = ['schemaVersion', 'points', 'metadata'] as const;
const breakdownResponseKeys = ['schemaVersion', 'points', 'devices', 'metadata'] as const;
const deviceSeriesKeys = ['deviceId', 'points'] as const;
const pointKeys = ['periodStart', 'periodEnd', 'energyKWh'] as const;
const pricedPointKeys = [...pointKeys, 'cost'] as const;
const costKeys = ['amountCNY', 'unpricedKWh', 'periods'] as const;
const periodCostKeys = ['periodCode', 'energyKWh', 'amountCNY'] as const;
const periodCodes = new Set(['SUPER_PEAK', 'PEAK', 'FLAT', 'VALLEY']);
const qualityKeys = ['valid', 'suspect', 'invalid'] as const;
const metadataRequiredKeys = [
  'requestedGranularity',
  'actualGranularity',
  'datasetRevision',
  'partial',
  'qualitySummary',
] as const;
const metadataAllowedKeys = [
  ...metadataRequiredKeys,
  'dataWatermark',
  'aggregateWatermark',
] as const;
const maximumPoints = 10_000;
const maximumRangeMs = 366 * 24 * 60 * 60 * 1_000;
const validGranularities = new Set<EnergyGranularity>(['hour', 'day', 'month']);
const validQualityPolicies = new Set<EnergyQualityPolicy>([
  'VALID_ONLY',
  'VALID_AND_SUSPECT',
]);

const isUuidV7 = (value: string): boolean => (
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value)
);

const isTimezone = (value: string): boolean => {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: value }).format(0);
    return value !== 'Local';
  } catch {
    return false;
  }
};

const isCount = (value: unknown): value is number => (
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
);

const assertEnergyRequest = (
  input: OwnerReadInput<EnergyAnalyticsReadRequest>,
): void => {
  const query = input.request.input;
  const { scope } = input.context;
  const from = Date.parse(query.from);
  const to = Date.parse(query.to);
  if (!isUuidV7(query.tenantId)
    || !isUuidV7(query.siteId)
    || query.tenantId !== scope.tenantId
    || query.siteId !== scope.siteId
    || scope.assetId !== null
    || scope.deviceId !== null
    || (query.energyType !== 'electricity' && query.energyType !== 'cooling')
    || !validGranularities.has(query.granularity)
    || !isNonEmptyString(query.timezone)
    || !isTimezone(query.timezone)
    || !isInstant(query.from)
    || !isInstant(query.to)
    || !Number.isFinite(from)
    || !Number.isFinite(to)
    || to - from < 1
    || to - from > maximumRangeMs
    || !validQualityPolicies.has(query.qualityPolicy)
    || (query.breakdown !== undefined && query.breakdown !== 'device')) {
    throw new OwnerReadError(
      'OWNER_REQUEST_INVALID',
      'The Energy Series READ request is outside the fixed product contract.',
    );
  }
};

const isAmount = (value: unknown): value is number => (
  typeof value === 'number' && Number.isFinite(value) && value >= 0
);

const isCost = (value: unknown): boolean => (
  isRecord(value)
  && hasExactKeys(value, costKeys)
  && isAmount(value.amountCNY)
  && isAmount(value.unpricedKWh)
  && Array.isArray(value.periods)
  && value.periods.every((period) => isRecord(period)
    && hasExactKeys(period, periodCostKeys)
    && typeof period.periodCode === 'string'
    && periodCodes.has(period.periodCode)
    && isAmount(period.energyKWh)
    && isAmount(period.amountCNY))
);

const decodePoint = (
  value: unknown,
  query: EnergyAnalyticsReadRequest['input'],
  previousEnd: number | null,
): { readonly point: EnergySeriesPointDto; readonly end: number } => {
  if (!isRecord(value)
    || !(hasExactKeys(value, pointKeys) || (hasExactKeys(value, pricedPointKeys) && isCost(value.cost)))
    || !isInstant(value.periodStart)
    || !isInstant(value.periodEnd)
    || typeof value.energyKWh !== 'number'
    || !Number.isFinite(value.energyKWh)
    || value.energyKWh < 0) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Energy Series Owner returned an invalid point.',
    );
  }
  const start = Date.parse(value.periodStart);
  const end = Date.parse(value.periodEnd);
  if (start >= end
    || end <= Date.parse(query.from)
    || start >= Date.parse(query.to)
    || (previousEnd !== null && start < previousEnd)) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Energy Series Owner returned an out-of-range or overlapping point.',
    );
  }
  return { point: value as unknown as EnergySeriesPointDto, end };
};

const decodeQualitySummary = (value: unknown): EnergyQualitySummaryDto => {
  if (!isRecord(value)
    || !hasExactKeys(value, qualityKeys)
    || !isCount(value.valid)
    || !isCount(value.suspect)
    || !isCount(value.invalid)) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Energy Series Quality Summary is invalid.',
    );
  }
  return value as unknown as EnergyQualitySummaryDto;
};

const decodeMetadata = (
  value: unknown,
  query: EnergyAnalyticsReadRequest['input'],
): EnergySeriesMetadataDto => {
  if (!isRecord(value)) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Energy Series metadata is invalid.',
    );
  }
  const actualKeys = Object.keys(value);
  if (metadataRequiredKeys.some((key) => !(key in value))
    || actualKeys.some((key) => !metadataAllowedKeys.includes(
      key as typeof metadataAllowedKeys[number],
    ))
    || value.requestedGranularity !== query.granularity
    || value.actualGranularity !== query.granularity
    || !isNonEmptyString(value.datasetRevision)
    || value.datasetRevision.length > 256
    || typeof value.partial !== 'boolean') {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Energy Series metadata contradicts the fixed request.',
    );
  }

  const dataWatermark = value.dataWatermark;
  const aggregateWatermark = value.aggregateWatermark;
  if ((dataWatermark !== undefined && !isInstant(dataWatermark))
    || (aggregateWatermark !== undefined && !isInstant(aggregateWatermark))
    || (dataWatermark !== undefined
      && aggregateWatermark !== undefined
      && dataWatermark !== aggregateWatermark)
    || (value.partial === false
      && (dataWatermark === undefined
        || aggregateWatermark === undefined
        || Date.parse(dataWatermark) < Date.parse(query.to)
        || Date.parse(aggregateWatermark) < Date.parse(query.to)))) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Energy Series watermarks contradict the partial state.',
    );
  }

  const qualitySummary = decodeQualitySummary(value.qualitySummary);
  return {
    requestedGranularity: query.granularity,
    actualGranularity: query.granularity,
    ...(dataWatermark === undefined ? {} : { dataWatermark }),
    ...(aggregateWatermark === undefined ? {} : { aggregateWatermark }),
    datasetRevision: value.datasetRevision,
    partial: value.partial,
    qualitySummary,
  };
};

const decodePoints = (
  values: readonly unknown[],
  query: EnergyAnalyticsReadRequest['input'],
): EnergySeriesPointDto[] => {
  const points: EnergySeriesPointDto[] = [];
  let previousEnd: number | null = null;
  for (const valuePoint of values) {
    const decoded = decodePoint(valuePoint, query, previousEnd);
    points.push(decoded.point);
    previousEnd = decoded.end;
  }
  return points;
};

const decodeDeviceSeries = (
  value: unknown,
  query: EnergyAnalyticsReadRequest['input'],
): NonNullable<EnergySeriesResponseDto['devices']>[number] => {
  if (!isRecord(value)
    || !hasExactKeys(value, deviceSeriesKeys)
    || !isNonEmptyString(value.deviceId)
    || !Array.isArray(value.points)
    || value.points.length > maximumPoints) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Energy Series Owner returned an invalid device series.',
    );
  }
  return { deviceId: value.deviceId, points: decodePoints(value.points, query) };
};

const decodeEnergySeries = (
  value: unknown,
  query: EnergyAnalyticsReadRequest['input'],
): EnergySeriesResponseDto => {
  const breakdown = query.breakdown === 'device';
  if (!isRecord(value)
    || !hasExactKeys(value, breakdown ? breakdownResponseKeys : responseKeys)
    || value.schemaVersion !== 1
    || !Array.isArray(value.points)
    || value.points.length > maximumPoints
    || (breakdown && (!Array.isArray(value.devices) || value.devices.length > maximumPoints))) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Energy Series Owner returned an invalid response.',
    );
  }
  const points = decodePoints(value.points, query);
  const devices = breakdown
    ? (value.devices as unknown[]).map((device) => decodeDeviceSeries(device, query))
    : undefined;
  const metadata = decodeMetadata(value.metadata, query);
  if (points.length === 0 && metadata.partial === false) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'An empty Energy Series must remain partial.',
    );
  }
  return {
    schemaVersion: 1,
    points,
    ...(devices === undefined ? {} : { devices }),
    metadata,
  };
};

const qualityFor = (response: EnergySeriesResponseDto): OwnerReadResult['quality'] => {
  if (response.metadata.qualitySummary.invalid > 0) return 'BAD';
  if (response.metadata.partial || response.metadata.qualitySummary.suspect > 0) {
    return 'UNCERTAIN';
  }
  return 'GOOD';
};

export const createEnergyAnalyticsOwnerReader = (
  input: EnergyAnalyticsOwnerReaderConfig,
): EnergyAnalyticsReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  const reader: EnergyAnalyticsReader = {
    async read(
      readInput: OwnerReadInput<EnergyAnalyticsReadRequest>,
    ): Promise<OwnerReadResult> {
      assertEnergyRequest(readInput);
      const payload = await fetchOwnerJson(config, {
        path: '/internal/v1/analytics/energy-series',
        method: 'POST',
        headers: createOwnerHeaders(readInput.request.requestId, readInput.context, {
          logicalTool: readInput.request.tool,
          includePolicyRevision: false,
          hasBody: true,
        }),
        body: JSON.stringify(readInput.request.input),
      });
      const response = decodeEnergySeries(payload, readInput.request.input);
      return {
        requestId: readInput.request.requestId,
        owner: 'telemetry-query-service',
        scope: { ...readInput.context.scope },
        revision: response.metadata.datasetRevision,
        quality: qualityFor(response),
        provenance: 'telemetry-query-service:energy-series/v1',
        payload: response,
      };
    },
  };
  return Object.freeze(reader);
};

import { createHash } from 'node:crypto';

import {
  AgentToolError,
  type AgentRunContext,
  type AgentTool,
  type AgentToolErrorCode,
  type AgentToolExecutionRequest,
} from '../../agent/index.js';
import {
  OwnerReadError,
  type AuthorizationDecision,
  type DeviceTelemetryReadRequest,
  type DeviceTelemetryReader,
  type EnergyAnalyticsReadRequest,
  type EnergyAnalyticsReader,
  type EnergyCost,
  type OwnerReadContext,
  type OwnerReadErrorCode,
  type OwnerReadResult,
  type RegistryReadRequest,
  type TelemetryHistoryAggregate,
  type TelemetryHistoryReadRequest,
  type TelemetryHistoryReader,
  type ToolAuthorizableReadRequest,
  type RegistryReader,
  type ToolAuthorizationReader,
} from '../../application/index.js';
import type { DeviceObservationSnapshotDto } from './device-telemetry-owner-reader.js';
import type { EnergySeriesResponseDto } from './energy-analytics-owner-reader.js';
import type { RegistryOwnerPayload } from './registry-owner-reader.js';

export const HVAC_READ_TOOL_NAMES = Object.freeze([
  'site_get_context',
  'assets_list',
  'equipment_list',
  'telemetry_get_current',
  'telemetry_query_history',
  'energy_query_series',
  'energy_compare_periods',
  'energy_by_equipment',
] as const);

export type HvacReadToolName = typeof HVAC_READ_TOOL_NAMES[number];

export interface HvacReadToolLimits {
  readonly maxAssets: number;
  readonly maxEnergyPoints: number;
  readonly maxEnergyRangeMs: number;
  readonly maxResultBytes: number;
  readonly timeoutMs: number;
}

export interface CreateHvacReadToolsInput {
  readonly capabilities: readonly string[];
  readonly authorization: AuthorizationDecision;
  readonly toolAuthorizationReader: ToolAuthorizationReader;
  readonly registryReader: RegistryReader;
  readonly deviceTelemetryReader: DeviceTelemetryReader;
  readonly telemetryHistoryReader: TelemetryHistoryReader;
  readonly energyAnalyticsReader: EnergyAnalyticsReader;
  readonly limits?: Partial<HvacReadToolLimits>;
}

const DEFAULT_LIMITS: HvacReadToolLimits = Object.freeze({
  maxAssets: 200,
  maxEnergyPoints: 1_000,
  maxEnergyRangeMs: 31 * 24 * 60 * 60 * 1_000,
  maxResultBytes: 64 * 1_024,
  timeoutMs: 8_000,
});

const SITE_CAPABILITIES = Object.freeze(['site.read']);
const ASSET_CAPABILITIES = Object.freeze(['site.read', 'asset.list']);
const EQUIPMENT_CAPABILITIES = Object.freeze(['site.read', 'asset-model.read']);
const TELEMETRY_CAPABILITIES = Object.freeze(['site.read', 'telemetry.snapshot.read']);
const HISTORY_CAPABILITIES = Object.freeze(['site.read', 'telemetry.history.read']);
const ENERGY_CAPABILITIES = Object.freeze(['site.read', 'analytics.energy-series.read']);
const EQUIPMENT_ENERGY_CAPABILITIES = Object.freeze(['site.read', 'asset-model.read', 'analytics.energy-series.read']);

const emptyInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({}),
  additionalProperties: false,
});

const telemetryInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    deviceId: Object.freeze({
      type: 'string',
      pattern: '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
    }),
    pointKeys: Object.freeze({
      type: 'array',
      minItems: 1,
      maxItems: 16,
      uniqueItems: true,
      items: Object.freeze({ type: 'string', pattern: '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$' }),
    }),
  }),
  required: Object.freeze(['deviceId', 'pointKeys']),
  additionalProperties: false,
});

const historyInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    deviceId: Object.freeze({ type: 'string', minLength: 36, maxLength: 36 }),
    pointKeys: Object.freeze({ type: 'array', minItems: 1, maxItems: 8, uniqueItems: true, items: Object.freeze({ type: 'string', minLength: 1, maxLength: 128 }) }),
    from: Object.freeze({ type: 'string', format: 'date-time' }),
    to: Object.freeze({ type: 'string', format: 'date-time' }),
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['quarter_hour', 'hour', 'day']) }),
  }),
  required: Object.freeze(['deviceId', 'pointKeys', 'from', 'to']),
  additionalProperties: false,
});

const energyTypeSchema = Object.freeze({
  type: 'string',
  enum: Object.freeze(['electricity', 'cooling']),
  description: 'electricity (default) is power drawn; cooling is cooling delivered, also in kWh. Divide cooling by electricity for COP.',
});

const energyWindowInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    from: Object.freeze({ type: 'string', format: 'date-time' }),
    to: Object.freeze({ type: 'string', format: 'date-time' }),
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    qualityPolicy: Object.freeze({ type: 'string', enum: Object.freeze(['VALID_ONLY', 'VALID_AND_SUSPECT']) }),
  }),
  required: Object.freeze(['from', 'to']),
  additionalProperties: false,
});

const energyQueryInputSchema = Object.freeze({
  ...energyWindowInputSchema,
  properties: Object.freeze({ ...energyWindowInputSchema.properties, energyType: energyTypeSchema }),
});

const energyCompareInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    baselineFrom: Object.freeze({ type: 'string', format: 'date-time' }),
    baselineTo: Object.freeze({ type: 'string', format: 'date-time' }),
    currentFrom: Object.freeze({ type: 'string', format: 'date-time' }),
    currentTo: Object.freeze({ type: 'string', format: 'date-time' }),
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    qualityPolicy: Object.freeze({ type: 'string', enum: Object.freeze(['VALID_ONLY', 'VALID_AND_SUSPECT']) }),
    energyType: energyTypeSchema,
  }),
  required: Object.freeze(['baselineFrom', 'baselineTo', 'currentFrom', 'currentTo']),
  additionalProperties: false,
});

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const hasOnlyKeys = (value: Record<string, unknown>, allowed: readonly string[]): boolean => (
  Object.keys(value).every((key) => allowed.includes(key))
);

const fail = (code: AgentToolErrorCode, message: string = code): never => {
  throw new AgentToolError(code, message);
};

const requireRecord = (value: unknown, message: string): Record<string, unknown> => {
  if (!isRecord(value)) fail('TOOL_ARGUMENTS_INVALID', message);
  return value as Record<string, unknown>;
};

const requireString = (value: unknown, message: string): string => (
  typeof value === 'string' ? value : fail('TOOL_ARGUMENTS_INVALID', message)
);

const normalizeLimits = (input: Partial<HvacReadToolLimits> | undefined): HvacReadToolLimits => {
  const limits = { ...DEFAULT_LIMITS, ...input };
  for (const [name, value] of Object.entries(limits)) {
    if (!Number.isSafeInteger(value) || value <= 0) {
      fail('TOOL_ARGUMENTS_INVALID', `Invalid Tool runtime limit: ${name}.`);
    }
  }
  return Object.freeze(limits);
};

const hasCapabilities = (available: readonly string[], required: readonly string[]): boolean => (
  required.every((capability) => available.includes(capability))
);

const assertExecutionCapability = (
  context: AgentRunContext,
  required: readonly string[],
): void => {
  if (!hasCapabilities(context.capabilities, required)) {
    fail('TOOL_UNAUTHORIZED', 'The current principal cannot execute this Tool.');
  }
};

const exactEmptyArguments = (value: unknown): void => {
  if (!isRecord(value) || Object.keys(value).length !== 0) {
    fail('TOOL_ARGUMENTS_INVALID', 'This Tool does not accept model-selected scope arguments.');
  }
};

interface TelemetryArguments {
  readonly deviceId: string;
  readonly pointKeys: readonly string[];
}

const validUuidV7 = (value: string): boolean => (
  value.length === 36
  && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/u.test(value)
);

const validTelemetryKey = (value: string): boolean => (
  value.length >= 1
  && value.length <= 128
  && /^[A-Za-z][A-Za-z0-9_.:-]*/u.exec(value)?.[0] === value
);

const parseTelemetryArguments = (value: unknown): TelemetryArguments => {
  const record = requireRecord(value, 'Telemetry query arguments are invalid.');
  if (!hasOnlyKeys(record, ['deviceId', 'pointKeys'])) {
    fail('TOOL_ARGUMENTS_INVALID', 'Telemetry query arguments are invalid.');
  }
  const deviceId = requireString(record.deviceId, 'Telemetry query arguments are invalid.');
  if (!validUuidV7(deviceId)
    || !Array.isArray(record.pointKeys)
    || record.pointKeys.length < 1
    || record.pointKeys.length > 16
    || record.pointKeys.some((key) => typeof key !== 'string' || !validTelemetryKey(key))
    || new Set(record.pointKeys).size !== record.pointKeys.length) {
    fail('TOOL_ARGUMENTS_INVALID', 'Telemetry query arguments are invalid.');
  }
  return Object.freeze({ deviceId, pointKeys: Object.freeze((record.pointKeys as string[]).slice()) });
};

interface HistoryArguments {
  readonly deviceId: string;
  readonly pointKeys: readonly string[];
  readonly from: string;
  readonly to: string;
  readonly granularity: TelemetryHistoryReadRequest['input']['granularity'];
}

const HISTORY_GRANULARITIES = Object.freeze({ quarter_hour: 'QUARTER_HOUR', hour: 'HOUR', day: 'DAY' } as const);
const HISTORY_BUCKET_MS = Object.freeze({ QUARTER_HOUR: 15 * 60_000, HOUR: 3_600_000, DAY: 86_400_000 } as const);

const parseHistoryArguments = (value: unknown, limits: HvacReadToolLimits): HistoryArguments => {
  const record = requireRecord(value, 'History query arguments are invalid.');
  if (!hasOnlyKeys(record, ['deviceId', 'pointKeys', 'from', 'to', 'granularity'])
    || (record.granularity !== undefined && !Object.hasOwn(HISTORY_GRANULARITIES, String(record.granularity)))) {
    fail('TOOL_ARGUMENTS_INVALID', 'History query arguments are invalid.');
  }
  const { deviceId, pointKeys } = parseTelemetryArguments({ deviceId: record.deviceId, pointKeys: record.pointKeys });
  if (pointKeys.length > 8) fail('TOOL_ARGUMENTS_INVALID', 'History query reads at most 8 points.');
  const from = requireString(record.from, 'History query arguments are invalid.');
  const to = requireString(record.to, 'History query arguments are invalid.');
  const fromMs = Date.parse(from);
  const toMs = Date.parse(to);
  if (!Number.isFinite(fromMs) || !Number.isFinite(toMs) || toMs <= fromMs || toMs - fromMs > limits.maxEnergyRangeMs) {
    fail('TOOL_ARGUMENTS_INVALID', 'History query range is invalid or exceeds the Tool limit.');
  }
  const granularity = HISTORY_GRANULARITIES[(record.granularity ?? 'hour') as keyof typeof HISTORY_GRANULARITIES];
  // One bucket per point per period; past the result's bound the model should narrow the range or coarsen.
  if (Math.ceil((toMs - fromMs) / HISTORY_BUCKET_MS[granularity]) * pointKeys.length > limits.maxEnergyPoints) {
    fail('TOOL_ARGUMENTS_INVALID', 'Too many history buckets: shorten the range, read fewer points or use a coarser granularity.');
  }
  return Object.freeze({
    deviceId,
    pointKeys,
    from: new Date(fromMs).toISOString(),
    to: new Date(toMs).toISOString(),
    granularity,
  });
};

interface EnergyArguments {
  readonly energyType: 'electricity' | 'cooling';
  readonly from: string;
  readonly to: string;
  readonly granularity: 'hour' | 'day' | 'month';
  readonly qualityPolicy: 'VALID_ONLY' | 'VALID_AND_SUSPECT';
}

const ALL_ENERGY_TYPES: readonly EnergyArguments['energyType'][] = ['electricity', 'cooling'];

const parseEnergyWindow = (
  value: unknown,
  limits: HvacReadToolLimits,
  energyTypes: readonly EnergyArguments['energyType'][] = ['electricity'],
): EnergyArguments => {
  const record = requireRecord(value, 'Energy query arguments are invalid.');
  if (!hasOnlyKeys(record, energyTypes.length > 1
    ? ['from', 'to', 'granularity', 'qualityPolicy', 'energyType']
    : ['from', 'to', 'granularity', 'qualityPolicy'])
    || (record.energyType !== undefined && !energyTypes.includes(record.energyType as EnergyArguments['energyType']))
    || (record.granularity !== undefined && !['hour', 'day', 'month'].includes(String(record.granularity)))
    || (record.qualityPolicy !== undefined
      && !['VALID_ONLY', 'VALID_AND_SUSPECT'].includes(String(record.qualityPolicy)))) {
    fail('TOOL_ARGUMENTS_INVALID', 'Energy query arguments are invalid.');
  }
  const fromValue = requireString(record.from, 'Energy query arguments are invalid.');
  const toValue = requireString(record.to, 'Energy query arguments are invalid.');
  const from = Date.parse(fromValue);
  const to = Date.parse(toValue);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from || to - from > limits.maxEnergyRangeMs) {
    fail('TOOL_ARGUMENTS_INVALID', 'Energy query range is invalid or exceeds the Tool limit.');
  }
  return Object.freeze({
    energyType: (record.energyType ?? 'electricity') as EnergyArguments['energyType'],
    from: fromValue,
    to: toValue,
    granularity: (record.granularity ?? 'hour') as EnergyArguments['granularity'],
    qualityPolicy: (record.qualityPolicy ?? 'VALID_ONLY') as EnergyArguments['qualityPolicy'],
  });
};

interface EnergyComparisonArguments {
  readonly baseline: EnergyArguments;
  readonly current: EnergyArguments;
}

const parseEnergyComparison = (
  value: unknown,
  limits: HvacReadToolLimits,
): EnergyComparisonArguments => {
  const record = requireRecord(value, 'Energy comparison arguments are invalid.');
  if (!hasOnlyKeys(record, [
    'baselineFrom',
    'baselineTo',
    'currentFrom',
    'currentTo',
    'granularity',
    'qualityPolicy',
    'energyType',
  ])) {
    fail('TOOL_ARGUMENTS_INVALID', 'Energy comparison arguments are invalid.');
  }
  const baselineFrom = requireString(record.baselineFrom, 'Energy comparison arguments are invalid.');
  const baselineTo = requireString(record.baselineTo, 'Energy comparison arguments are invalid.');
  const currentFrom = requireString(record.currentFrom, 'Energy comparison arguments are invalid.');
  const currentTo = requireString(record.currentTo, 'Energy comparison arguments are invalid.');
  const shared = {
    ...(record.granularity === undefined ? {} : { granularity: record.granularity }),
    ...(record.qualityPolicy === undefined ? {} : { qualityPolicy: record.qualityPolicy }),
    ...(record.energyType === undefined ? {} : { energyType: record.energyType }),
  };
  return Object.freeze({
    baseline: parseEnergyWindow({ from: baselineFrom, to: baselineTo, ...shared }, limits, ALL_ENERGY_TYPES),
    current: parseEnergyWindow({ from: currentFrom, to: currentTo, ...shared }, limits, ALL_ENERGY_TYPES),
  });
};

const ownerErrorCode = (code: OwnerReadErrorCode): AgentToolErrorCode => {
  switch (code) {
    case 'OWNER_REQUEST_INVALID':
      return 'TOOL_OWNER_REQUEST_REJECTED';
    case 'OWNER_RESOURCE_NOT_FOUND':
      return 'TOOL_OWNER_RESOURCE_NOT_FOUND';
    case 'OWNER_READ_TIMEOUT':
      return 'TOOL_OWNER_TIMEOUT';
    case 'OWNER_READ_UNAVAILABLE':
      return 'TOOL_OWNER_UNAVAILABLE';
    case 'OWNER_RESPONSE_TOO_LARGE':
      return 'TOOL_RESULT_TOO_LARGE';
    case 'OWNER_RESPONSE_INVALID':
      return 'TOOL_OWNER_RESPONSE_INVALID';
  }
};

const normalizeToolFailure = (error: unknown): AgentToolError => {
  if (error instanceof AgentToolError) return error;
  if (error instanceof OwnerReadError) {
    const code = ownerErrorCode(error.code);
    return new AgentToolError(code, code);
  }
  return new AgentToolError('TOOL_EXECUTION_FAILED');
};

const runBounded = async <T>(
  signal: AbortSignal,
  timeoutMs: number,
  operation: () => Promise<T>,
): Promise<T> => {
  if (signal.aborted) fail('TOOL_CANCELLED');
  let timeoutHandle: ReturnType<typeof setTimeout> | undefined;
  let abortHandler: (() => void) | undefined;
  const timeout = new Promise<never>((_resolve, reject) => {
    timeoutHandle = setTimeout(() => reject(new AgentToolError('TOOL_TIMEOUT')), timeoutMs);
  });
  const aborted = new Promise<never>((_resolve, reject) => {
    abortHandler = () => reject(new AgentToolError('TOOL_CANCELLED'));
    signal.addEventListener('abort', abortHandler, { once: true });
  });
  try {
    return await Promise.race([operation(), timeout, aborted]);
  } catch (error) {
    throw normalizeToolFailure(error);
  } finally {
    if (timeoutHandle !== undefined) clearTimeout(timeoutHandle);
    if (abortHandler !== undefined) signal.removeEventListener('abort', abortHandler);
  }
};

const requestIdFor = (
  context: AgentRunContext,
  semanticTool: HvacReadToolName,
  ownerRequest: Omit<ToolAuthorizableReadRequest, 'requestId'>,
): string => {
  const digest = createHash('sha256')
    .update(JSON.stringify([context.runId, semanticTool, ownerRequest]))
    .digest('hex');
  return `agent-tool:${digest}`;
};

const baseOwnerContext = (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
): OwnerReadContext => {
  if (authorization.decision !== 'ALLOW'
    || authorization.delegationGrant === undefined
    || authorization.delegationGrant.trim().length === 0) {
    fail('TOOL_UNAUTHORIZED', 'The server authorization context is incomplete.');
  }
  return {
    sessionId: context.sessionId,
    runId: context.runId,
    scope: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      assetId: null,
      deviceId: null,
    },
    authorization,
    correlationId: context.correlationId,
  };
};

const authorizeOwnerRead = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  reader: ToolAuthorizationReader,
  request: ToolAuthorizableReadRequest,
): Promise<OwnerReadContext> => {
  const base = baseOwnerContext(context, authorization);
  const scoped = request.tool === 'telemetry.current.getDeviceObservationSnapshot' || request.tool === 'telemetry.history.aggregateDeviceHistory'
    ? {
      ...base,
      scope: { ...base.scope, deviceId: request.input.deviceId },
    }
    : base;
  const grant = await reader.authorize({ request, context: scoped });
  if (grant.delegationGrant.trim().length === 0) {
    fail('TOOL_UNAUTHORIZED', 'Owner authorization returned an empty grant.');
  }
  return {
    ...scoped,
    authorization: {
      ...authorization,
      delegationGrant: grant.delegationGrant,
      toolDelegationGrants: {
        ...authorization.toolDelegationGrants,
        [request.tool]: grant.delegationGrant,
      },
      ...(grant.policyRevision === undefined ? {} : { policyRevision: grant.policyRevision }),
    },
  };
};

const assertResultBytes = <T>(result: T, limits: HvacReadToolLimits): T => {
  if (Buffer.byteLength(JSON.stringify(result), 'utf8') > limits.maxResultBytes) {
    fail('TOOL_RESULT_TOO_LARGE', 'Tool result exceeds the model-context byte limit.');
  }
  return result;
};

const sitePayloadFrom = (result: OwnerReadResult): Extract<RegistryOwnerPayload, { kind: 'SITE' }> => {
  const payload = result.payload as RegistryOwnerPayload;
  if (payload.kind !== 'SITE') fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload as Extract<RegistryOwnerPayload, { kind: 'SITE' }>;
};

const assetsPayloadFrom = (
  result: OwnerReadResult,
): Extract<RegistryOwnerPayload, { kind: 'SITE_ASSETS' }> => {
  const payload = result.payload as RegistryOwnerPayload;
  if (payload.kind !== 'SITE_ASSETS') fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload as Extract<RegistryOwnerPayload, { kind: 'SITE_ASSETS' }>;
};

const assetModelPayloadFrom = (
  result: OwnerReadResult,
): Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }> => {
  const payload = result.payload as RegistryOwnerPayload;
  if (payload.kind !== 'SITE_ASSET_MODEL') fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload as Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }>;
};

const telemetryPayloadFrom = (result: OwnerReadResult): DeviceObservationSnapshotDto => {
  const payload = result.payload as DeviceObservationSnapshotDto;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.values)) {
    fail('TOOL_OWNER_RESPONSE_INVALID');
  }
  return payload;
};

const energyPayloadFrom = (result: OwnerReadResult): EnergySeriesResponseDto => {
  const payload = result.payload as EnergySeriesResponseDto;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.points) || !isRecord(payload.metadata)) {
    fail('TOOL_OWNER_RESPONSE_INVALID');
  }
  return payload;
};

/** An instant as the Site reads it, "2026-10-15 18:00", so the model never mistakes UTC hours for local ones. */
const siteTime = (instant: string, timezone: string): string => {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).formatToParts(new Date(instant)).map((part) => [part.type, part.value]));
  return `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}`;
};

type SitePointInput = { readonly periodStart: string; readonly energyKWh: number; readonly cost?: EnergyCost };

/** Each point at Site-local time; electricity also carries the owner's estimated cost and the energy it could not price. */
const sitePoints = (points: readonly SitePointInput[], timezone: string) => (
  points.map((point) => ({
    siteTime: siteTime(point.periodStart, timezone),
    energyKWh: point.energyKWh,
    ...(point.cost ? { estimatedCostCNY: point.cost.amountCNY, unpricedKWh: point.cost.unpricedKWh } : {}),
  }))
);

/** The series' estimated energy charge and unpriced energy; cooling has neither. */
const seriesCost = (points: readonly SitePointInput[]) => {
  const costs = points.flatMap((point) => (point.cost ? [point.cost] : []));
  if (costs.length === 0) return {};
  return {
    estimatedCostCNY: Number(costs.reduce((sum, cost) => sum + cost.amountCNY, 0).toFixed(2)),
    unpricedKWh: Number(costs.reduce((sum, cost) => sum + cost.unpricedKWh, 0).toFixed(6)),
  };
};

const historyPayloadFrom = (result: OwnerReadResult): TelemetryHistoryAggregate => {
  const payload = result.payload as TelemetryHistoryAggregate;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.buckets)) fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload;
};

/** One series per point, in the order asked: averages for readings, increases for counters. */
const readHistory = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  history: TelemetryHistoryReader,
  timezone: string,
  query: HistoryArguments,
) => {
  const draft = {
    tool: 'telemetry.history.aggregateDeviceHistory' as const,
    input: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      deviceId: query.deviceId,
      keys: query.pointKeys,
      from: query.from,
      to: query.to,
      granularity: query.granularity,
      timezone,
      qualityPolicy: 'VALID_ONLY' as const,
    },
  };
  const request: TelemetryHistoryReadRequest = { ...draft, requestId: requestIdFor(context, 'telemetry_query_history', draft) };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await history.read({ request, context: ownerContext });
  const payload = historyPayloadFrom(result);
  const series = query.pointKeys.flatMap((key) => {
    const buckets = payload.buckets.filter((bucket) => bucket.telemetryKey === key);
    const first = buckets[0];
    if (first === undefined) return [];
    const kind = first.pointType === 'COUNTER' ? 'counter' as const : first.pointType === 'STATE' ? 'state' as const : 'reading' as const;
    return [{
      key,
      unit: first.unit,
      kind,
      points: buckets.map((bucket) => ({
        siteTime: siteTime(bucket.periodStart, timezone),
        ...(bucket.gauge === undefined ? {} : { average: bucket.gauge.average, minimum: bucket.gauge.minimum, maximum: bucket.gauge.maximum }),
        ...(bucket.counter === undefined ? {} : { increase: bucket.counter.deltaSum }),
        ...(bucket.state === undefined ? {} : { lastValue: bucket.state.lastValue }),
        coverage: bucket.completeness,
      })),
    }];
  });
  // The history owner reader rates a result GOOD only when it is projected past the range and every bucket is whole.
  const completeness = result.quality === 'GOOD' ? 'COMPLETE' as const : 'PARTIAL' as const;
  return {
    deviceId: query.deviceId,
    query: { from: query.from, to: query.to, granularity: query.granularity, timezone },
    series,
    missingKeys: query.pointKeys.filter((key) => !series.some((entry) => entry.key === key)),
    completeness,
    quality: result.quality,
    source: {
      owner: result.owner,
      revision: result.revision,
      quality: result.quality,
      completeness,
      provenance: result.provenance,
      ...(payload.projectionWatermark === null ? {} : { projectionWatermark: payload.projectionWatermark }),
    },
  };
};

const readSite = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  registry: RegistryReader,
): Promise<{ readonly result: OwnerReadResult; readonly payload: Extract<RegistryOwnerPayload, { kind: 'SITE' }> }> => {
  const draft = {
    tool: 'registry.getSite' as const,
    input: { siteId: context.siteId },
  };
  const request: RegistryReadRequest = {
    ...draft,
    requestId: requestIdFor(context, 'site_get_context', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await registry.read({ request, context: ownerContext });
  return { result, payload: sitePayloadFrom(result) };
};

const readAssets = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  registry: RegistryReader,
): Promise<{ readonly result: OwnerReadResult; readonly payload: Extract<RegistryOwnerPayload, { kind: 'SITE_ASSETS' }> }> => {
  const draft = {
    tool: 'registry.listSiteAssets' as const,
    input: { siteId: context.siteId },
  };
  const request: RegistryReadRequest = {
    ...draft,
    requestId: requestIdFor(context, 'assets_list', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await registry.read({ request, context: ownerContext });
  return { result, payload: assetsPayloadFrom(result) };
};

const readAssetModel = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  registry: RegistryReader,
): Promise<{ readonly result: OwnerReadResult; readonly model: Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }>['model'] }> => {
  const draft = {
    tool: 'registry.getSiteAssetModel' as const,
    input: { siteId: context.siteId },
  };
  const request: RegistryReadRequest = {
    ...draft,
    requestId: requestIdFor(context, 'equipment_list', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await registry.read({ request, context: ownerContext });
  return { result, model: assetModelPayloadFrom(result).model };
};

/** Each device with the machine it belongs to and its active points, the keys other Tools read by. */
const describeEquipment = (model: Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }>['model']) => {
  const assetsById = new Map(model.assets.map((asset) => [asset.id, asset]));
  return model.devices.map((device) => {
    const link = model.relationships.find((relationship) => relationship.status === 'ACTIVE'
      && relationship.fromType === 'DEVICE' && relationship.fromId === device.id && relationship.toType === 'ASSET');
    const asset = link === undefined ? undefined : assetsById.get(link.toId);
    return {
      deviceId: device.id,
      name: device.displayName,
      type: device.deviceType,
      machine: asset === undefined ? null : { name: asset.displayName, type: asset.assetType },
      points: model.telemetryPoints
        .filter((point) => point.reportingDeviceId === device.id && point.status === 'ACTIVE')
        .map((point) => ({ key: point.pointCode, name: point.displayName, unit: point.unit, kind: point.pointType })),
    };
  });
};

const readTelemetry = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  telemetry: DeviceTelemetryReader,
  query: TelemetryArguments,
) => {
  const draft = {
    tool: 'telemetry.current.getDeviceObservationSnapshot' as const,
    input: {
      siteId: context.siteId,
      deviceId: query.deviceId,
      pointKeys: query.pointKeys,
    },
  };
  const request: DeviceTelemetryReadRequest = {
    ...draft,
    requestId: requestIdFor(context, 'telemetry_get_current', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await telemetry.read({ request, context: ownerContext });
  const payload = telemetryPayloadFrom(result);
  const completeness = payload.evaluationAvailability === 'AVAILABLE'
    && payload.telemetryReadiness === 'CURRENT'
    && payload.values.every((value) => value.state === 'PRESENT')
    ? 'COMPLETE' as const
    : 'PARTIAL' as const;
  return {
    deviceId: payload.deviceId,
    evaluatedAt: payload.evaluatedAt,
    evaluationAvailability: payload.evaluationAvailability,
    availabilityReasons: payload.availabilityReasons,
    presence: payload.presence,
    telemetryReadiness: payload.telemetryReadiness,
    displayState: payload.displayState,
    values: payload.values,
    completeness,
    quality: result.quality,
    source: {
      owner: result.owner,
      revision: result.revision,
      quality: result.quality,
      completeness,
      provenance: result.provenance,
      businessRevision: payload.businessRevision,
      evaluatedAt: payload.evaluatedAt,
    },
  };
};

const readEnergy = async (
  semanticTool: Extract<HvacReadToolName, 'energy_query_series' | 'energy_compare_periods'>,
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  energy: EnergyAnalyticsReader,
  timezone: string,
  query: EnergyArguments,
  limits: HvacReadToolLimits,
) => {
  const draft = {
    tool: 'analytics.getEnergySeries' as const,
    input: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      energyType: query.energyType,
      granularity: query.granularity,
      timezone,
      from: query.from,
      to: query.to,
      qualityPolicy: query.qualityPolicy,
    },
  };
  const request: EnergyAnalyticsReadRequest = {
    ...draft,
    requestId: requestIdFor(context, semanticTool, draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await energy.read({ request, context: ownerContext });
  const payload = energyPayloadFrom(result);
  if (payload.points.length > limits.maxEnergyPoints) {
    fail('TOOL_RESULT_TOO_LARGE', 'Energy series exceeds the Tool point limit.');
  }
  const totalKWh = payload.points.length === 0
    ? null
    : Number(payload.points.reduce((sum, point) => sum + point.energyKWh, 0).toFixed(6));
  const completeness = payload.metadata.partial ? 'PARTIAL' as const : 'COMPLETE' as const;
  return {
    siteId: context.siteId,
    query: {
      energyType: query.energyType,
      from: query.from,
      to: query.to,
      granularity: query.granularity,
      timezone,
      qualityPolicy: query.qualityPolicy,
    },
    points: sitePoints(payload.points, timezone),
    pointCount: payload.points.length,
    totalKWh,
    ...seriesCost(payload.points),
    completeness,
    quality: result.quality,
    qualitySummary: payload.metadata.qualitySummary,
    source: {
      owner: result.owner,
      revision: result.revision,
      quality: result.quality,
      completeness,
      provenance: result.provenance,
      ...(payload.metadata.dataWatermark === undefined ? {} : { dataWatermark: payload.metadata.dataWatermark }),
      ...(payload.metadata.aggregateWatermark === undefined ? {} : { aggregateWatermark: payload.metadata.aggregateWatermark }),
    },
  };
};

const sumKWh = (points: readonly { readonly energyKWh: number }[]): number => (
  Number(points.reduce((sum, point) => sum + point.energyKWh, 0).toFixed(6))
);

/** The Site total split by sub-metered device, largest first, each named from the asset model. */
const readEquipmentEnergy = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  energy: EnergyAnalyticsReader,
  registry: RegistryReader,
  timezone: string,
  query: EnergyArguments,
  limits: HvacReadToolLimits,
) => {
  const draft = {
    tool: 'analytics.getEnergySeries' as const,
    input: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      energyType: 'electricity' as const,
      granularity: query.granularity,
      timezone,
      from: query.from,
      to: query.to,
      qualityPolicy: query.qualityPolicy,
      breakdown: 'device' as const,
    },
  };
  const request: EnergyAnalyticsReadRequest = { ...draft, requestId: requestIdFor(context, 'energy_by_equipment', draft) };
  const [{ model }, result] = await Promise.all([
    readAssetModel(context, authorization, authorizer, registry),
    authorizeOwnerRead(context, authorization, authorizer, request).then((ownerContext) => energy.read({ request, context: ownerContext })),
  ]);
  const payload = energyPayloadFrom(result);
  const deviceSeries = payload.devices ?? [];
  if (deviceSeries.reduce((count, device) => count + device.points.length, payload.points.length) > limits.maxEnergyPoints) {
    fail('TOOL_RESULT_TOO_LARGE', 'Energy breakdown exceeds the Tool point limit.');
  }
  const equipment = new Map(describeEquipment(model).map((device) => [device.deviceId, device]));
  const siteTotalKWh = payload.points.length === 0 ? null : sumKWh(payload.points);
  const devices = deviceSeries
    .map((device) => {
      const totalKWh = sumKWh(device.points);
      const known = equipment.get(device.deviceId);
      return {
        deviceId: device.deviceId,
        name: known?.name ?? null,
        machine: known?.machine ?? null,
        totalKWh,
        shareOfSite: siteTotalKWh === null || siteTotalKWh === 0 ? null : Number((totalKWh / siteTotalKWh).toFixed(3)),
        points: sitePoints(device.points, timezone),
      };
    })
    .sort((left, right) => right.totalKWh - left.totalKWh);
  const completeness = payload.metadata.partial ? 'PARTIAL' as const : 'COMPLETE' as const;
  return {
    siteId: context.siteId,
    query: { from: query.from, to: query.to, granularity: query.granularity, timezone, qualityPolicy: query.qualityPolicy },
    siteTotalKWh,
    devices,
    completeness,
    quality: result.quality,
    qualitySummary: payload.metadata.qualitySummary,
    source: {
      owner: result.owner,
      revision: result.revision,
      quality: result.quality,
      completeness,
      provenance: result.provenance,
      ...(payload.metadata.dataWatermark === undefined ? {} : { dataWatermark: payload.metadata.dataWatermark }),
    },
  };
};

const summarizeEnergy = (value: Awaited<ReturnType<typeof readEnergy>>) => ({
  siteId: value.siteId,
  query: value.query,
  pointCount: value.pointCount,
  totalKWh: value.totalKWh,
  ...(value.estimatedCostCNY === undefined ? {} : { estimatedCostCNY: value.estimatedCostCNY, unpricedKWh: value.unpricedKWh }),
  completeness: value.completeness,
  quality: value.quality,
  qualitySummary: value.qualitySummary,
  source: value.source,
});

const compareEnergy = (
  baseline: ReturnType<typeof summarizeEnergy>,
  current: ReturnType<typeof summarizeEnergy>,
) => {
  if (baseline.totalKWh === null || current.totalKWh === null
    || baseline.completeness !== 'COMPLETE' || current.completeness !== 'COMPLETE') {
    return { status: 'INCOMPLETE' as const, absoluteChangeKWh: null, percentChange: null };
  }
  if (baseline.quality !== 'GOOD' || current.quality !== 'GOOD') {
    return { status: 'QUALITY_LIMITED' as const, absoluteChangeKWh: null, percentChange: null };
  }
  const absoluteChangeKWh = Number((current.totalKWh - baseline.totalKWh).toFixed(6));
  if (baseline.totalKWh === 0) {
    return { status: 'BASELINE_ZERO' as const, absoluteChangeKWh, percentChange: null };
  }
  return {
    status: 'COMPARABLE' as const,
    absoluteChangeKWh,
    percentChange: Number(((absoluteChangeKWh / baseline.totalKWh) * 100).toFixed(6)),
  };
};

const toolDefinition = (
  name: HvacReadToolName,
  description: string,
  inputSchema: Readonly<Record<string, unknown>>,
  requiredCapabilities: readonly string[],
) => Object.freeze({
  name,
  description,
  inputSchema,
  executionMode: 'parallel' as const,
  replayPolicy: 'safe' as const,
  requiredCapabilities,
});

export const createHvacReadTools = ({
  capabilities,
  authorization,
  toolAuthorizationReader,
  registryReader,
  deviceTelemetryReader,
  telemetryHistoryReader,
  energyAnalyticsReader,
  limits: inputLimits,
}: CreateHvacReadToolsInput): readonly AgentTool[] => {
  const limits = normalizeLimits(inputLimits);

  const siteTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'site_get_context',
      'Read authoritative context for the current Site. The Site scope is injected by the server.',
      emptyInputSchema,
      SITE_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      exactEmptyArguments(argumentsValue);
      assertExecutionCapability(context, SITE_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { result, payload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        return assertResultBytes({
          site: {
            id: payload.site.id,
            code: payload.site.code,
            displayName: payload.site.displayName,
            timezone: payload.site.timezone,
            status: payload.site.status,
          },
          source: {
            owner: result.owner,
            revision: result.revision,
            quality: result.quality,
            completeness: 'COMPLETE',
            provenance: result.provenance,
          },
        }, limits);
      });
    },
  });

  const assetsTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'assets_list',
      'List bounded authoritative Assets for the current Site. Tenant and Site are injected by the server.',
      emptyInputSchema,
      ASSET_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      exactEmptyArguments(argumentsValue);
      assertExecutionCapability(context, ASSET_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { result, payload } = await readAssets(context, authorization, toolAuthorizationReader, registryReader);
        const boundedAssets = payload.assets.slice(0, limits.maxAssets).map((asset) => ({
          id: asset.id,
          code: asset.code,
          displayName: asset.displayName,
          assetType: asset.assetType,
          status: asset.status,
          revision: asset.revision,
        }));
        const completeness = payload.assets.length > boundedAssets.length ? 'PARTIAL' as const : 'COMPLETE' as const;
        return assertResultBytes({
          siteId: context.siteId,
          assets: boundedAssets,
          totalCount: payload.assets.length,
          returnedCount: boundedAssets.length,
          completeness,
          source: {
            owner: result.owner,
            revision: result.revision,
            quality: result.quality,
            completeness,
            provenance: result.provenance,
          },
        }, limits);
      });
    },
  });

  const equipmentTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'equipment_list',
      'List the current Site\'s devices with their names, the machine each belongs to and their point keys. Use the device IDs and point keys with the other Tools.',
      emptyInputSchema,
      EQUIPMENT_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      exactEmptyArguments(argumentsValue);
      assertExecutionCapability(context, EQUIPMENT_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { result, model } = await readAssetModel(context, authorization, toolAuthorizationReader, registryReader);
        const devices = describeEquipment(model);
        const bounded = devices.slice(0, limits.maxAssets);
        const completeness = devices.length > bounded.length ? 'PARTIAL' as const : 'COMPLETE' as const;
        return assertResultBytes({
          siteId: context.siteId,
          devices: bounded,
          totalCount: devices.length,
          completeness,
          source: {
            owner: result.owner,
            revision: result.revision,
            quality: result.quality,
            completeness,
            provenance: result.provenance,
          },
        }, limits);
      });
    },
  });

  const telemetryTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'telemetry_get_current',
      'Read bounded current telemetry for one Device in the current Site. Tenant and Site are injected by the server.',
      telemetryInputSchema,
      TELEMETRY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseTelemetryArguments(argumentsValue);
      assertExecutionCapability(context, TELEMETRY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => (
        assertResultBytes(
          await readTelemetry(context, authorization, toolAuthorizationReader, deviceTelemetryReader, query),
          limits,
        )
      ));
    },
  });

  const historyTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'telemetry_query_history',
      'Read how up to 8 points of one Device in the current Site changed over a period, bucketed by quarter hour, hour (default) or day in the Site calendar. Readings give average, minimum and maximum per bucket; counters give the increase. Use equipment_list for device ids and point keys.',
      historyInputSchema,
      HISTORY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseHistoryArguments(argumentsValue, limits);
      assertExecutionCapability(context, HISTORY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        return assertResultBytes(await readHistory(
          context,
          authorization,
          toolAuthorizationReader,
          telemetryHistoryReader,
          sitePayload.site.timezone,
          query,
        ), limits);
      });
    },
  });

  const energyQueryTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy_query_series',
      'Query a bounded electricity or cooling energy series for the current Site using its authoritative timezone.',
      energyQueryInputSchema,
      ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseEnergyWindow(argumentsValue, limits, ALL_ENERGY_TYPES);
      assertExecutionCapability(context, ENERGY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        const result = await readEnergy(
          'energy_query_series',
          context,
          authorization,
          toolAuthorizationReader,
          energyAnalyticsReader,
          sitePayload.site.timezone,
          query,
          limits,
        );
        return assertResultBytes(result, limits);
      });
    },
  });

  const energyCompareTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy_compare_periods',
      'Compare two bounded electricity or cooling periods for the current Site without exposing raw infrastructure.',
      energyCompareInputSchema,
      ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseEnergyComparison(argumentsValue, limits);
      assertExecutionCapability(context, ENERGY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        const [baselineSeries, currentSeries] = await Promise.all([
          readEnergy(
            'energy_compare_periods',
            context,
            authorization,
            toolAuthorizationReader,
            energyAnalyticsReader,
            sitePayload.site.timezone,
            query.baseline,
            limits,
          ),
          readEnergy(
            'energy_compare_periods',
            context,
            authorization,
            toolAuthorizationReader,
            energyAnalyticsReader,
            sitePayload.site.timezone,
            query.current,
            limits,
          ),
        ]);
        const baseline = summarizeEnergy(baselineSeries);
        const current = summarizeEnergy(currentSeries);
        return assertResultBytes({
          baseline,
          current,
          comparison: compareEnergy(baseline, current),
        }, limits);
      });
    },
  });

  const equipmentEnergyTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy_by_equipment',
      'Split the current Site\'s electricity in a period by sub-metered device (machines with their own meter), largest first, with each device\'s series at the chosen granularity.',
      energyWindowInputSchema,
      EQUIPMENT_ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseEnergyWindow(argumentsValue, limits);
      assertExecutionCapability(context, EQUIPMENT_ENERGY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        return assertResultBytes(await readEquipmentEnergy(
          context,
          authorization,
          toolAuthorizationReader,
          energyAnalyticsReader,
          registryReader,
          sitePayload.site.timezone,
          query,
          limits,
        ), limits);
      });
    },
  });

  return Object.freeze([
    siteTool,
    assetsTool,
    equipmentTool,
    telemetryTool,
    historyTool,
    energyQueryTool,
    energyCompareTool,
    equipmentEnergyTool,
  ].filter((tool) => hasCapabilities(capabilities, tool.definition.requiredCapabilities)));
};

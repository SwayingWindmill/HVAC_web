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
  type SemanticQueryReadRequest,
  type SemanticQueryReader,
  type SemanticQueryResult,
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
import { semanticCatalog } from './semantic-catalog.gen.js';

export const HVAC_READ_TOOL_NAMES = Object.freeze([
  'site_get_context',
  'assets_list',
  'equipment_list',
  'telemetry_get_current',
  'telemetry_query_history',
  'energy_query_series',
  'energy_compare_periods',
  'energy_by_equipment',
  'energy_semantic_query',
] as const);

export type HvacReadToolName = typeof HVAC_READ_TOOL_NAMES[number];

export interface HvacReadToolLimits {
  readonly maxAssets: number;
  readonly maxEnergyPoints: number;
  readonly maxEnergyRangeMs: number;
  /** Rows one semantic query may return (the Run's maxToolResultRecords). */
  readonly maxResultRecords: number;
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
  readonly semanticQueryReader: SemanticQueryReader;
  readonly limits?: Partial<HvacReadToolLimits>;
  /** The clock site_get_context reads the Site's local date and time from. */
  readonly now?: () => number;
}

const DEFAULT_LIMITS: HvacReadToolLimits = Object.freeze({
  maxAssets: 200,
  maxEnergyPoints: 1_000,
  maxEnergyRangeMs: 31 * 24 * 60 * 60 * 1_000,
  maxResultRecords: 2_000,
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

// Site-local calendar dates, both ends included: 「10月13日到16日」 is 2026-10-13 to 2026-10-16.
// The Tool turns them into the Site's whole days, so the model never converts to UTC.
const siteDateSchema = (description: string) => Object.freeze({ type: 'string', pattern: '^\\d{4}-\\d{2}-\\d{2}$', description });
const START_DATE = siteDateSchema('First day, Site-local, YYYY-MM-DD.');
const END_DATE = siteDateSchema('Last day, Site-local, YYYY-MM-DD, included.');

const historyInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    deviceId: Object.freeze({ type: 'string', minLength: 36, maxLength: 36 }),
    pointKeys: Object.freeze({ type: 'array', minItems: 1, maxItems: 8, uniqueItems: true, items: Object.freeze({ type: 'string', minLength: 1, maxLength: 128 }) }),
    startDate: START_DATE,
    endDate: END_DATE,
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['quarter_hour', 'hour', 'day']) }),
  }),
  required: Object.freeze(['deviceId', 'pointKeys', 'startDate', 'endDate']),
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
    startDate: START_DATE,
    endDate: END_DATE,
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    qualityPolicy: Object.freeze({ type: 'string', enum: Object.freeze(['VALID_ONLY', 'VALID_AND_SUSPECT']) }),
  }),
  required: Object.freeze(['startDate', 'endDate']),
  additionalProperties: false,
});

const energyQueryInputSchema = Object.freeze({
  ...energyWindowInputSchema,
  properties: Object.freeze({ ...energyWindowInputSchema.properties, energyType: energyTypeSchema }),
});

const energyCompareInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    baselineStartDate: START_DATE,
    baselineEndDate: END_DATE,
    currentStartDate: START_DATE,
    currentEndDate: END_DATE,
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    qualityPolicy: Object.freeze({ type: 'string', enum: Object.freeze(['VALID_ONLY', 'VALID_AND_SUSPECT']) }),
    energyType: energyTypeSchema,
  }),
  required: Object.freeze(['baselineStartDate', 'baselineEndDate', 'currentStartDate', 'currentEndDate']),
  additionalProperties: false,
});

// The query service's own ceilings (analyticsmodel), which it checks again: members per query and
// time buckets; the Run's budget may be lower.
const SEMANTIC_MEASURE_LIMIT = 4;
const SEMANTIC_DIMENSION_LIMIT = 2;
const SEMANTIC_BUCKET_LIMIT = 744;

// Every catalog member as `view.member`, with the view's and the member's Chinese meaning, so the
// model can only name what the semantic views publish (#573).
const semanticMemberSchema = (kind: 'measures' | 'dimensions') => Object.freeze({
  anyOf: Object.freeze(semanticCatalog.views.flatMap((view) => view[kind].map((member) => Object.freeze({
    const: `${view.name}.${member.name}`,
    description: `${view.title} · ${member.title}：${member.description}`,
  })))),
});

const semanticQueryInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    measures: Object.freeze({
      type: 'array', items: semanticMemberSchema('measures'), minItems: 1, maxItems: SEMANTIC_MEASURE_LIMIT,
      description: 'One to four figures, all from the same view.',
    }),
    dimensions: Object.freeze({
      type: 'array', items: semanticMemberSchema('dimensions'), maxItems: SEMANTIC_DIMENSION_LIMIT,
      description: 'Up to two groupings from the same view as the measures; each adds a row per value.',
    }),
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    startDate: START_DATE,
    endDate: END_DATE,
  }),
  required: Object.freeze(['measures', 'granularity', 'startDate', 'endDate']),
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

/** Site-local days, both ends included, already checked against the Run's range budget. */
interface SiteDays {
  readonly startDate: string;
  readonly endDate: string;
  readonly days: number;
}

const DAY_MS = 86_400_000;

const calendarDay = (value: unknown, message: string): number => {
  const text = requireString(value, message);
  const match = /^(\d{4})-(\d{2})-(\d{2})$/u.exec(text);
  const day = match === null ? Number.NaN : Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  if (!Number.isFinite(day) || new Date(day).toISOString().slice(0, 10) !== text) fail('TOOL_ARGUMENTS_INVALID', message);
  return day;
};

const parseSiteDays = (start: unknown, end: unknown, limits: HvacReadToolLimits, message: string): SiteDays => {
  const first = calendarDay(start, message);
  const last = calendarDay(end, message);
  const days = (last - first) / DAY_MS + 1;
  if (days < 1) fail('TOOL_ARGUMENTS_INVALID', `${message} The last day is before the first.`);
  if (days * DAY_MS > limits.maxEnergyRangeMs) {
    fail('TOOL_ARGUMENTS_INVALID', `${message} At most ${limits.maxEnergyRangeMs / DAY_MS} days per read.`);
  }
  return Object.freeze({ startDate: String(start), endDate: String(end), days });
};

// How far a zone is ahead of UTC at an instant.
const zoneOffsetMs = (instant: number, timezone: string): number => {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(new Date(instant)).map((part) => [part.type, part.value]));
  return Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day), Number(parts.hour), Number(parts.minute), Number(parts.second)) - instant;
};

const localMidnight = (day: number, timezone: string): number => {
  const guess = day - zoneOffsetMs(day, timezone);
  return day - zoneOffsetMs(guess, timezone);
};

/** The Site's whole days as the owner's UTC range: from the first midnight to the one after the last day. */
const siteRange = (days: SiteDays, timezone: string): { readonly from: string; readonly to: string } => {
  const first = Date.parse(`${days.startDate}T00:00:00.000Z`);
  return {
    from: new Date(localMidnight(first, timezone)).toISOString(),
    to: new Date(localMidnight(first + days.days * DAY_MS, timezone)).toISOString(),
  };
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

interface HistoryArguments extends SiteDays {
  readonly deviceId: string;
  readonly pointKeys: readonly string[];
  readonly granularity: TelemetryHistoryReadRequest['input']['granularity'];
}

const HISTORY_GRANULARITIES = Object.freeze({ quarter_hour: 'QUARTER_HOUR', hour: 'HOUR', day: 'DAY' } as const);
const HISTORY_BUCKETS_PER_DAY = Object.freeze({ QUARTER_HOUR: 96, HOUR: 24, DAY: 1 } as const);

const parseHistoryArguments = (value: unknown, limits: HvacReadToolLimits): HistoryArguments => {
  const record = requireRecord(value, 'History query arguments are invalid.');
  if (!hasOnlyKeys(record, ['deviceId', 'pointKeys', 'startDate', 'endDate', 'granularity'])
    || (record.granularity !== undefined && !Object.hasOwn(HISTORY_GRANULARITIES, String(record.granularity)))) {
    fail('TOOL_ARGUMENTS_INVALID', 'History query arguments are invalid.');
  }
  const { deviceId, pointKeys } = parseTelemetryArguments({ deviceId: record.deviceId, pointKeys: record.pointKeys });
  if (pointKeys.length > 8) fail('TOOL_ARGUMENTS_INVALID', 'History query reads at most 8 points.');
  const days = parseSiteDays(record.startDate, record.endDate, limits, 'History query dates are invalid.');
  const granularity = HISTORY_GRANULARITIES[(record.granularity ?? 'hour') as keyof typeof HISTORY_GRANULARITIES];
  // One bucket per point per period; past the result's bound the model should narrow the range or coarsen.
  if (days.days * HISTORY_BUCKETS_PER_DAY[granularity] * pointKeys.length > limits.maxEnergyPoints) {
    fail('TOOL_ARGUMENTS_INVALID', 'Too many history buckets: shorten the range, read fewer points or use a coarser granularity.');
  }
  return Object.freeze({ deviceId, pointKeys, ...days, granularity });
};

interface EnergyArguments extends SiteDays {
  readonly energyType: 'electricity' | 'cooling';
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
    ? ['startDate', 'endDate', 'granularity', 'qualityPolicy', 'energyType']
    : ['startDate', 'endDate', 'granularity', 'qualityPolicy'])
    || (record.energyType !== undefined && !energyTypes.includes(record.energyType as EnergyArguments['energyType']))
    || (record.granularity !== undefined && !['hour', 'day', 'month'].includes(String(record.granularity)))
    || (record.qualityPolicy !== undefined
      && !['VALID_ONLY', 'VALID_AND_SUSPECT'].includes(String(record.qualityPolicy)))) {
    fail('TOOL_ARGUMENTS_INVALID', 'Energy query arguments are invalid.');
  }
  const days = parseSiteDays(record.startDate, record.endDate, limits, 'Energy query dates are invalid.');
  return Object.freeze({
    energyType: (record.energyType ?? 'electricity') as EnergyArguments['energyType'],
    ...days,
    granularity: (record.granularity ?? 'hour') as EnergyArguments['granularity'],
    qualityPolicy: (record.qualityPolicy ?? 'VALID_ONLY') as EnergyArguments['qualityPolicy'],
  });
};

interface SemanticQueryArguments extends SiteDays {
  readonly view: typeof semanticCatalog.views[number];
  readonly measures: readonly string[];
  readonly dimensions: readonly string[];
  readonly granularity: 'hour' | 'day' | 'month';
}

const semanticBuckets = (days: number, granularity: SemanticQueryArguments['granularity']): number => (
  granularity === 'hour' ? days * 24 : granularity === 'day' ? days : Math.ceil(days / 28) + 1
);

// Refuses, before any read, members outside one catalog view and ranges beyond the Run's budget.
// The owner checks the catalog again and forces the Site.
const parseSemanticQuery = (value: unknown, limits: HvacReadToolLimits): SemanticQueryArguments => {
  const invalid = 'Semantic query arguments are invalid.';
  const record = requireRecord(value, invalid);
  const named = (list: unknown, max: number): string[][] => {
    if (!Array.isArray(list) || list.length > max) fail('TOOL_ARGUMENTS_INVALID', invalid);
    return (list as unknown[]).map((member) => requireString(member, invalid).split('.'));
  };
  if (!hasOnlyKeys(record, ['measures', 'dimensions', 'granularity', 'startDate', 'endDate'])
    || !['hour', 'day', 'month'].includes(String(record.granularity))) {
    fail('TOOL_ARGUMENTS_INVALID', invalid);
  }
  const measureParts = named(record.measures, SEMANTIC_MEASURE_LIMIT);
  const dimensionParts = record.dimensions === undefined ? [] : named(record.dimensions, SEMANTIC_DIMENSION_LIMIT);
  const all = [...measureParts, ...dimensionParts];
  const view = semanticCatalog.views.find(({ name }) => name === measureParts[0]?.[0]);
  if (view === undefined || all.some((parts) => parts.length !== 2 || parts[0] !== view.name)
    || new Set(all.map((parts) => parts[1])).size !== all.length) {
    return fail('TOOL_ARGUMENTS_INVALID', 'Semantic query members must come from one listed view, each once.');
  }
  const measures = measureParts.map((parts) => parts[1] ?? '');
  const dimensions = dimensionParts.map((parts) => parts[1] ?? '');
  if (!measures.every((name) => view.measures.some((member) => member.name === name))
    || !dimensions.every((name) => view.dimensions.some((member) => member.name === name))) {
    fail('TOOL_ARGUMENTS_INVALID', "Semantic query members are not in that view's catalog.");
  }
  const granularity = record.granularity as SemanticQueryArguments['granularity'];
  const days = parseSiteDays(record.startDate, record.endDate, limits, 'Semantic query dates are invalid.');
  if (semanticBuckets(days.days, granularity) > Math.min(SEMANTIC_BUCKET_LIMIT, limits.maxEnergyPoints)) {
    fail('TOOL_ARGUMENTS_INVALID', 'Semantic query has too many time buckets: shorten the range or use a coarser granularity.');
  }
  return Object.freeze({ view, measures, dimensions, granularity, ...days });
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
    'baselineStartDate',
    'baselineEndDate',
    'currentStartDate',
    'currentEndDate',
    'granularity',
    'qualityPolicy',
    'energyType',
  ])) {
    fail('TOOL_ARGUMENTS_INVALID', 'Energy comparison arguments are invalid.');
  }
  const shared = {
    ...(record.granularity === undefined ? {} : { granularity: record.granularity }),
    ...(record.qualityPolicy === undefined ? {} : { qualityPolicy: record.qualityPolicy }),
    ...(record.energyType === undefined ? {} : { energyType: record.energyType }),
  };
  return Object.freeze({
    baseline: parseEnergyWindow({ startDate: record.baselineStartDate, endDate: record.baselineEndDate, ...shared }, limits, ALL_ENERGY_TYPES),
    current: parseEnergyWindow({ startDate: record.currentStartDate, endDate: record.currentEndDate, ...shared }, limits, ALL_ENERGY_TYPES),
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

// What the model reads is what an operator would read: Chinese values, Site-local times, no owner
// names, revisions, quality counts or internal enums (#590).
const ENERGY_WORDS = Object.freeze({ electricity: '用电', cooling: '供冷' } as const);
const GRANULARITY_WORDS = Object.freeze({ hour: '按小时', day: '按日', month: '按月', QUARTER_HOUR: '按15分钟', HOUR: '按小时', DAY: '按日' } as const);
const QUALITY_POLICY_WORDS = Object.freeze({ VALID_ONLY: '只算有效读数', VALID_AND_SUSPECT: '含可疑读数' } as const);
const POINT_KIND_WORDS: Readonly<Record<string, string>> = Object.freeze({ TELEMETRY: '读数', COUNTER: '累计量', STATE: '状态' });

/** 「完整」, or what is missing and how far the data reaches. */
const dataStatus = (partial: boolean, watermark: string | null | undefined, timezone: string): string => {
  if (!partial) return '完整';
  return watermark == null ? '不完整' : `不完整：数据只到 ${siteTime(watermark, timezone)}`;
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
      ...siteRange(query, timezone),
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
    return [{
      key,
      unit: first.unit,
      kind: POINT_KIND_WORDS[first.pointType] ?? '读数',
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
  return {
    deviceId: query.deviceId,
    query: { startDate: query.startDate, endDate: query.endDate, granularity: GRANULARITY_WORDS[query.granularity] },
    series,
    missingKeys: query.pointKeys.filter((key) => !series.some((entry) => entry.key === key)),
    dataStatus: dataStatus(result.quality !== 'GOOD', payload.projectionWatermark, timezone),
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
      machine: asset === undefined ? null : asset.displayName,
      points: model.telemetryPoints
        .filter((point) => point.reportingDeviceId === device.id && point.status === 'ACTIVE')
        .map((point) => ({ key: point.pointCode, name: point.displayName, unit: point.unit, kind: POINT_KIND_WORDS[point.pointType] ?? '读数' })),
    };
  });
};

const DEVICE_STATE_WORDS: Readonly<Record<string, string>> = Object.freeze({
  ONLINE: '在线', OFFLINE: '离线', STALE: '读数过期', UNKNOWN: '状态不明', UNAVAILABLE: '暂时读不到',
});

const valueState = (value: DeviceObservationSnapshotDto['values'][number]): string => {
  if (value.state === 'MISSING') return '没有读数';
  if (value.freshness === 'STALE' || value.quality === 'STALE') return '读数过期';
  if (value.quality === 'INVALID') return '读数无效';
  return value.quality === 'GOOD' ? '正常' : '读数可疑';
};

const readTelemetry = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  telemetry: DeviceTelemetryReader,
  timezone: string,
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
  const complete = payload.evaluationAvailability === 'AVAILABLE'
    && payload.telemetryReadiness === 'CURRENT'
    && payload.values.every((value) => value.state === 'PRESENT');
  return {
    deviceId: payload.deviceId,
    readAt: siteTime(payload.evaluatedAt, timezone),
    deviceState: payload.displayState === null ? '状态不明' : DEVICE_STATE_WORDS[payload.displayState] ?? '状态不明',
    values: payload.values.map((value) => (value.state === 'MISSING'
      ? { key: value.key, state: valueState(value) }
      : { key: value.key, value: value.value, unit: value.unit, siteTime: siteTime(value.sampledAt, timezone), state: valueState(value) })),
    dataStatus: complete ? '完整' : '不完整：有读数过期或缺失',
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
      ...siteRange(query, timezone),
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
  return {
    query: {
      energy: ENERGY_WORDS[query.energyType],
      startDate: query.startDate,
      endDate: query.endDate,
      granularity: GRANULARITY_WORDS[query.granularity],
      readings: QUALITY_POLICY_WORDS[query.qualityPolicy],
    },
    points: sitePoints(payload.points, timezone),
    pointCount: payload.points.length,
    totalKWh,
    ...seriesCost(payload.points),
    dataStatus: dataStatus(payload.metadata.partial, payload.metadata.dataWatermark, timezone),
    ...readingNote(payload.metadata.qualitySummary, query.qualityPolicy),
    // For the comparison only; summarizeEnergy leaves it out of what the model reads.
    comparable: !payload.metadata.partial && result.quality === 'GOOD',
  };
};

/** Says so when readings were left out for quality; counts of measuring intervals mean nothing to an operator. */
const readingNote = (summary: EnergySeriesResponseDto['metadata']['qualitySummary'], policy: EnergyArguments['qualityPolicy']) => (
  summary.suspect + summary.invalid === 0 ? {}
    : { readingNote: policy === 'VALID_ONLY' ? '部分读数可疑或无效，没有计入' : '包含部分可疑读数，无效读数没有计入' }
);

const semanticPayloadFrom = (result: OwnerReadResult): SemanticQueryResult => {
  const payload = result.payload as SemanticQueryResult;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.columns) || !Array.isArray(payload.rows) || !isRecord(payload.metadata)) {
    fail('TOOL_OWNER_RESPONSE_INVALID');
  }
  return payload;
};

// One semantic query; each row comes back keyed by the column's Chinese title, its period at Site time.
const readSemantic = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  reader: SemanticQueryReader,
  timezone: string,
  query: SemanticQueryArguments,
  limits: HvacReadToolLimits,
  machines: ReadonlyMap<string, string>,
) => {
  const draft = {
    tool: 'analytics.semanticQuery' as const,
    input: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      view: query.view.name,
      measures: query.measures,
      ...(query.dimensions.length === 0 ? {} : { dimensions: query.dimensions }),
      granularity: query.granularity,
      timezone,
      ...siteRange(query, timezone),
    },
  };
  const request: SemanticQueryReadRequest = { ...draft, requestId: requestIdFor(context, 'energy_semantic_query', draft) };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await reader.read({ request, context: ownerContext });
  const payload = semanticPayloadFrom(result);
  if (payload.rows.length > limits.maxResultRecords) {
    fail('TOOL_RESULT_TOO_LARGE', "Semantic query rows exceed the Run's record limit.");
  }
  return {
    view: query.view.title,
    query: { granularity: GRANULARITY_WORDS[query.granularity], startDate: query.startDate, endDate: query.endDate },
    columns: payload.columns.map(({ title }) => title),
    rows: payload.rows.map((row) => Object.fromEntries(payload.columns.map((column, index) => (
      column.kind === 'time' ? ['siteTime', siteTime(String(row[index]), timezone)]
        : column.kind === 'dimension' ? [column.title, dimensionWords(column.name, row[index] ?? null, machines)]
          : [column.title, row[index]]
    )))),
    rowCount: payload.rows.length,
    dataStatus: dataStatus(payload.metadata.partial, payload.metadata.dataWatermark, timezone),
  };
};

// A grouping value as the pages show it: energy type, data quality and Tariff period in words,
// equipment by its machine's name.
const DIMENSION_WORDS: Readonly<Record<string, Readonly<Record<string, string>>>> = Object.freeze({
  energy_type: { electricity: '用电', cooling: '供冷' },
  quality: { VALID: '有效', SUSPECT: '可疑', INVALID: '无效' },
  tariff_period: { SHARP: '尖', PEAK: '峰', FLAT: '平', VALLEY: '谷', DEEP_VALLEY: '深谷' },
});

const dimensionWords = (name: string, value: string | number | null, machines: ReadonlyMap<string, string>): string | null => {
  if (value === null) return null;
  const text = String(value);
  if (name === 'equipment_id') return machines.get(text) ?? '未登记的设备';
  return DIMENSION_WORDS[name]?.[text] ?? text;
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
      ...siteRange(query, timezone),
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
        name: known?.name ?? '未登记的设备',
        machine: known?.machine ?? null,
        totalKWh,
        shareOfSite: siteTotalKWh === null || siteTotalKWh === 0 ? null : Number((totalKWh / siteTotalKWh).toFixed(3)),
        points: sitePoints(device.points, timezone),
      };
    })
    .sort((left, right) => right.totalKWh - left.totalKWh);
  return {
    query: { startDate: query.startDate, endDate: query.endDate, granularity: GRANULARITY_WORDS[query.granularity], readings: QUALITY_POLICY_WORDS[query.qualityPolicy] },
    siteTotalKWh,
    devices,
    dataStatus: dataStatus(payload.metadata.partial, payload.metadata.dataWatermark, timezone),
    ...readingNote(payload.metadata.qualitySummary, query.qualityPolicy),
  };
};

const summarizeEnergy = ({ points: _points, comparable: _comparable, ...visible }: Awaited<ReturnType<typeof readEnergy>>) => visible;

const compareEnergy = (
  baseline: Awaited<ReturnType<typeof readEnergy>>,
  current: Awaited<ReturnType<typeof readEnergy>>,
) => {
  if (baseline.totalKWh === null || current.totalKWh === null || !baseline.comparable || !current.comparable) {
    return { status: '数据不完整或读数有问题，不能比较', absoluteChangeKWh: null, percentChange: null };
  }
  const absoluteChangeKWh = Number((current.totalKWh - baseline.totalKWh).toFixed(6));
  if (baseline.totalKWh === 0) {
    return { status: '对比期为 0，算不出百分比', absoluteChangeKWh, percentChange: null };
  }
  return {
    status: '可以比较',
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
  semanticQueryReader,
  limits: inputLimits,
  now = Date.now,
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
        const { payload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        return assertResultBytes({
          site: {
            name: payload.site.displayName,
            // The Site's own date and time, to resolve 「昨天」「上周同日」 into dates.
            localNow: siteTime(new Date(now()).toISOString(), payload.site.timezone),
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
        const { payload } = await readAssets(context, authorization, toolAuthorizationReader, registryReader);
        const boundedAssets = payload.assets.slice(0, limits.maxAssets).map((asset) => ({ name: asset.displayName, code: asset.code }));
        return assertResultBytes({
          assets: boundedAssets,
          ...(payload.assets.length > boundedAssets.length ? { note: `只列出前 ${boundedAssets.length} 个，共 ${payload.assets.length} 个` } : {}),
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
        const { model } = await readAssetModel(context, authorization, toolAuthorizationReader, registryReader);
        const devices = describeEquipment(model);
        const bounded = devices.slice(0, limits.maxAssets);
        return assertResultBytes({
          devices: bounded,
          ...(devices.length > bounded.length ? { note: `只列出前 ${bounded.length} 个，共 ${devices.length} 个` } : {}),
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
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        return assertResultBytes(
          await readTelemetry(context, authorization, toolAuthorizationReader, deviceTelemetryReader, sitePayload.site.timezone, query),
          limits,
        );
      });
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
        return assertResultBytes({
          baseline: summarizeEnergy(baselineSeries),
          current: summarizeEnergy(currentSeries),
          comparison: compareEnergy(baselineSeries, currentSeries),
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

  const semanticQueryTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy_semantic_query',
      'Ask several figures of the current Site at once from one semantic view (Site energy, equipment energy or the electricity cost estimate), '
        + 'per hour, day or month, optionally grouped by up to two dimensions. Use it when a question needs more than one figure side by side. '
        + 'Energy totals mix electricity and cooling unless grouped by energy type; the Site is set by the server.',
      semanticQueryInputSchema,
      ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseSemanticQuery(argumentsValue, limits);
      assertExecutionCapability(context, ENERGY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        // Equipment is grouped by device id; the model reads each machine's name instead.
        const machines = new Map<string, string>(query.dimensions.includes('equipment_id')
          ? describeEquipment((await readAssetModel(context, authorization, toolAuthorizationReader, registryReader)).model)
            .map((device) => [device.deviceId, device.machine ?? device.name])
          : []);
        return assertResultBytes(await readSemantic(
          context,
          authorization,
          toolAuthorizationReader,
          semanticQueryReader,
          sitePayload.site.timezone,
          query,
          limits,
          machines,
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
    semanticQueryTool,
  ].filter((tool) => hasCapabilities(capabilities, tool.definition.requiredCapabilities)));
};

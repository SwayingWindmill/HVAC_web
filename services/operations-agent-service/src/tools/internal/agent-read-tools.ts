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
  type EnergyAnalyticsReadRequest,
  type EnergyAnalyticsReader,
  type OwnerReadContext,
  type OwnerReadErrorCode,
  type OwnerReadResult,
  type ParallelReadRequest,
  type RegistryReadRequest,
  type RegistryReader,
  type ToolAuthorizationReader,
} from '../../application/index.js';
import type { EnergySeriesResponseDto } from './energy-analytics-owner-reader.js';
import type { RegistryOwnerPayload } from './registry-owner-reader.js';

export const HVAC_READ_TOOL_NAMES = Object.freeze([
  'site.get_context',
  'assets.list',
  'energy.query_series',
  'energy.compare_periods',
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
const ENERGY_CAPABILITIES = Object.freeze(['site.read', 'analytics.energy-series.read']);

const emptyInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({}),
  additionalProperties: false,
});

const energyQueryInputSchema = Object.freeze({
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

const energyCompareInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    baselineFrom: Object.freeze({ type: 'string', format: 'date-time' }),
    baselineTo: Object.freeze({ type: 'string', format: 'date-time' }),
    currentFrom: Object.freeze({ type: 'string', format: 'date-time' }),
    currentTo: Object.freeze({ type: 'string', format: 'date-time' }),
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    qualityPolicy: Object.freeze({ type: 'string', enum: Object.freeze(['VALID_ONLY', 'VALID_AND_SUSPECT']) }),
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

interface EnergyArguments {
  readonly from: string;
  readonly to: string;
  readonly granularity: 'hour' | 'day' | 'month';
  readonly qualityPolicy: 'VALID_ONLY' | 'VALID_AND_SUSPECT';
}

const parseEnergyWindow = (
  value: unknown,
  limits: HvacReadToolLimits,
): EnergyArguments => {
  const record = requireRecord(value, 'Energy query arguments are invalid.');
  if (!hasOnlyKeys(record, ['from', 'to', 'granularity', 'qualityPolicy'])
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
  };
  return Object.freeze({
    baseline: parseEnergyWindow({ from: baselineFrom, to: baselineTo, ...shared }, limits),
    current: parseEnergyWindow({ from: currentFrom, to: currentTo, ...shared }, limits),
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
  ownerRequest: Omit<ParallelReadRequest, 'requestId'>,
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
    investigationId: context.sessionId,
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
  request: ParallelReadRequest,
): Promise<OwnerReadContext> => {
  const base = baseOwnerContext(context, authorization);
  const grant = await reader.authorize({ request, context: base });
  if (grant.delegationGrant.trim().length === 0) {
    fail('TOOL_UNAUTHORIZED', 'Owner authorization returned an empty grant.');
  }
  return {
    ...base,
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

const energyPayloadFrom = (result: OwnerReadResult): EnergySeriesResponseDto => {
  const payload = result.payload as EnergySeriesResponseDto;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.points) || !isRecord(payload.metadata)) {
    fail('TOOL_OWNER_RESPONSE_INVALID');
  }
  return payload;
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
    requestId: requestIdFor(context, 'site.get_context', draft),
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
    requestId: requestIdFor(context, 'assets.list', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await registry.read({ request, context: ownerContext });
  return { result, payload: assetsPayloadFrom(result) };
};

const readEnergy = async (
  semanticTool: Extract<HvacReadToolName, 'energy.query_series' | 'energy.compare_periods'>,
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
      energyType: 'electricity' as const,
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
      from: query.from,
      to: query.to,
      granularity: query.granularity,
      timezone,
      qualityPolicy: query.qualityPolicy,
    },
    points: payload.points,
    pointCount: payload.points.length,
    totalKWh,
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

const summarizeEnergy = (value: Awaited<ReturnType<typeof readEnergy>>) => ({
  siteId: value.siteId,
  query: value.query,
  pointCount: value.pointCount,
  totalKWh: value.totalKWh,
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
  energyAnalyticsReader,
  limits: inputLimits,
}: CreateHvacReadToolsInput): readonly AgentTool[] => {
  const limits = normalizeLimits(inputLimits);

  const siteTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'site.get_context',
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
      'assets.list',
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

  const energyQueryTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy.query_series',
      'Query a bounded electricity energy series for the current Site using its authoritative timezone.',
      energyQueryInputSchema,
      ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseEnergyWindow(argumentsValue, limits);
      assertExecutionCapability(context, ENERGY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        const result = await readEnergy(
          'energy.query_series',
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
      'energy.compare_periods',
      'Compare two bounded electricity periods for the current Site without exposing raw infrastructure.',
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
            'energy.compare_periods',
            context,
            authorization,
            toolAuthorizationReader,
            energyAnalyticsReader,
            sitePayload.site.timezone,
            query.baseline,
            limits,
          ),
          readEnergy(
            'energy.compare_periods',
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

  return Object.freeze([
    siteTool,
    assetsTool,
    energyQueryTool,
    energyCompareTool,
  ].filter((tool) => hasCapabilities(capabilities, tool.definition.requiredCapabilities)));
};

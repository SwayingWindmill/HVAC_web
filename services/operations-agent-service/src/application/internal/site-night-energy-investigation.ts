import {
  businessRecordsEqual,
  createInvestigationBusinessRecord,
  createInvestigationRevision,
  type AnalysisReferenceRecord,
  type EvidenceQualityClassification,
  type EvidenceRecord,
  type EvidenceSourceReference,
  type FindingRecord,
  type FindingSynthesisProvenance,
  type InvestigationBusinessRecord,
  type InvestigationScope,
  type InvestigationStatus,
  type OperatorInputAcceptedRecord,
  type OperatorInputAcceptedValues,
  type OperatorInputRequestView,
  type OperationsInvestigationView,
  type ToolExecutionReceiptRecord,
} from '../../domain/index.js';
import {
  InvestigationCoordinatorError,
  createInvestigationCoordinator,
  type InvestigationCoordinator,
} from './investigation-coordinator.js';
import {
  analyzeSiteNightEnergy,
  planSiteNightEnergyPeriods,
  type NightEnergyQualityPolicy,
  type NightEnergySeries,
  type SiteNightEnergyWindow,
} from './night-energy-analysis.js';
import {
  FINDING_SYNTHESIS_OUTPUT_SCHEMA_VERSION,
  FINDING_SYNTHESIS_PROMPT_POLICY_VERSION,
  synthesizeFinding,
  type FindingSynthesisDecision,
  type FindingSynthesizer,
} from './finding-synthesis.js';
import {
  createOperationsAuditEvent,
  operationsAuditEventId,
} from './operations-audit.js';
import { OwnerReadError } from './ports.js';
import type {
  AgentExecutionRuntime,
  AuthorizationDecision,
  InvestigationCoordinatorPorts,
  OwnerReadContext,
  OwnerReadResult,
  ParallelReadRequest,
  RunResourceBudgetCost,
  RunResourceBudgetDimension,
  RunResourceBudgetOutcome,
} from './ports.js';
import {
  DEFAULT_RUN_RESOURCE_BUDGET_POLICY,
  ZERO_RUN_RESOURCE_BUDGET_COST,
  normalizeRunResourceBudgetPolicy,
  runResourceOwnerResultBatchCost,
  runResourceOwnerResultBatchOperationId,
  runResourceOwnerResultCost,
  runResourceReadBatchCost,
  runResourceReadBatchOperationId,
  toRunResourceBudgetOutcome,
} from './run-resource-budget.js';
import {
  safeAddOperationsTelemetryCounter,
  safeObserveOperationsTelemetryHistogram,
  safeStartOperationsTelemetrySpan,
  type OperationsTelemetryCorrelation,
  type OperationsTelemetryOutcome,
  type OperationsTelemetryOwner,
} from './operations-telemetry.js';
import { sha256Hex } from './sha256.js';

export interface SiteNightEnergyInvestigationPolicy {
  readonly runtimeRevision: string;
  readonly window: SiteNightEnergyWindow;
  readonly baselineOffsetDays: number;
  readonly increaseThresholdPercent: number;
  readonly qualityPolicy: NightEnergyQualityPolicy;
  readonly findingSynthesisTimeoutMs: number;
}

export type SiteNightEnergyInvestigationCoordinatorPorts = Omit<
  InvestigationCoordinatorPorts,
  'agentExecutionRuntime'
> & {
  readonly createAgentExecutionRuntime: (scope: InvestigationScope) => AgentExecutionRuntime;
  readonly findingSynthesizer?: FindingSynthesizer;
};

export interface StartSiteNightEnergyInvestigationCommand {
  readonly tenantId: string;
  readonly siteId: string;
}

export interface SiteNightEnergyInvestigationQuery {
  readonly investigationId: string;
}

export interface ListSiteNightEnergyInvestigationsQuery {
  readonly tenantId: string;
  readonly siteId: string;
  readonly limit?: number;
}

export interface SiteNightEnergyActiveRunView {
  readonly id: string;
  readonly status: 'ACTIVE' | 'PAUSED' | 'WAITING_FOR_OPERATOR_INPUT';
  readonly startedAt: number;
}

export type SiteNightEnergyFindingView = Omit<FindingRecord, 'synthesis'>;

export interface SiteNightEnergyInvestigationView {
  readonly schemaVersion: 1;
  readonly id: string;
  readonly scope: InvestigationScope;
  readonly status: InvestigationStatus;
  readonly revision: number;
  readonly createdAt: number;
  readonly activeRun: SiteNightEnergyActiveRunView | null;
  readonly outcome: 'SUPPORTED_SITE_FINDING' | 'UNABLE_TO_CONCLUDE' | null;
  readonly resourceBudget: RunResourceBudgetOutcome | null;
  readonly evidence: readonly EvidenceRecord[];
  readonly analysisReferences: readonly AnalysisReferenceRecord[];
  readonly findings: readonly SiteNightEnergyFindingView[];
  readonly toolReceipts: readonly ToolExecutionReceiptRecord[];
  readonly operatorInputRequest: OperatorInputRequestView | null;
  readonly acceptedOperatorInputs: readonly OperatorInputAcceptedRecord[];
}

export interface SiteNightEnergyInvestigationSummary {
  readonly schemaVersion: 1;
  readonly id: string;
  readonly scope: InvestigationScope;
  readonly status: InvestigationStatus;
  readonly revision: number;
  readonly createdAt: number;
  readonly outcome: 'SUPPORTED_SITE_FINDING' | 'UNABLE_TO_CONCLUDE' | null;
  readonly resourceBudget: RunResourceBudgetOutcome | null;
  readonly evidenceCount: number;
  readonly analysisReferenceCount: number;
  readonly findingCount: number;
  readonly toolReceiptCount: number;
  readonly acceptedOperatorInputCount: number;
}

export interface SiteNightEnergyInvestigationList {
  readonly schemaVersion: 1;
  readonly investigations: readonly SiteNightEnergyInvestigationSummary[];
}

export interface AcceptSiteNightEnergyOperatorInputCommand {
  readonly investigationId: string;
  readonly requestId: string;
  readonly expectedRevision: number;
  readonly idempotencyKey: string;
  readonly values: OperatorInputAcceptedValues;
}

export interface AcceptSiteNightEnergyOperatorInputResult {
  readonly outcome: 'COMMITTED' | 'DUPLICATE';
  readonly investigation: SiteNightEnergyInvestigationView;
}

export interface SiteNightEnergyInvestigationCoordinator {
  start(command: StartSiteNightEnergyInvestigationCommand): Promise<SiteNightEnergyInvestigationView>;
  list(query: ListSiteNightEnergyInvestigationsQuery): Promise<SiteNightEnergyInvestigationList>;
  get(query: SiteNightEnergyInvestigationQuery): Promise<SiteNightEnergyInvestigationView>;
  advance(query: SiteNightEnergyInvestigationQuery): Promise<SiteNightEnergyInvestigationView>;
  requestOperatorInput(query: SiteNightEnergyInvestigationQuery): Promise<SiteNightEnergyInvestigationView>;
  acceptOperatorInput(
    command: AcceptSiteNightEnergyOperatorInputCommand,
  ): Promise<AcceptSiteNightEnergyOperatorInputResult>;
  cancel(query: SiteNightEnergyInvestigationQuery): Promise<SiteNightEnergyInvestigationView>;
}

interface RegistrySitePayload {
  readonly kind: 'SITE';
  readonly site: {
    readonly id: string;
    readonly tenantId: string;
    readonly timezone: string;
  };
}

interface RegistryAssetPayload {
  readonly kind: 'SITE_ASSETS';
  readonly siteId: string;
  readonly assets: readonly { readonly id: string }[];
}

const defaultPolicy: SiteNightEnergyInvestigationPolicy = Object.freeze({
  runtimeRevision: 'site-night-energy-investigation/v1',
  window: Object.freeze({ startLocalTime: '22:00', endLocalTime: '06:00' }),
  baselineOffsetDays: 7,
  increaseThresholdPercent: 10,
  qualityPolicy: 'VALID_AND_SUSPECT',
  findingSynthesisTimeoutMs: 2_000,
});

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const canonicalize = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (!isRecord(value)) return value;
  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => [key, canonicalize(item)]),
  );
};

const digest = (value: unknown): string => `sha256:${sha256Hex(JSON.stringify(canonicalize(value)))}`;

const synthesisProvenanceFor = (
  decision: FindingSynthesisDecision,
): FindingSynthesisProvenance => {
  const invocation = decision.invocation;
  const usage = invocation?.tokenUsage ?? null;
  return Object.freeze({
    source: decision.source,
    provider: invocation?.provider ?? null,
    model: invocation?.model ?? null,
    configurationDigest: invocation?.configurationDigest ?? null,
    promptPolicyVersion: FINDING_SYNTHESIS_PROMPT_POLICY_VERSION,
    outputSchemaVersion: FINDING_SYNTHESIS_OUTPUT_SCHEMA_VERSION,
    inputDigest: decision.inputDigest,
    outputDigest: decision.outputDigest,
    latencyMs: invocation?.latencyMs ?? null,
    metering: usage === null
      ? null
      : Object.freeze({ inputUnits: usage.inputTokens, outputUnits: usage.outputTokens }),
    traceId: invocation?.traceId ?? null,
    fallbackReason: decision.fallbackReason,
  });
};

const requireIdentity = (value: string, label: string): string => {
  if (value.trim().length === 0 || value.length > 256) {
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      `${label} must contain 1 to 256 characters.`,
    );
  }
  return value;
};

const requireSiteScope = (scope: InvestigationScope): InvestigationScope => {
  if (scope.tenantId.trim().length === 0
    || scope.siteId === null
    || scope.siteId.trim().length === 0
    || scope.assetId !== null
    || scope.deviceId !== null) {
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      'Night-energy Investigations require an exact Site Scope.',
    );
  }
  return scope;
};

const localDateAt = (epochMs: number, timezone: string): string => {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat('en-US-u-ca-iso8601', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).formatToParts(epochMs).map(({ type, value }) => [type, value]),
  );
  return `${parts.year}-${parts.month}-${parts.day}`;
};

const shiftLocalDate = (value: string, days: number): string => {
  const [year, month, day] = value.split('-').map(Number);
  return new Date(Date.UTC(year ?? 0, (month ?? 1) - 1, (day ?? 1) + days))
    .toISOString()
    .slice(0, 10);
};

const ownerForTool = (tool: ParallelReadRequest['tool']): ToolExecutionReceiptRecord['owner'] => {
  if (tool === 'registry.getSite' || tool === 'registry.listSiteAssets') return 'registry';
  if (tool === 'commands.getCapabilities') return 'command-service';
  return 'telemetry-query-service';
};

const qualityClassification = (quality: OwnerReadResult['quality']): EvidenceQualityClassification => quality;

const requireAllowed = (decision: AuthorizationDecision): AuthorizationDecision => {
  if (decision.decision !== 'ALLOW' || decision.decisionId.trim().length === 0) {
    throw new InvestigationCoordinatorError(
      'AUTHORIZATION_DENIED',
      'The requested Site Investigation is not authorized.',
    );
  }
  return decision;
};

const decodeSite = (result: OwnerReadResult, scope: InvestigationScope): RegistrySitePayload => {
  const payload = result.payload;
  if (!isRecord(payload)
    || payload.kind !== 'SITE'
    || !isRecord(payload.site)
    || payload.site.id !== scope.siteId
    || payload.site.tenantId !== scope.tenantId
    || typeof payload.site.timezone !== 'string'
    || payload.site.timezone.trim().length === 0) {
    throw new InvestigationCoordinatorError(
      'OWNER_RESPONSE_INVALID',
      'Registry returned Site data outside the Investigation Scope.',
    );
  }
  return payload as unknown as RegistrySitePayload;
};

const decodeAssets = (
  result: OwnerReadResult,
  scope: InvestigationScope,
): RegistryAssetPayload => {
  const payload = result.payload;
  if (!isRecord(payload)
    || payload.kind !== 'SITE_ASSETS'
    || payload.siteId !== scope.siteId
    || !Array.isArray(payload.assets)
    || payload.assets.length > 2_000
    || payload.assets.some((item) => !isRecord(item) || typeof item.id !== 'string')) {
    throw new InvestigationCoordinatorError(
      'OWNER_RESPONSE_INVALID',
      'Registry returned Asset data outside the Investigation Scope.',
    );
  }
  return payload as unknown as RegistryAssetPayload;
};

const decodeEnergy = (result: OwnerReadResult): NightEnergySeries => {
  if (!isRecord(result.payload)) {
    throw new InvestigationCoordinatorError(
      'OWNER_RESPONSE_INVALID',
      'Telemetry Query Service returned an invalid Energy Series.',
    );
  }
  return result.payload as unknown as NightEnergySeries;
};

const sourceFor = (
  result: OwnerReadResult,
  recordedAt: number,
): EvidenceSourceReference => {
  if (result.owner === 'registry') {
    return {
      owner: 'registry',
      scope: { ...result.scope },
      requestId: result.requestId,
      registryRevision: result.revision,
      datasetRevision: null,
      watermark: { data: null, aggregate: null },
      partial: false,
      quality: {
        classification: qualityClassification(result.quality),
        valid: 1,
        suspect: result.quality === 'UNCERTAIN' ? 1 : 0,
        invalid: result.quality === 'BAD' ? 1 : 0,
      },
      capturedAt: recordedAt,
      evaluatedAt: recordedAt,
      provenanceDigest: digest({
        requestId: result.requestId,
        revision: result.revision,
        provenance: result.provenance,
      }),
    };
  }
  const series = decodeEnergy(result);
  return {
    owner: 'telemetry-query-service',
    scope: { ...result.scope },
    requestId: result.requestId,
    registryRevision: null,
    datasetRevision: series.metadata.datasetRevision,
    watermark: {
      data: series.metadata.dataWatermark ?? null,
      aggregate: series.metadata.aggregateWatermark ?? null,
    },
    partial: series.metadata.partial,
    quality: {
      classification: qualityClassification(result.quality),
      valid: series.metadata.qualitySummary.valid,
      suspect: series.metadata.qualitySummary.suspect,
      invalid: series.metadata.qualitySummary.invalid,
    },
    capturedAt: recordedAt,
    evaluatedAt: recordedAt,
    provenanceDigest: digest({
      requestId: result.requestId,
      revision: result.revision,
      provenance: result.provenance,
      metadata: series.metadata,
    }),
  };
};

const recordId = (investigationId: string, suffix: string): string => (
  `${investigationId}:${suffix}`
);

const requestId = (investigationId: string, suffix: string): string => (
  `${investigationId}:${suffix}`
);

const activeAuthority = (view: OperationsInvestigationView): {
  readonly runId: string;
  readonly leaseId: string;
} => {
  const run = view.runs.find(({ id }) => id === view.activeRunId);
  if (run === undefined || run.status !== 'ACTIVE' || run.lease === null) {
    throw new InvestigationCoordinatorError(
      'LEASE_CONFLICT',
      'The Night-energy Investigation has no active writable Agent Run.',
    );
  }
  return { runId: run.id, leaseId: run.lease.id };
};

export const createSiteNightEnergyInvestigationCoordinator = (
  ports: SiteNightEnergyInvestigationCoordinatorPorts,
  configuredPolicy: Partial<SiteNightEnergyInvestigationPolicy> = {},
): SiteNightEnergyInvestigationCoordinator => {
  const policy: SiteNightEnergyInvestigationPolicy = Object.freeze({
    ...defaultPolicy,
    ...configuredPolicy,
    window: Object.freeze({
      ...defaultPolicy.window,
      ...configuredPolicy.window,
    }),
  });
  if (!Number.isSafeInteger(policy.baselineOffsetDays) || policy.baselineOffsetDays <= 0) {
    throw new Error('baselineOffsetDays must be a positive safe integer.');
  }
  if (!Number.isSafeInteger(policy.findingSynthesisTimeoutMs)
    || policy.findingSynthesisTimeoutMs <= 0
    || policy.findingSynthesisTimeoutMs > 600_000) {
    throw new Error('findingSynthesisTimeoutMs must be between 1 and 600000.');
  }
  const runResourcePolicy = normalizeRunResourceBudgetPolicy(
    ports.resourceBudgetPolicy ?? DEFAULT_RUN_RESOURCE_BUDGET_POLICY,
  );

  const telemetryCorrelationFor = (input: {
    readonly investigationId?: string;
    readonly runId?: string;
    readonly stepId?: string;
    readonly traceparent?: string;
    readonly tracestate?: string;
  } = {}): OperationsTelemetryCorrelation => ({
    ...(ports.telemetryContext ?? {}),
    ...(input.investigationId === undefined ? {} : { investigationId: input.investigationId }),
    ...(input.runId === undefined ? {} : { runId: input.runId }),
    ...(input.stepId === undefined ? {} : { stepId: input.stepId }),
    ...(input.traceparent === undefined ? {} : { traceparent: input.traceparent }),
    ...(input.tracestate === undefined ? {} : { tracestate: input.tracestate }),
  });

  const telemetryOwnerFor = (request: ParallelReadRequest): OperationsTelemetryOwner => (
    request.tool === 'analytics.getEnergySeries' ? 'telemetry-query-service' : 'registry'
  );

  const telemetryOutcomeForOwnerError = (error: unknown): OperationsTelemetryOutcome => {
    if (!(error instanceof OwnerReadError)) return 'ERROR';
    if (error.code === 'OWNER_RESOURCE_NOT_FOUND') return 'NOT_FOUND';
    if (error.code === 'OWNER_READ_TIMEOUT') return 'TIMEOUT';
    if (error.code === 'OWNER_READ_UNAVAILABLE') return 'UNAVAILABLE';
    return 'INVALID';
  };

  const genericFor = (
    scope: InvestigationScope,
    fixedNow?: number,
  ): InvestigationCoordinator => (
    createInvestigationCoordinator({
      ...ports,
      ...(fixedNow === undefined ? {} : { clock: { now: () => fixedNow } }),
      agentExecutionRuntime: ports.createAgentExecutionRuntime(scope),
    })
  );

  const checkResourceBudget = async (input: {
    readonly view: OperationsInvestigationView;
    readonly authorization: AuthorizationDecision;
    readonly at: number;
    readonly operationId: string;
    readonly cost: RunResourceBudgetCost;
  }): Promise<RunResourceBudgetOutcome | null> => {
    const run = input.view.runs.find(({ id }) => id === input.view.activeRunId);
    if (run === undefined) {
      throw new InvestigationCoordinatorError(
        'INVALID_INVESTIGATION_STATE',
        'The Night-energy Investigation has no active Agent Run for resource accounting.',
      );
    }
    const startedAt = ports.clock.now();
    const span = safeStartOperationsTelemetrySpan(ports.telemetry, {
      name: 'operations.budget.check',
      kind: 'INTERNAL',
      correlation: telemetryCorrelationFor({
        investigationId: input.view.id,
        runId: run.id,
      }),
      attributes: { operation: 'CHECK_BUDGET' },
    });
    try {
      const decision = await ports.budgetGuard.check({
        investigationId: input.view.id,
        runId: run.id,
        startedAt: run.startedAt,
        at: input.at,
        operationId: input.operationId,
        policy: runResourcePolicy,
        cost: input.cost,
      });
      span.setAttributes({ duplicate: decision.duplicate });
      if (decision.duplicate) {
        safeAddOperationsTelemetryCounter(ports.telemetry, {
          name: 'operations_agent_retries_total',
          labels: { operation: 'CHECK_BUDGET', outcome: 'DUPLICATE' },
        });
      }
      const costs: readonly [RunResourceBudgetDimension, number][] = [
        ['MODEL_INVOCATIONS', input.cost.modelInvocations],
        ['TOOL_REQUESTS', input.cost.toolRequests],
        ['QUERY_RANGE_MS', input.cost.queryRangeMs],
        ['QUERY_BUCKETS', input.cost.queryBuckets],
        ['OWNER_RECORDS', input.cost.ownerRecords],
        ['PAYLOAD_BYTES', input.cost.payloadBytes],
      ];
      if (!decision.duplicate) {
        for (const [budgetDimension, value] of costs) {
          if (value <= 0) continue;
          safeAddOperationsTelemetryCounter(ports.telemetry, {
            name: 'operations_agent_budget_consumed',
            value,
            labels: { budgetDimension },
          });
        }
      }
      if (decision.decision === 'ALLOW') {
        span.setStatus('SUCCESS');
        return null;
      }
      const outcome = toRunResourceBudgetOutcome(decision.snapshot);
      if (outcome === null) {
        span.setStatus('ERROR');
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          'Budget Guard denied Night-energy work without a typed exhaustion outcome.',
        );
      }
      const publicOutcome = outcome.outcome === 'PARTIAL' ? 'PARTIAL' : 'UNABLE_TO_CONCLUDE';
      span.setAttributes({
        outcome: publicOutcome,
        budgetDimension: outcome.exhaustedDimension,
        budgetConsumed: outcome.consumed,
        budgetLimit: outcome.limit,
        partial: outcome.outcome === 'PARTIAL',
      });
      span.setStatus('EXHAUSTED');
      safeAddOperationsTelemetryCounter(ports.telemetry, {
        name: 'operations_agent_budget_exhaustions_total',
        labels: { budgetDimension: outcome.exhaustedDimension, outcome: publicOutcome },
      });
      const siteId = input.view.scope.siteId;
      if (siteId !== null) {
        try {
          await ports.auditRecorder.record(createOperationsAuditEvent({
            eventId: operationsAuditEventId({
              tenantId: input.view.scope.tenantId,
              siteId,
              investigationId: input.view.id,
              runId: run.id,
              revision: input.view.revision,
              operation: 'ADVANCE_AGENT_RUN',
              outcome: outcome.outcome,
              discriminator: input.operationId,
            }),
            scope: input.view.scope,
            investigationId: input.view.id,
            runId: run.id,
            investigationRevision: input.view.revision,
            actor: input.authorization.auditActor ?? {
              actorType: 'SERVICE',
              actorId: 'operations-agent-service',
              actorIssuer: 'spiffe://hvac.local',
              executingService: 'operations-agent-service',
              executingSpiffeId: 'spiffe://hvac.local/operations-agent-service',
            },
            authorizationDecisionId: input.authorization.decisionId,
            policyRevision: input.authorization.policyRevision?.trim() || 'unversioned',
            operation: 'ADVANCE_AGENT_RUN',
            outcome: outcome.outcome,
            occurredAt: input.at,
          }));
        } catch {
          // Budget denial remains authoritative when Audit intent storage is unavailable.
        }
      }
      return outcome;
    } catch (error) {
      span.setStatus('ERROR');
      throw error;
    } finally {
      const durationMs = Math.max(0, ports.clock.now() - startedAt);
      span.setAttributes({ durationMs });
      span.end();
    }
  };


  const loadAuthorizedView = async (
    investigationId: string,
  ): Promise<OperationsInvestigationView> => {
    const persisted = await ports.investigationRepository.get(investigationId);
    if (persisted === null) {
      throw new InvestigationCoordinatorError(
        'INVESTIGATION_NOT_FOUND',
        'The requested Investigation was not found.',
      );
    }
    return genericFor(requireSiteScope(persisted.view().scope)).get({ investigationId });
  };

  const ensureRecord = async <TRecord extends InvestigationBusinessRecord>(
    investigationId: string,
    identity: string,
    create: (recordedAt: number) => TRecord,
    newRecordedAt = ports.clock.now(),
  ): Promise<TRecord> => {
    const existing = await ports.businessRecordRepository.get(investigationId, identity);
    const recordedAt = existing?.recordedAt ?? newRecordedAt;
    const candidate = createInvestigationBusinessRecord(create(recordedAt)) as TRecord;
    if (existing !== null && !businessRecordsEqual(existing, candidate)) {
      throw new InvestigationCoordinatorError(
        'DUPLICATE_RECORD',
        `Business record ${identity} already exists with different authoritative content.`,
      );
    }
    return (existing ?? candidate) as TRecord;
  };

  const commitRecord = async (
    coordinator: InvestigationCoordinator,
    view: OperationsInvestigationView,
    stepId: string,
    record: InvestigationBusinessRecord,
  ): Promise<OperationsInvestigationView> => {
    if (record.recordType === 'OPERATOR_INPUT_ACCEPTED') {
      throw new InvestigationCoordinatorError(
        'INVALID_INVESTIGATION_STATE',
        'Accepted Operator Input cannot be committed as an Agent effect.',
      );
    }
    const authority = activeAuthority(view);
    const result = await coordinator.commitEffect({
      investigationId: view.id,
      runId: authority.runId,
      leaseId: authority.leaseId,
      expectedRevision: view.revision,
      stepId,
      idempotencyKey: `${record.id}:commit`,
      kind: record.recordType,
      recordId: record.id,
      record,
    });
    return result.investigation;
  };

  const receiptFor = async (
    investigationId: string,
    runId: string,
    stepId: string,
    request: ParallelReadRequest,
    result: OwnerReadResult,
    operationTime: number,
  ): Promise<ToolExecutionReceiptRecord> => {
    const identity = recordId(investigationId, `receipt:${request.requestId}`);
    return ensureRecord(investigationId, identity, (recordedAt) => ({
      schemaVersion: 1,
      recordType: 'TOOL_EXECUTION_RECEIPT',
      id: identity,
      investigationId,
      recordedAt,
      logicalTool: request.tool,
      owner: ownerForTool(request.tool),
      requestId: request.requestId,
      attemptId: `${request.requestId}:attempt:1`,
      runId,
      stepId: stepId as ToolExecutionReceiptRecord['stepId'],
      startedAt: recordedAt,
      completedAt: recordedAt,
      resultCategory: 'SUCCEEDED',
      metadata: {
        revision: result.revision,
        quality: result.quality,
        provenanceDigest: digest(result.provenance),
      },
    }), operationTime);
  };

  const readDirect = async (
    request: ParallelReadRequest,
    context: OwnerReadContext,
  ): Promise<OwnerReadResult> => {
    const owner = telemetryOwnerFor(request);
    const toolStartedAt = ports.clock.now();
    const toolSpan = safeStartOperationsTelemetrySpan(ports.telemetry, {
      name: 'operations.tool.call',
      kind: 'INTERNAL',
      correlation: telemetryCorrelationFor({
        investigationId: context.investigationId,
        runId: context.runId,
        ...(context.stepId === undefined ? {} : { stepId: context.stepId }),
        ...(context.authorization.traceparent === undefined
          ? {} : { traceparent: context.authorization.traceparent }),
        ...(context.authorization.tracestate === undefined
          ? {} : { tracestate: context.authorization.tracestate }),
      }),
      attributes: { operation: 'READ_OWNER', logicalTool: request.tool, owner },
    });
    let toolOutcome: OperationsTelemetryOutcome = 'ERROR';
    try {
      let authorizedContext: OwnerReadContext = context;
      if (ports.toolAuthorizationReader !== undefined) {
        const authorizationStartedAt = ports.clock.now();
        const authorizationSpan = safeStartOperationsTelemetrySpan(ports.telemetry, {
          name: 'operations.authorization',
          kind: 'CLIENT',
          correlation: telemetryCorrelationFor({
            investigationId: context.investigationId,
            runId: context.runId,
            ...(context.stepId === undefined ? {} : { stepId: context.stepId }),
            ...(toolSpan.traceparent === undefined ? {} : { traceparent: toolSpan.traceparent }),
            ...(toolSpan.tracestate === undefined ? {} : { tracestate: toolSpan.tracestate }),
          }),
          attributes: {
            operation: 'AUTHORIZE_TOOL',
            owner: 'platform-gateway',
            logicalTool: request.tool,
          },
        });
        try {
          const authorizationContext: OwnerReadContext = {
            ...context,
            authorization: {
              ...context.authorization,
              ...(authorizationSpan.traceparent === undefined
                ? {} : { traceparent: authorizationSpan.traceparent }),
              ...(authorizationSpan.tracestate === undefined
                ? {} : { tracestate: authorizationSpan.tracestate }),
            },
          };
          const toolGrant = await ports.toolAuthorizationReader.authorize({
            request,
            context: authorizationContext,
          });
          authorizedContext = {
            ...context,
            authorization: {
              ...context.authorization,
              delegationGrant: toolGrant.delegationGrant,
              toolDelegationGrants: {
                ...context.authorization.toolDelegationGrants,
                [request.tool]: toolGrant.delegationGrant,
              },
              ...(toolGrant.policyRevision === undefined
                ? {}
                : { policyRevision: toolGrant.policyRevision }),
            },
          };
          authorizationSpan.setStatus('SUCCESS');
        } catch (error) {
          authorizationSpan.setStatus('ERROR');
          throw error;
        } finally {
          const authorizationDurationMs = Math.max(0, ports.clock.now() - authorizationStartedAt);
          authorizationSpan.setAttributes({ durationMs: authorizationDurationMs });
          authorizationSpan.end();
        }
      }

      const ownerStartedAt = ports.clock.now();
      const ownerSpan = safeStartOperationsTelemetrySpan(ports.telemetry, {
        name: 'operations.owner.request',
        kind: 'CLIENT',
        correlation: telemetryCorrelationFor({
          investigationId: context.investigationId,
          runId: context.runId,
          ...(context.stepId === undefined ? {} : { stepId: context.stepId }),
          ...(toolSpan.traceparent === undefined ? {} : { traceparent: toolSpan.traceparent }),
          ...(toolSpan.tracestate === undefined ? {} : { tracestate: toolSpan.tracestate }),
        }),
        attributes: { operation: 'READ_OWNER', logicalTool: request.tool, owner },
      });
      try {
        const ownerContext: OwnerReadContext = {
          ...authorizedContext,
          authorization: {
            ...authorizedContext.authorization,
            ...(ownerSpan.traceparent === undefined ? {} : { traceparent: ownerSpan.traceparent }),
            ...(ownerSpan.tracestate === undefined ? {} : { tracestate: ownerSpan.tracestate }),
          },
        };
        let result: OwnerReadResult;
        if (request.tool === 'registry.getSite' || request.tool === 'registry.listSiteAssets') {
          result = await ports.ownerReaders.registry.read({ request, context: ownerContext });
        } else if (request.tool === 'analytics.getEnergySeries') {
          result = await ports.ownerReaders.energyAnalytics.read({ request, context: ownerContext });
        } else {
          throw new InvestigationCoordinatorError(
            'OWNER_REQUEST_INVALID',
            'The Night-energy Investigation requested an unsupported logical Tool.',
          );
        }
        const resultCost = runResourceOwnerResultCost(result);
        ownerSpan.setAttributes({
          outcome: 'SUCCESS',
          ownerRecords: resultCost.ownerRecords,
          payloadBytes: resultCost.payloadBytes,
        });
        ownerSpan.setStatus('SUCCESS');
        toolOutcome = 'SUCCESS';
        return result;
      } catch (error) {
        const outcome = telemetryOutcomeForOwnerError(error);
        ownerSpan.setAttributes({ outcome });
        ownerSpan.setStatus(outcome);
        toolOutcome = outcome;
        throw error;
      } finally {
        const ownerDurationMs = Math.max(0, ports.clock.now() - ownerStartedAt);
        ownerSpan.setAttributes({ durationMs: ownerDurationMs });
        ownerSpan.end();
      }
    } finally {
      const toolDurationMs = Math.max(0, ports.clock.now() - toolStartedAt);
      toolSpan.setAttributes({ outcome: toolOutcome, durationMs: toolDurationMs });
      toolSpan.setStatus(toolOutcome);
      toolSpan.end();
      safeAddOperationsTelemetryCounter(ports.telemetry, {
        name: 'operations_agent_tool_calls_total',
        labels: { logicalTool: request.tool, owner, outcome: toolOutcome },
      });
      safeObserveOperationsTelemetryHistogram(ports.telemetry, {
        name: 'operations_agent_tool_duration_ms',
        value: toolDurationMs,
        labels: { logicalTool: request.tool, owner, outcome: toolOutcome },
      });
    }
  };


  const readBudgetedBatch = async (input: {
    readonly view: OperationsInvestigationView;
    readonly requests: readonly ParallelReadRequest[];
    readonly context: OwnerReadContext;
    readonly operationTime: number;
    readonly stepId: string;
  }): Promise<
    | { readonly outcome: 'READS_COMPLETED'; readonly results: readonly OwnerReadResult[] }
    | { readonly outcome: 'BUDGET_EXHAUSTED'; readonly budget: RunResourceBudgetOutcome }
  > => {
    const stepStartedAt = ports.clock.now();
    const stepSpan = safeStartOperationsTelemetrySpan(ports.telemetry, {
      name: 'operations.runtime.step',
      kind: 'INTERNAL',
      correlation: telemetryCorrelationFor({
        investigationId: input.context.investigationId,
        runId: input.context.runId,
        stepId: input.stepId,
        ...(input.context.authorization.traceparent === undefined
          ? {} : { traceparent: input.context.authorization.traceparent }),
        ...(input.context.authorization.tracestate === undefined
          ? {} : { tracestate: input.context.authorization.tracestate }),
      }),
      attributes: { operation: 'EXECUTE_STEP' },
    });
    let stepOutcome: OperationsTelemetryOutcome = 'ERROR';
    try {
      const readBudget = await checkResourceBudget({
        view: input.view,
        authorization: input.context.authorization,
        at: input.operationTime,
        operationId: runResourceReadBatchOperationId(input.requests),
        cost: runResourceReadBatchCost(input.requests),
      });
      if (readBudget !== null) {
        stepOutcome = 'EXHAUSTED';
        return { outcome: 'BUDGET_EXHAUSTED', budget: readBudget };
      }
      const stepContext: OwnerReadContext = {
        ...input.context,
        stepId: input.stepId,
        authorization: {
          ...input.context.authorization,
          ...(stepSpan.traceparent === undefined ? {} : { traceparent: stepSpan.traceparent }),
          ...(stepSpan.tracestate === undefined ? {} : { tracestate: stepSpan.tracestate }),
        },
      };
      const results = await Promise.all(input.requests.map((request) => (
        readDirect(request, stepContext)
      )));
      const resultBudget = await checkResourceBudget({
        view: input.view,
        authorization: input.context.authorization,
        at: ports.clock.now(),
        operationId: runResourceOwnerResultBatchOperationId(input.requests),
        cost: runResourceOwnerResultBatchCost(results),
      });
      if (resultBudget !== null) {
        stepOutcome = 'EXHAUSTED';
        return { outcome: 'BUDGET_EXHAUSTED', budget: resultBudget };
      }
      stepOutcome = 'SUCCESS';
      return { outcome: 'READS_COMPLETED', results };
    } catch (error) {
      stepOutcome = telemetryOutcomeForOwnerError(error);
      throw error;
    } finally {
      const stepDurationMs = Math.max(0, ports.clock.now() - stepStartedAt);
      stepSpan.setAttributes({ outcome: stepOutcome, durationMs: stepDurationMs });
      stepSpan.setStatus(stepOutcome);
      stepSpan.end();
      safeObserveOperationsTelemetryHistogram(ports.telemetry, {
        name: 'operations_agent_operation_duration_ms',
        value: stepDurationMs,
        labels: { operation: 'EXECUTE_STEP', outcome: stepOutcome },
      });
    }
  };


  const snapshot = async (
    view: OperationsInvestigationView,
  ): Promise<SiteNightEnergyInvestigationView> => {
    const persisted = await ports.investigationRepository.get(view.id);
    if (persisted === null) {
      throw new InvestigationCoordinatorError(
        'INVESTIGATION_NOT_FOUND',
        'The requested Investigation was not found.',
      );
    }
    const records = await Promise.all([
      ...view.evidenceIds,
      ...view.analysisReferenceIds,
      ...view.findingIds,
      ...view.toolReceiptIds,
      ...view.acceptedOperatorInputIds,
    ].map((identity) => ports.businessRecordRepository.get(view.id, identity)));
    if (records.some((record) => record === null)) {
      throw new InvestigationCoordinatorError(
        'INVALID_INVESTIGATION_STATE',
        'The Investigation references a missing typed business record.',
      );
    }
    const typed = records as InvestigationBusinessRecord[];
    const evidence = typed.filter((record): record is EvidenceRecord => record.recordType === 'EVIDENCE');
    const analysisReferences = typed.filter(
      (record): record is AnalysisReferenceRecord => record.recordType === 'ANALYSIS_REFERENCE',
    );
    const findingRecords = typed.filter(
      (record): record is FindingRecord => record.recordType === 'FINDING',
    );
    const findings: SiteNightEnergyFindingView[] = findingRecords.map((record) => {
      const { synthesis: _synthesis, ...publicFinding } = record;
      return publicFinding;
    });
    const toolReceipts = typed.filter(
      (record): record is ToolExecutionReceiptRecord => record.recordType === 'TOOL_EXECUTION_RECEIPT',
    );
    const acceptedOperatorInputs = typed.filter(
      (record): record is OperatorInputAcceptedRecord => record.recordType === 'OPERATOR_INPUT_ACCEPTED',
    );
    const active = view.runs.find(({ id }) => id === view.activeRunId);
    const budgetRun = active ?? view.runs.at(-1);
    const budgetSnapshot = budgetRun === undefined
      ? null
      : await ports.budgetGuard.get(view.id, budgetRun.id);
    const resourceBudget = budgetSnapshot === null
      ? null
      : toRunResourceBudgetOutcome(budgetSnapshot);
    const lastFinding = findings.at(-1);
    return {
      schemaVersion: 1,
      id: view.id,
      scope: { ...view.scope },
      status: view.status,
      revision: view.revision,
      createdAt: persisted.snapshot().createdAt,
      activeRun: active === undefined || (active.status !== 'ACTIVE'
        && active.status !== 'PAUSED'
        && active.status !== 'WAITING_FOR_OPERATOR_INPUT')
        ? null
        : { id: active.id, status: active.status, startedAt: active.startedAt },
      outcome: lastFinding === undefined
        ? resourceBudget === null ? null : 'UNABLE_TO_CONCLUDE'
        : lastFinding.conclusion.status === 'SUPPORTED'
          ? 'SUPPORTED_SITE_FINDING'
          : 'UNABLE_TO_CONCLUDE',
      resourceBudget,
      evidence,
      analysisReferences,
      findings,
      toolReceipts,
      operatorInputRequest: view.activeOperatorInputRequest,
      acceptedOperatorInputs,
    };
  };

  const registryRequests = (investigationId: string, siteId: string): readonly [
    ParallelReadRequest,
    ParallelReadRequest,
  ] => [{
    requestId: requestId(investigationId, 'registry-site'),
    tool: 'registry.getSite',
    input: { siteId },
  }, {
    requestId: requestId(investigationId, 'registry-assets'),
    tool: 'registry.listSiteAssets',
    input: { siteId },
  }];

  const summary = (view: SiteNightEnergyInvestigationView): SiteNightEnergyInvestigationSummary => ({
    schemaVersion: 1,
    id: view.id,
    scope: view.scope,
    status: view.status,
    revision: view.revision,
    createdAt: view.createdAt,
    outcome: view.outcome,
    resourceBudget: view.resourceBudget,
    evidenceCount: view.evidence.length,
    analysisReferenceCount: view.analysisReferences.length,
    findingCount: view.findings.length,
    toolReceiptCount: view.toolReceipts.length,
    acceptedOperatorInputCount: view.acceptedOperatorInputs.length,
  });

  return Object.freeze({
    async start(command: StartSiteNightEnergyInvestigationCommand) {
      const scope = requireSiteScope({
        tenantId: requireIdentity(command.tenantId, 'Tenant identity'),
        siteId: requireIdentity(command.siteId, 'Site identity'),
        assetId: null,
        deviceId: null,
      });
      const coordinator = genericFor(scope);
      const created = await coordinator.create({ scope });
      const started = await coordinator.start({
        investigationId: created.id,
        runtimeRevision: policy.runtimeRevision,
        expectedRevision: created.revision,
      });
      return snapshot(started);
    },

    async list(query: ListSiteNightEnergyInvestigationsQuery) {
      const scope = requireSiteScope({
        tenantId: requireIdentity(query.tenantId, 'Tenant identity'),
        siteId: requireIdentity(query.siteId, 'Site identity'),
        assetId: null,
        deviceId: null,
      });
      requireAllowed(await ports.authorizationDecisionReader.authorizeScope({
        scope,
        action: 'LIST_INVESTIGATIONS',
      }));
      const limit = query.limit ?? 50;
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 50) {
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          'Investigation list limit must be between 1 and 50.',
        );
      }
      if (ports.investigationRepository.listByScope === undefined) {
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          'The Investigation repository does not support Site listing.',
        );
      }
      const persisted = await ports.investigationRepository.listByScope({
        tenantId: scope.tenantId,
        siteId: scope.siteId as string,
        limit,
      });
      const projected = await Promise.all(
        persisted.map((investigation) => snapshot(investigation.view())),
      );
      const investigations = projected
        .filter((view) => view.scope.tenantId === scope.tenantId
          && view.scope.siteId === scope.siteId)
        .sort((left, right) => right.createdAt - left.createdAt || right.id.localeCompare(left.id))
        .slice(0, limit)
        .map(summary);
      return Object.freeze({
        schemaVersion: 1,
        investigations: Object.freeze(investigations),
      });
    },

    async get(query: SiteNightEnergyInvestigationQuery) {
      return snapshot(await loadAuthorizedView(requireIdentity(
        query.investigationId,
        'Investigation identity',
      )));
    },

    async requestOperatorInput(query: SiteNightEnergyInvestigationQuery) {
      const view = await loadAuthorizedView(requireIdentity(
        query.investigationId,
        'Investigation identity',
      ));
      const authority = activeAuthority(view);
      const requested = await genericFor(view.scope).requestOperatorInput({
        investigationId: view.id,
        runId: authority.runId,
        leaseId: authority.leaseId,
        expectedRevision: view.revision,
        kind: 'SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION',
      });
      return snapshot(requested);
    },

    async acceptOperatorInput(command: AcceptSiteNightEnergyOperatorInputCommand) {
      const investigationId = requireIdentity(command.investigationId, 'Investigation identity');
      const current = await loadAuthorizedView(investigationId);
      const result = await genericFor(current.scope).acceptOperatorInput({
        investigationId,
        requestId: requireIdentity(command.requestId, 'Operator Input Request identity'),
        expectedRevision: createInvestigationRevision(command.expectedRevision),
        idempotencyKey: requireIdentity(command.idempotencyKey, 'Idempotency Key'),
        values: command.values,
      });
      return {
        outcome: result.outcome,
        investigation: await snapshot(result.investigation),
      };
    },

    async cancel(query: SiteNightEnergyInvestigationQuery) {
      const view = await loadAuthorizedView(requireIdentity(
        query.investigationId,
        'Investigation identity',
      ));
      if (view.status === 'CANCELLED') return snapshot(view);
      const cancelled = await genericFor(view.scope).cancel({
        investigationId: view.id,
        expectedRevision: view.revision,
      });
      return snapshot(cancelled);
    },

    async advance(query: SiteNightEnergyInvestigationQuery) {
      let view = await loadAuthorizedView(requireIdentity(
        query.investigationId,
        'Investigation identity',
      ));
      if (view.status === 'COMPLETED') return snapshot(view);
      if (view.status !== 'RUNNING') {
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          'Only a running Night-energy Investigation can advance.',
        );
      }
      const scope = requireSiteScope(view.scope);
      const siteId = scope.siteId;
      if (siteId === null) {
        throw new InvestigationCoordinatorError(
          'INVALID_INVESTIGATION_STATE',
          'Night-energy Investigations require a Site identity.',
        );
      }
      const operationTime = ports.clock.now();
      const coordinator = genericFor(scope, operationTime);
      const authority = activeAuthority(view);
      const existingBudget = await ports.budgetGuard.get(view.id, authority.runId);
      if (existingBudget !== null && existingBudget.exhaustion !== null) return snapshot(view);
      const authorization = requireAllowed(await ports.authorizationDecisionReader.authorizeScope({
        scope,
        action: 'ADVANCE_AGENT_RUN',
      }));
      const context: OwnerReadContext = {
        investigationId: view.id,
        runId: authority.runId,
        scope,
        authorization,
        correlationId: `${view.id}:${authority.runId}`,
      };
      const registry = registryRequests(view.id, siteId);
      const registryReceiptIdentities = registry.map((request) => (
        recordId(view.id, `receipt:${request.requestId}`)
      ));
      let registryResults: readonly OwnerReadResult[];
      const existingCheckpoint = await ports.checkpointRepository.load(view.id, authority.runId);
      if (existingCheckpoint !== null
        || registryReceiptIdentities.every((identity) => view.toolReceiptIds.includes(identity))) {
        const directRegistry = await readBudgetedBatch({
          view,
          requests: registry,
          context,
          operationTime,
          stepId: 'collect-registry-context',
        });
        if (directRegistry.outcome === 'BUDGET_EXHAUSTED') return snapshot(view);
        registryResults = directRegistry.results;
      } else {
        const advanced = await coordinator.advance({
          investigationId: view.id,
          runId: authority.runId,
          leaseId: authority.leaseId,
          expectedRevision: view.revision,
        });
        if (advanced.outcome === 'BUDGET_EXHAUSTED') return snapshot(advanced.investigation);
        registryResults = advanced.results;
        if (advanced.plan.batches.length !== 1
          || registryResults.length !== 2
          || !registry.every((request) => (
            registryResults.some((result) => result.requestId === request.requestId)
          ))) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'The Runtime READ plan is outside the Site night-energy program.',
          );
        }
        view = advanced.investigation;
      }
      const siteResult = registryResults.find(({ requestId: identity }) => identity === registry[0].requestId);
      const assetsResult = registryResults.find(
        ({ requestId: identity }) => identity === registry[1].requestId,
      );
      if (siteResult === undefined || assetsResult === undefined) {
        throw new InvestigationCoordinatorError(
          'OWNER_RESPONSE_INVALID',
          'The Runtime did not return complete Registry Site context.',
        );
      }
      const site = decodeSite(siteResult, scope);
      const assets = decodeAssets(assetsResult, scope);
      for (const request of registry) {
        const result = registryResults.find(({ requestId: identity }) => identity === request.requestId);
        if (result === undefined) continue;
        const receipt = await receiptFor(
          view.id,
          authority.runId,
          'collect-registry-context',
          request,
          result,
          operationTime,
        );
        view = await commitRecord(coordinator, view, 'collect-registry-context', receipt);
      }

      const targetLocalDate = shiftLocalDate(localDateAt(
        view.runs.find(({ id }) => id === authority.runId)?.startedAt ?? operationTime,
        site.site.timezone,
      ), -1);
      const baselineLocalDate = shiftLocalDate(targetLocalDate, -policy.baselineOffsetDays);
      const periods = planSiteNightEnergyPeriods({
        timezone: site.site.timezone,
        window: policy.window,
        targetLocalDate,
        baselineLocalDate,
      });
      const energyRequests: readonly [ParallelReadRequest, ParallelReadRequest] = [{
        requestId: requestId(view.id, 'energy-target'),
        tool: 'analytics.getEnergySeries',
        input: {
          tenantId: scope.tenantId,
          siteId,
          energyType: 'electricity',
          granularity: 'hour',
          timezone: site.site.timezone,
          from: periods.targetPeriod.from,
          to: periods.targetPeriod.to,
          qualityPolicy: policy.qualityPolicy,
        },
      }, {
        requestId: requestId(view.id, 'energy-baseline'),
        tool: 'analytics.getEnergySeries',
        input: {
          tenantId: scope.tenantId,
          siteId,
          energyType: 'electricity',
          granularity: 'hour',
          timezone: site.site.timezone,
          from: periods.baselinePeriod.from,
          to: periods.baselinePeriod.to,
          qualityPolicy: policy.qualityPolicy,
        },
      }];
      const energyRead = await readBudgetedBatch({
        view,
        requests: energyRequests,
        context,
        operationTime,
        stepId: 'collect-energy-periods',
      });
      if (energyRead.outcome === 'BUDGET_EXHAUSTED') return snapshot(view);
      const energyResults = energyRead.results;
      for (const [index, request] of energyRequests.entries()) {
        const receipt = await receiptFor(
          view.id,
          authority.runId,
          'collect-energy-periods',
          request,
          energyResults[index] as OwnerReadResult,
          operationTime,
        );
        view = await commitRecord(coordinator, view, 'collect-energy-periods', receipt);
      }

      const analysis = analyzeSiteNightEnergy({
        site: {
          tenantId: scope.tenantId,
          siteId,
          timezone: site.site.timezone,
          assetIds: assets.assets.map(({ id }) => id),
        },
        window: policy.window,
        targetLocalDate,
        baselineLocalDate,
        increaseThresholdPercent: policy.increaseThresholdPercent,
        qualityPolicy: policy.qualityPolicy,
        targetSeries: decodeEnergy(energyResults[0] as OwnerReadResult),
        baselineSeries: decodeEnergy(energyResults[1] as OwnerReadResult),
      });
      const allResults = [siteResult, assetsResult, ...energyResults];
      const readinessIdentity = recordId(view.id, 'evidence:energy-readiness');
      const readiness = await ensureRecord(view.id, readinessIdentity, (recordedAt) => ({
        schemaVersion: 1,
        recordType: 'EVIDENCE',
        id: readinessIdentity,
        investigationId: view.id,
        recordedAt,
        evidenceKind: analysis.status === 'SUPPORTED_SITE_FINDING'
          ? 'SITE_ENERGY_SERIES_READY'
          : 'SITE_ENERGY_SERIES_READINESS_ASSESSED',
        classification: 'FACT',
        statement: analysis.status === 'SUPPORTED_SITE_FINDING'
          ? analysis.evidence[0].statement
          : `Energy Series readiness failed: ${analysis.blockers.map(({ code }) => code).join(', ')}.`,
        analysisReferenceDigest: analysis.analysisReference.digest,
        sources: allResults.map((result) => sourceFor(result, recordedAt)),
      }), operationTime);
      view = await commitRecord(coordinator, view, 'evaluate-energy-readiness', readiness);

      const analysisIdentity = recordId(view.id, 'analysis:night-energy-comparison');
      const analysisRecord = await ensureRecord(view.id, analysisIdentity, (recordedAt) => ({
        schemaVersion: 1,
        recordType: 'ANALYSIS_REFERENCE',
        id: analysisIdentity,
        investigationId: view.id,
        recordedAt,
        analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
        authority: 'DETERMINISTIC_ALGORITHM',
        algorithmVersion: analysis.analysisReference.algorithmVersion,
        policyVersion: 'site-night-energy-policy/v1',
        inputEvidenceIds: [readiness.id],
        parameterDigest: digest({
          window: policy.window,
          targetLocalDate,
          baselineLocalDate,
          threshold: policy.increaseThresholdPercent,
          qualityPolicy: policy.qualityPolicy,
        }),
        resultDigest: analysis.analysisReference.digest,
        executedAt: recordedAt,
        outcome: analysis.status,
      }), operationTime);
      view = await commitRecord(coordinator, view, 'analyze-night-energy', analysisRecord);

      const evidenceRecords: EvidenceRecord[] = [readiness];
      if (analysis.status === 'SUPPORTED_SITE_FINDING') {
        const comparisonIdentity = recordId(view.id, 'evidence:energy-comparison');
        const comparison = await ensureRecord(view.id, comparisonIdentity, (recordedAt) => ({
          schemaVersion: 1,
          recordType: 'EVIDENCE',
          id: comparisonIdentity,
          investigationId: view.id,
          recordedAt,
          evidenceKind: 'SITE_ENERGY_PERIOD_COMPARISON',
          classification: 'ALGORITHM_RESULT',
          statement: analysis.evidence[1].statement,
          analysisReferenceDigest: analysis.analysisReference.digest,
          sources: energyResults.map((result) => sourceFor(result, recordedAt)),
        }), operationTime);
        view = await commitRecord(coordinator, view, 'record-energy-comparison', comparison);
        evidenceRecords.push(comparison);
      }

      const findingIdentity = recordId(view.id, 'finding:night-energy');
      const existingFinding = await ports.businessRecordRepository.get(view.id, findingIdentity);
      if (existingFinding !== null && existingFinding.recordType !== 'FINDING') {
        throw new InvestigationCoordinatorError(
          'DUPLICATE_RECORD',
          `Business record ${findingIdentity} already exists with another record type.`,
        );
      }
      if (existingFinding === null && ports.findingSynthesizer !== undefined) {
        const modelBudget = await checkResourceBudget({
          view,
          authorization,
          at: operationTime,
          operationId: `finding-synthesis:${findingIdentity}`,
          cost: { ...ZERO_RUN_RESOURCE_BUDGET_COST, modelInvocations: 1 },
        });
        if (modelBudget !== null) return snapshot(view);
      }
      const deterministicStatement = analysis.status === 'SUPPORTED_SITE_FINDING'
        ? analysis.siteFinding.statement
        : 'The Site night-energy Investigation cannot produce a supported conclusion.';
      let synthesis: FindingSynthesisDecision | null = null;
      if (existingFinding === null) {
        const modelStartedAt = ports.clock.now();
        const modelSpan = safeStartOperationsTelemetrySpan(ports.telemetry, {
          name: 'operations.model.call',
          kind: 'CLIENT',
          correlation: telemetryCorrelationFor({
            investigationId: view.id,
            runId: authority.runId,
            stepId: 'record-night-energy-finding',
            ...(context.authorization.traceparent === undefined
              ? {} : { traceparent: context.authorization.traceparent }),
            ...(context.authorization.tracestate === undefined
              ? {} : { tracestate: context.authorization.tracestate }),
          }),
          attributes: { operation: 'SYNTHESIZE_FINDING' },
        });
        try {
          synthesis = await synthesizeFinding({
            ...(ports.findingSynthesizer === undefined
              ? {}
              : { synthesizer: ports.findingSynthesizer }),
            timeoutMs: policy.findingSynthesisTimeoutMs,
            investigationId: view.id,
            scope,
            expectedClassification: analysis.status === 'SUPPORTED_SITE_FINDING'
              ? 'INFERENCE'
              : 'UNABLE_TO_CONCLUDE',
            deterministicStatement,
            evidence: evidenceRecords,
            analysisReferences: [analysisRecord],
          });
          const tokenUsage = synthesis.invocation?.tokenUsage ?? null;
          const modelOutcome: OperationsTelemetryOutcome = synthesis.source === 'MODEL'
            ? 'SUCCESS'
            : 'PARTIAL';
          modelSpan.setAttributes({
            outcome: modelOutcome,
            partial: synthesis.source !== 'MODEL',
            ...(tokenUsage === null ? {} : {
              modelInputTokens: tokenUsage.inputTokens,
              modelOutputTokens: tokenUsage.outputTokens,
            }),
          });
          modelSpan.setStatus(modelOutcome);
          if (tokenUsage !== null) {
            safeAddOperationsTelemetryCounter(ports.telemetry, {
              name: 'operations_agent_model_tokens',
              value: tokenUsage.inputTokens + tokenUsage.outputTokens,
              labels: { operation: 'SYNTHESIZE_FINDING', outcome: modelOutcome },
            });
          }
        } catch (error) {
          modelSpan.setStatus('ERROR');
          throw error;
        } finally {
          const modelDurationMs = Math.max(0, ports.clock.now() - modelStartedAt);
          modelSpan.setAttributes({ durationMs: modelDurationMs });
          modelSpan.end();
          safeObserveOperationsTelemetryHistogram(ports.telemetry, {
            name: 'operations_agent_operation_duration_ms',
            value: modelDurationMs,
            labels: { operation: 'SYNTHESIZE_FINDING' },
          });
        }
      }
      const synthesisProvenance = synthesis === null ? undefined : synthesisProvenanceFor(synthesis);
      const finding = existingFinding ?? await ensureRecord(
        view.id,
        findingIdentity,
        (recordedAt): FindingRecord => (
          analysis.status === 'SUPPORTED_SITE_FINDING'
            ? {
              schemaVersion: 1,
              recordType: 'FINDING',
              id: findingIdentity,
              investigationId: view.id,
              recordedAt,
              findingKind: analysis.siteFinding.kind,
              classification: 'INFERENCE',
              statement: synthesis?.statement ?? deterministicStatement,
              evidenceIds: synthesis?.evidenceIds ?? evidenceRecords.map(({ id }) => id),
              analysisReferenceIds: [analysisRecord.id],
              ...(synthesisProvenance === undefined ? {} : { synthesis: synthesisProvenance }),
              conclusion: {
                status: 'SUPPORTED',
                scope: 'SITE',
                tenantId: scope.tenantId,
                siteId,
              },
            }
            : {
              schemaVersion: 1,
              recordType: 'FINDING',
              id: findingIdentity,
              investigationId: view.id,
              recordedAt,
              findingKind: 'UNABLE_TO_CONCLUDE',
              classification: 'INFERENCE',
              statement: synthesis?.statement ?? deterministicStatement,
              evidenceIds: synthesis?.evidenceIds ?? evidenceRecords.map(({ id }) => id),
              analysisReferenceIds: [analysisRecord.id],
              ...(synthesisProvenance === undefined ? {} : { synthesis: synthesisProvenance }),
              conclusion: {
                status: 'UNABLE_TO_CONCLUDE',
                scope: 'SITE',
                reasonCode: analysis.blockers[0]?.code ?? 'ENERGY_READINESS_FAILED',
                detail: analysis.blockers.map(({ detail }) => detail).join(' '),
                requiredNext: analysis.assetAttribution.requiredNext,
              },
            }
        ),
        operationTime,
      ) as FindingRecord;
      view = await commitRecord(coordinator, view, 'record-night-energy-finding', finding);

      const latestAuthority = activeAuthority(view);
      view = await coordinator.complete({
        investigationId: view.id,
        runId: latestAuthority.runId,
        leaseId: latestAuthority.leaseId,
        expectedRevision: view.revision,
      });
      return snapshot(view);
    },
  });
};

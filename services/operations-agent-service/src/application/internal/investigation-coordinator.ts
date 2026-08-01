import {
  OPERATIONS_AGENT_RUNTIME_READ_TOOLS,
  InvestigationBusinessRecordError,
  OperationsInvestigation,
  OperationsInvestigationError,
  businessRecordsEqual,
  createIdempotencyKey,
  createInvestigationBusinessRecord,
  createStepIdentity,
  type AgentRunView,
  type CommittedEffectKind,
  type CommittedEffectView,
  type InvestigationBusinessRecord,
  type InvestigationRevision,
  type InvestigationScope,
  type OperatorInputAcceptedRecord,
  type OperatorInputAcceptedValues,
  type OperatorInputRequestKind,
  type OperationsInvestigationErrorCode,
  type OperationsInvestigationView,
} from '../../domain/index.js';
import {
  InvestigationRepositoryConflictError,
  OwnerReadError,
  type ApplicationEvent,
  type AuditRecord,
  type AuthorizationDecision,
  type InvestigationAuthorizationAction,
  type InvestigationCoordinatorPorts,
  type OwnerReadContext,
  type OwnerReadResult,
  type ParallelReadRequest,
  type RunResourceBudgetCost,
  type RunResourceBudgetDimension,
  type RunResourceBudgetOutcome,
  type RuntimePlanningContext,
  type RuntimePlanningResult,
  type RuntimeReadPlan,
} from './ports.js';
import { OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY } from './generated-runtime-control-contract.js';
import {
  DEFAULT_RUN_RESOURCE_BUDGET_POLICY,
  ZERO_RUN_RESOURCE_BUDGET_COST,
  normalizeRunResourceBudgetPolicy,
  runResourceEffectOperationId,
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

export type InvestigationCoordinatorErrorCode =
  | 'INVESTIGATION_NOT_FOUND'
  | 'AUTHORIZATION_DENIED'
  | 'LEASE_CONFLICT'
  | 'REVISION_CONFLICT'
  | 'DUPLICATE_EFFECT'
  | 'DUPLICATE_RECORD'
  | 'BUDGET_EXHAUSTED'
  | 'UNABLE_TO_CONCLUDE'
  | 'OWNER_REQUEST_INVALID'
  | 'OWNER_RESOURCE_NOT_FOUND'
  | 'OWNER_READ_TIMEOUT'
  | 'OWNER_READ_UNAVAILABLE'
  | 'OWNER_RESPONSE_TOO_LARGE'
  | 'OWNER_RESPONSE_INVALID'
  | 'OPERATOR_INPUT_CONFLICT'
  | 'UNTRUSTED_CONTENT_REJECTED'
  | 'INVALID_INVESTIGATION_STATE';

export class InvestigationCoordinatorError extends Error {
  readonly code: InvestigationCoordinatorErrorCode;

  constructor(code: InvestigationCoordinatorErrorCode, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'InvestigationCoordinatorError';
    this.code = code;
  }
}

export interface CreateInvestigationCommand {
  readonly scope: InvestigationScope;
}

export interface StartInvestigationCommand {
  readonly investigationId: string;
  readonly runtimeRevision: string;
  readonly expectedRevision: InvestigationRevision;
}

export type ReopenInvestigationCommand = StartInvestigationCommand;

export interface GetInvestigationQuery {
  readonly investigationId: string;
}

export interface AdvanceInvestigationCommand {
  readonly investigationId: string;
  readonly runId: string;
  readonly leaseId: string;
  readonly expectedRevision: InvestigationRevision;
}

export type AdvanceInvestigationResult =
  | {
    readonly outcome: 'READ_PLAN_COMPLETED';
    readonly investigation: OperationsInvestigationView;
    readonly plan: RuntimeReadPlan;
    readonly results: readonly OwnerReadResult[];
    readonly checkpointId: string;
  }
  | {
    readonly outcome: 'BUDGET_EXHAUSTED';
    readonly investigation: OperationsInvestigationView;
    readonly budget: RunResourceBudgetOutcome;
  };

export interface RunLeaseMutationCommand {
  readonly investigationId: string;
  readonly runId: string;
  readonly leaseId: string;
  readonly expectedRevision: InvestigationRevision;
}

export interface ResumeInvestigationCommand {
  readonly investigationId: string;
  readonly runId: string;
  readonly expectedRevision: InvestigationRevision;
}

export interface RequestOperatorInputCommand extends RunLeaseMutationCommand {
  readonly kind: OperatorInputRequestKind;
}

export interface AcceptOperatorInputCommand {
  readonly investigationId: string;
  readonly requestId: string;
  readonly expectedRevision: InvestigationRevision;
  readonly idempotencyKey: string;
  readonly values: OperatorInputAcceptedValues;
}

export interface AcceptOperatorInputResult {
  readonly outcome: 'COMMITTED' | 'DUPLICATE';
  readonly investigation: OperationsInvestigationView;
  readonly record: OperatorInputAcceptedRecord;
}

export interface CancelInvestigationCommand {
  readonly investigationId: string;
  readonly expectedRevision: InvestigationRevision;
}

export interface CommitInvestigationEffectCommand extends RunLeaseMutationCommand {
  readonly stepId: string;
  readonly idempotencyKey: string;
  readonly kind: CommittedEffectKind;
  readonly recordId: string;
  readonly record?: InvestigationBusinessRecord;
}

export interface CommitInvestigationEffectResult {
  readonly outcome: 'COMMITTED' | 'DUPLICATE';
  readonly investigation: OperationsInvestigationView;
  readonly effect: CommittedEffectView;
  readonly record?: InvestigationBusinessRecord;
}

export interface InvestigationCoordinator {
  create(command: CreateInvestigationCommand): Promise<OperationsInvestigationView>;
  start(command: StartInvestigationCommand): Promise<OperationsInvestigationView>;
  reopen(command: ReopenInvestigationCommand): Promise<OperationsInvestigationView>;
  advance(command: AdvanceInvestigationCommand): Promise<AdvanceInvestigationResult>;
  commitEffect(command: CommitInvestigationEffectCommand): Promise<CommitInvestigationEffectResult>;
  pause(command: RunLeaseMutationCommand): Promise<OperationsInvestigationView>;
  resume(command: ResumeInvestigationCommand): Promise<OperationsInvestigationView>;
  requestOperatorInput(command: RequestOperatorInputCommand): Promise<OperationsInvestigationView>;
  acceptOperatorInput(command: AcceptOperatorInputCommand): Promise<AcceptOperatorInputResult>;
  cancel(command: CancelInvestigationCommand): Promise<OperationsInvestigationView>;
  complete(command: RunLeaseMutationCommand): Promise<OperationsInvestigationView>;
  fail(command: RunLeaseMutationCommand): Promise<OperationsInvestigationView>;
  get(query: GetInvestigationQuery): Promise<OperationsInvestigationView>;
}

const leaseErrorCodes = new Set<OperationsInvestigationErrorCode>([
  'LEASE_INVALID',
  'LEASE_ID_REUSED',
  'LEASE_MISMATCH',
  'LEASE_EXPIRED',
  'RUN_NOT_ACTIVE',
]);

const duplicateErrorCodes = new Set<OperationsInvestigationErrorCode>([
  'IDEMPOTENCY_KEY_REUSED',
  'EFFECT_RECORD_ALREADY_COMMITTED',
]);

const mapApplicationError = (error: unknown): never => {
  if (error instanceof InvestigationCoordinatorError) throw error;
  if (error instanceof InvestigationRepositoryConflictError) {
    const code: InvestigationCoordinatorErrorCode = error.code === 'LEASE_CONFLICT'
      ? 'LEASE_CONFLICT'
      : error.code === 'DUPLICATE_EFFECT'
        ? 'DUPLICATE_EFFECT'
        : error.code === 'DUPLICATE_RECORD'
          ? 'DUPLICATE_RECORD'
          : error.code === 'REVISION_CONFLICT'
            ? 'REVISION_CONFLICT'
            : 'INVALID_INVESTIGATION_STATE';
    throw new InvestigationCoordinatorError(code, error.message, { cause: error });
  }
  if (error instanceof OwnerReadError) {
    throw new InvestigationCoordinatorError(error.code, error.message, { cause: error });
  }
  if (error instanceof InvestigationBusinessRecordError) {
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      error.message,
      { cause: error },
    );
  }
  if (error instanceof OperationsInvestigationError) {
    if (error.code === 'REVISION_STALE') {
      throw new InvestigationCoordinatorError('REVISION_CONFLICT', error.message, { cause: error });
    }
    if (leaseErrorCodes.has(error.code)) {
      throw new InvestigationCoordinatorError('LEASE_CONFLICT', error.message, { cause: error });
    }
    if (error.code === 'OPERATOR_INPUT_INVALID'
      || error.code === 'OPERATOR_INPUT_REQUEST_MISMATCH') {
      throw new InvestigationCoordinatorError(
        'OPERATOR_INPUT_CONFLICT',
        error.message,
        { cause: error },
      );
    }
    if (duplicateErrorCodes.has(error.code)) {
      throw new InvestigationCoordinatorError('DUPLICATE_EFFECT', error.message, { cause: error });
    }
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      error.message,
      { cause: error },
    );
  }
  throw error;
};

const requirePositiveLeaseDuration = (value: number): number => {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error('leaseDurationMs must be a positive safe integer.');
  }
  return value;
};

const normalizeOperatorInputValues = (value: unknown): OperatorInputAcceptedValues => {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new InvestigationCoordinatorError(
      'OPERATOR_INPUT_CONFLICT',
      'Operator Input values must be an object.',
    );
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record);
  if (keys.length !== 2
    || !keys.includes('analysisScope')
    || !keys.includes('operatorNote')
    || (record.analysisScope !== 'SITE_ONLY' && record.analysisScope !== 'DEFER')
    || (record.operatorNote !== null
      && (typeof record.operatorNote !== 'string'
        || record.operatorNote.trim().length === 0
        || record.operatorNote.length > 500))) {
    throw new InvestigationCoordinatorError(
      'OPERATOR_INPUT_CONFLICT',
      'Operator Input values do not match the supported bounded schema.',
    );
  }
  return {
    analysisScope: record.analysisScope,
    operatorNote: record.operatorNote,
  };
};

const operatorInputDigest = (
  requestId: string,
  values: OperatorInputAcceptedValues,
): string => `sha256:${sha256Hex(JSON.stringify({ requestId, values }))}`;

const operatorInputRecordMatches = (
  record: OperatorInputAcceptedRecord,
  requestId: string,
  idempotencyKey: string,
  inputDigest: string,
  values: OperatorInputAcceptedValues,
): boolean => record.requestId === requestId
  && record.idempotencyKey === idempotencyKey
  && record.inputDigest === inputDigest
  && record.values.analysisScope === values.analysisScope
  && record.values.operatorNote === values.operatorNote;

const supportedReadTools = new Set<ParallelReadRequest['tool']>(
  OPERATIONS_AGENT_RUNTIME_READ_TOOLS,
);

const supportedQualities = new Set<OwnerReadResult['quality']>([
  'GOOD',
  'UNCERTAIN',
  'BAD',
  'STALE',
]);

const scopeIsWithin = (
  authorized: InvestigationScope,
  returned: InvestigationScope,
): boolean => (
  authorized.organizationId === returned.organizationId
  && (authorized.siteId === null || authorized.siteId === returned.siteId)
  && (authorized.equipmentId === null || authorized.equipmentId === returned.equipmentId)
  && (authorized.deviceId === null || authorized.deviceId === returned.deviceId)
);

const expectedOwner = (tool: ParallelReadRequest['tool']): OwnerReadResult['owner'] => {
  if (tool === 'registry.getSite' || tool === 'registry.listSiteEquipment') return 'registry';
  if (tool === 'telemetry.getCurrentSnapshot' || tool === 'analytics.getEnergySeries') {
    return 'telemetry-query-service';
  }
  return 'command-service';
};

const validateOwnerResult = (
  request: ParallelReadRequest,
  result: OwnerReadResult,
  authorizedScope: InvestigationScope,
): OwnerReadResult => {
  if (result.requestId !== request.requestId
    || result.owner !== expectedOwner(request.tool)
    || !scopeIsWithin(authorizedScope, result.scope)
    || result.revision.trim().length === 0
    || result.provenance.trim().length === 0
    || !supportedQualities.has(result.quality)) {
    throw new InvestigationCoordinatorError(
      'INVALID_INVESTIGATION_STATE',
      `Owner result for READ request ${request.requestId} failed metadata validation.`,
    );
  }
  return result;
};

const isReadInputRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const hasExactReadInputKeys = (
  value: Record<string, unknown>,
  expected: readonly string[],
): boolean => {
  const actual = Object.keys(value);
  return actual.length === expected.length && actual.every((key) => expected.includes(key));
};

const isNonEmptyReadString = (value: unknown): value is string => (
  typeof value === 'string' && value.trim().length > 0
);

const hasValidReadInput = (request: ParallelReadRequest): boolean => {
  const input: unknown = request.input;
  if (!isReadInputRecord(input)) return false;
  if (request.tool === 'registry.getSite' || request.tool === 'registry.listSiteEquipment') {
    return hasExactReadInputKeys(input, ['siteId']) && isNonEmptyReadString(input.siteId);
  }
  if (request.tool === 'telemetry.getCurrentSnapshot') {
    return (hasExactReadInputKeys(input, ['equipmentId'])
      || hasExactReadInputKeys(input, ['equipmentId', 'pointKeys']))
      && isNonEmptyReadString(input.equipmentId)
      && (input.pointKeys === undefined
        || (Array.isArray(input.pointKeys)
          && input.pointKeys.every(isNonEmptyReadString)));
  }
  if (request.tool === 'analytics.getEnergySeries') {
    return hasExactReadInputKeys(input, [
      'organizationId',
      'siteId',
      'energyType',
      'granularity',
      'timezone',
      'from',
      'to',
      'qualityPolicy',
    ])
      && isNonEmptyReadString(input.organizationId)
      && isNonEmptyReadString(input.siteId)
      && input.energyType === 'electricity'
      && (input.granularity === 'hour'
        || input.granularity === 'day'
        || input.granularity === 'month')
      && isNonEmptyReadString(input.timezone)
      && isNonEmptyReadString(input.from)
      && isNonEmptyReadString(input.to)
      && (input.qualityPolicy === 'VALID_ONLY'
        || input.qualityPolicy === 'VALID_AND_SUSPECT');
  }
  return hasExactReadInputKeys(input, ['equipmentId'])
    && isNonEmptyReadString(input.equipmentId);
};

const createRuntimePlanningContext = (
  investigation: OperationsInvestigationView,
  run: AgentRunView,
): RuntimePlanningContext => Object.freeze({
  schemaVersion: OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.schemaVersion,
  source: OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.source,
  trust: OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.trust,
  investigationId: investigation.id,
  scope: Object.freeze({
    organizationId: investigation.scope.organizationId,
    siteId: investigation.scope.siteId,
    equipmentId: investigation.scope.equipmentId,
    deviceId: investigation.scope.deviceId,
  }),
  revision: investigation.revision,
  runId: run.id,
  runStatus: OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.runStatus,
  runtimeRevision: run.runtimeRevision,
  allowedReadTools: Object.freeze([...OPERATIONS_AGENT_RUNTIME_READ_TOOLS]),
  effectPolicy: OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.effectPolicy,
  scopePolicy: OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.scopePolicy,
  untrustedContentPolicy: OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.untrustedContentPolicy,
});

const requestIsWithinPlanningScope = (
  request: ParallelReadRequest,
  scope: InvestigationScope,
): boolean => {
  if (request.tool === 'registry.getSite' || request.tool === 'registry.listSiteEquipment') {
    return scope.siteId === null || request.input.siteId === scope.siteId;
  }
  if (request.tool === 'analytics.getEnergySeries') {
    return request.input.organizationId === scope.organizationId
      && (scope.siteId === null || request.input.siteId === scope.siteId);
  }
  return scope.equipmentId === null || request.input.equipmentId === scope.equipmentId;
};

const validateRuntimePlanningResult = (value: unknown): RuntimePlanningResult => {
  if (!isReadInputRecord(value) || typeof value.status !== 'string') {
    throw new InvestigationCoordinatorError(
      'UNTRUSTED_CONTENT_REJECTED',
      'Runtime planning result must match the bounded result contract.',
    );
  }
  if (value.status === 'UNABLE_TO_CONCLUDE') {
    if (!hasExactReadInputKeys(value, ['status', 'reasonCode'])
      || value.reasonCode !== 'NO_REMAINING_READ_STEP') {
      throw new InvestigationCoordinatorError(
        'UNTRUSTED_CONTENT_REJECTED',
        'Runtime unable-to-conclude result is outside the bounded result contract.',
      );
    }
    return { status: 'UNABLE_TO_CONCLUDE', reasonCode: value.reasonCode };
  }
  if (value.status !== 'PLANNED'
    || !hasExactReadInputKeys(value, ['status', 'plan', 'checkpoint'])
    || !isReadInputRecord(value.checkpoint)
    || !hasExactReadInputKeys(value.checkpoint, ['position', 'opaqueState'])
    || !isNonEmptyReadString(value.checkpoint.position)
    || typeof value.checkpoint.opaqueState !== 'string') {
    throw new InvestigationCoordinatorError(
      'UNTRUSTED_CONTENT_REJECTED',
      'Runtime planned result is outside the bounded result contract.',
    );
  }
  return value as unknown as RuntimePlanningResult;
};

const countValidatedReads = (
  plan: RuntimeReadPlan,
  context: RuntimePlanningContext,
): number => {
  const planValue: unknown = plan;
  if (!isReadInputRecord(planValue)
    || !hasExactReadInputKeys(planValue, ['batches'])
    || !Array.isArray(planValue.batches)
    || planValue.batches.length === 0) {
    throw new InvestigationCoordinatorError(
      'UNTRUSTED_CONTENT_REJECTED',
      'Runtime READ plan must contain only a non-empty batches array.',
    );
  }
  const batchIds = new Set<string>();
  const requestIds = new Set<string>();
  let count = 0;
  for (const batchValue of planValue.batches) {
    if (!isReadInputRecord(batchValue)
      || !hasExactReadInputKeys(batchValue, ['batchId', 'requests'])
      || !isNonEmptyReadString(batchValue.batchId)
      || !Array.isArray(batchValue.requests)
      || batchValue.requests.length === 0
      || batchIds.has(batchValue.batchId)) {
      throw new InvestigationCoordinatorError(
        'UNTRUSTED_CONTENT_REJECTED',
        'Runtime READ batch shape, identity, or requests are invalid.',
      );
    }
    batchIds.add(batchValue.batchId);
    for (const requestValue of batchValue.requests) {
      if (!isReadInputRecord(requestValue)
        || !hasExactReadInputKeys(requestValue, ['requestId', 'tool', 'input'])
        || !isNonEmptyReadString(requestValue.requestId)
        || requestIds.has(requestValue.requestId)) {
        throw new InvestigationCoordinatorError(
          'UNTRUSTED_CONTENT_REJECTED',
          'Runtime READ request shape or identity is invalid.',
        );
      }
      const request = requestValue as unknown as ParallelReadRequest;
      if (!supportedReadTools.has(request.tool)
        || !context.allowedReadTools.includes(request.tool)) {
        throw new InvestigationCoordinatorError(
          'UNTRUSTED_CONTENT_REJECTED',
          `Runtime requested unsupported or disallowed READ tool ${String(request.tool)}.`,
        );
      }
      if (!hasValidReadInput(request)) {
        throw new InvestigationCoordinatorError(
          'UNTRUSTED_CONTENT_REJECTED',
          `Runtime READ request ${request.requestId} has an invalid fixed-tool input.`,
        );
      }
      if (!requestIsWithinPlanningScope(request, context.scope)) {
        throw new InvestigationCoordinatorError(
          'UNTRUSTED_CONTENT_REJECTED',
          `Runtime READ request ${request.requestId} attempts to widen Investigation Scope.`,
        );
      }
      requestIds.add(request.requestId);
      count += 1;
    }
  }
  return count;
};

export const createInvestigationCoordinator = (
  ports: InvestigationCoordinatorPorts,
): InvestigationCoordinator => {
  const leaseDurationMs = requirePositiveLeaseDuration(ports.leaseDurationMs);
  const resourceBudgetPolicy = normalizeRunResourceBudgetPolicy(
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

  const telemetryOwnerFor = (request: ParallelReadRequest): OperationsTelemetryOwner => {
    if (request.tool === 'registry.getSite' || request.tool === 'registry.listSiteEquipment') {
      return 'registry';
    }
    if (request.tool === 'analytics.getEnergySeries') return 'telemetry-query-service';
    if (request.tool === 'commands.getCapabilities') return 'command-service';
    return 'operations-agent-service';
  };

  const telemetryOutcomeForOwnerError = (error: unknown): OperationsTelemetryOutcome => {
    if (!(error instanceof OwnerReadError)) return 'ERROR';
    if (error.code === 'OWNER_RESOURCE_NOT_FOUND') return 'NOT_FOUND';
    if (error.code === 'OWNER_READ_TIMEOUT') return 'TIMEOUT';
    if (error.code === 'OWNER_READ_UNAVAILABLE') return 'UNAVAILABLE';
    return 'INVALID';
  };

  const checkRunResourceBudget = async (input: {
    readonly investigationId: string;
    readonly run: AgentRunView;
    readonly at: number;
    readonly operationId: string;
    readonly cost: RunResourceBudgetCost;
  }): Promise<RunResourceBudgetOutcome | null> => {
    const startedAt = ports.clock.now();
    const span = safeStartOperationsTelemetrySpan(ports.telemetry, {
      name: 'operations.budget.check',
      kind: 'INTERNAL',
      correlation: telemetryCorrelationFor({
        investigationId: input.investigationId,
        runId: input.run.id,
      }),
      attributes: { operation: 'CHECK_BUDGET' },
    });
    try {
      const decision = await ports.budgetGuard.check({
        investigationId: input.investigationId,
        runId: input.run.id,
        startedAt: input.run.startedAt,
        at: input.at,
        operationId: input.operationId,
        policy: resourceBudgetPolicy,
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
          'Budget Guard denied work without a typed exhaustion outcome.',
        );
      }
      span.setAttributes({
        outcome: outcome.outcome === 'PARTIAL' ? 'PARTIAL' : 'UNABLE_TO_CONCLUDE',
        budgetDimension: outcome.exhaustedDimension,
        budgetConsumed: outcome.consumed,
        budgetLimit: outcome.limit,
        partial: outcome.outcome === 'PARTIAL',
      });
      span.setStatus('EXHAUSTED');
      safeAddOperationsTelemetryCounter(ports.telemetry, {
        name: 'operations_agent_budget_exhaustions_total',
        labels: {
          budgetDimension: outcome.exhaustedDimension,
          outcome: outcome.outcome === 'PARTIAL' ? 'PARTIAL' : 'UNABLE_TO_CONCLUDE',
        },
      });
      safeAddOperationsTelemetryCounter(ports.telemetry, {
        name: 'operations_agent_terminal_outcomes_total',
        labels: {
          operation: 'ADVANCE',
          outcome: outcome.outcome === 'PARTIAL' ? 'PARTIAL' : 'UNABLE_TO_CONCLUDE',
        },
      });
      return outcome;
    } catch (error) {
      span.setStatus('ERROR');
      throw error;
    } finally {
      const durationMs = Math.max(0, ports.clock.now() - startedAt);
      span.setAttributes({ durationMs });
      span.end();
      safeObserveOperationsTelemetryHistogram(ports.telemetry, {
        name: 'operations_agent_operation_duration_ms',
        value: durationMs,
        labels: { operation: 'CHECK_BUDGET' },
      });
    }
  };

  const load = async (investigationId: string): Promise<OperationsInvestigation> => {
    const investigation = await ports.investigationRepository.get(investigationId);
    if (investigation === null) {
      throw new InvestigationCoordinatorError(
        'INVESTIGATION_NOT_FOUND',
        `Operations Investigation ${investigationId} was not found.`,
      );
    }
    return investigation;
  };

  const authorize = async (
    scope: InvestigationScope,
    action: InvestigationAuthorizationAction,
  ): Promise<AuthorizationDecision> => {
    const authorization = await ports.authorizationDecisionReader.authorizeScope({ scope, action });
    if (authorization.decision === 'DENY') {
      throw new InvestigationCoordinatorError(
        'AUTHORIZATION_DENIED',
        authorization.reason ?? 'The requested Investigation Scope is not authorized.',
      );
    }
    return authorization;
  };

  const loadAuthorized = async (
    investigationId: string,
    action: InvestigationAuthorizationAction,
  ): Promise<OperationsInvestigation> => {
    const investigation = await load(investigationId);
    await authorize(investigation.view().scope, action);
    return investigation;
  };

  const recordMutation = async (
    event: ApplicationEvent,
    audit: AuditRecord,
  ): Promise<void> => {
    await ports.applicationOutbox.append(event);
    await ports.auditRecorder.record(audit);
  };

  const persistMutation = async (input: {
    readonly investigation: OperationsInvestigation;
    readonly expectedRevision: InvestigationRevision;
    readonly expectedAuthority?: {
      readonly runId: string;
      readonly leaseId: string;
      readonly at: number;
    };
    readonly effect?: CommittedEffectView;
    readonly record?: InvestigationBusinessRecord;
    readonly occurredAt: number;
    readonly eventType: ApplicationEvent['type'];
    readonly auditAction: AuditRecord['action'];
    readonly runId: string | null;
  }): Promise<OperationsInvestigationView> => {
    const view = input.investigation.view();
    const commitStartedAt = ports.clock.now();
    const commitSpan = safeStartOperationsTelemetrySpan(ports.telemetry, {
      name: 'operations.business.commit',
      kind: 'INTERNAL',
      correlation: telemetryCorrelationFor({
        investigationId: view.id,
        ...(input.runId === null ? {} : { runId: input.runId }),
        ...(input.effect === undefined ? {} : { stepId: input.effect.stepId }),
      }),
      attributes: { operation: 'COMMIT_EFFECT' },
    });
    try {
      await ports.investigationTransaction.save({
        investigation: input.investigation,
        expectedRevision: input.expectedRevision,
        ...(input.expectedAuthority === undefined
          ? {}
          : { expectedAuthority: input.expectedAuthority }),
        ...(input.effect === undefined ? {} : { effect: input.effect }),
        ...(input.record === undefined ? {} : { record: input.record }),
        event: {
          type: input.eventType,
          investigationId: view.id,
          revision: view.revision,
          occurredAt: input.occurredAt,
        },
        audit: {
          action: input.auditAction,
          investigationId: view.id,
          runId: input.runId,
          revision: view.revision,
          occurredAt: input.occurredAt,
        },
      });
      commitSpan.setStatus('SUCCESS');
      safeAddOperationsTelemetryCounter(ports.telemetry, {
        name: 'operations_agent_business_commits_total',
        labels: { operation: 'COMMIT_EFFECT', outcome: 'SUCCESS' },
      });
      return view;
    } catch (error) {
      commitSpan.setStatus('ERROR');
      safeAddOperationsTelemetryCounter(ports.telemetry, {
        name: 'operations_agent_business_commits_total',
        labels: { operation: 'COMMIT_EFFECT', outcome: 'ERROR' },
      });
      throw error;
    } finally {
      const commitDurationMs = Math.max(0, ports.clock.now() - commitStartedAt);
      commitSpan.setAttributes({ durationMs: commitDurationMs });
      commitSpan.end();
      safeObserveOperationsTelemetryHistogram(ports.telemetry, {
        name: 'operations_agent_operation_duration_ms',
        value: commitDurationMs,
        labels: { operation: 'COMMIT_EFFECT' },
      });
    }
  };

  const recordTerminalOutcome = (input: {
    readonly investigationId: string;
    readonly runId?: string | null;
    readonly operation: 'COMPLETE_RUN' | 'FAIL_RUN' | 'CANCEL' | 'ADVANCE';
    readonly outcome: OperationsTelemetryOutcome;
  }): void => {
    const span = safeStartOperationsTelemetrySpan(ports.telemetry, {
      name: 'operations.run.terminal',
      kind: 'INTERNAL',
      correlation: telemetryCorrelationFor({
        investigationId: input.investigationId,
        ...(input.runId === undefined || input.runId === null ? {} : { runId: input.runId }),
      }),
      attributes: { operation: input.operation, outcome: input.outcome, terminal: true },
    });
    span.setStatus(input.outcome);
    span.end();
    safeAddOperationsTelemetryCounter(ports.telemetry, {
      name: 'operations_agent_terminal_outcomes_total',
      labels: { operation: input.operation, outcome: input.outcome },
    });
  };

  const executeRead = async (
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
      attributes: {
        operation: 'READ_OWNER',
        logicalTool: request.tool,
        owner,
      },
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
          safeObserveOperationsTelemetryHistogram(ports.telemetry, {
            name: 'operations_agent_operation_duration_ms',
            value: authorizationDurationMs,
            labels: { operation: 'AUTHORIZE_TOOL', owner: 'platform-gateway' },
          });
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
        if (request.tool === 'registry.getSite' || request.tool === 'registry.listSiteEquipment') {
          result = await ports.ownerReaders.registry.read({ request, context: ownerContext });
        } else if (request.tool === 'telemetry.getCurrentSnapshot') {
          result = await ports.ownerReaders.currentTelemetry.read({ request, context: ownerContext });
        } else if (request.tool === 'analytics.getEnergySeries') {
          result = await ports.ownerReaders.energyAnalytics.read({ request, context: ownerContext });
        } else {
          result = await ports.ownerReaders.commandCapabilities.read({ request, context: ownerContext });
        }
        const validated = validateOwnerResult(request, result, ownerContext.scope);
        const resultCost = runResourceOwnerResultCost(validated);
        ownerSpan.setAttributes({
          outcome: 'SUCCESS',
          ownerRecords: resultCost.ownerRecords,
          payloadBytes: resultCost.payloadBytes,
        });
        ownerSpan.setStatus('SUCCESS');
        toolOutcome = 'SUCCESS';
        return validated;
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


  return {
    async create(command) {
      try {
        await authorize(command.scope, 'CREATE_INVESTIGATION');

        const now = ports.clock.now();
        const investigation = OperationsInvestigation.create({
          id: ports.idGenerator.next('investigation'),
          scope: command.scope,
          createdAt: now,
        });
        const view = investigation.view();
        await ports.investigationTransaction.create({
          investigation,
          event: {
            type: 'INVESTIGATION_CREATED',
            investigationId: view.id,
            revision: view.revision,
            occurredAt: now,
          },
          audit: {
            action: 'CREATE_INVESTIGATION',
            investigationId: view.id,
            runId: null,
            revision: view.revision,
            occurredAt: now,
          },
        });
        return view;
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async start(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'START_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.startRun({
          runId: ports.idGenerator.next('run'),
          runtimeRevision: command.runtimeRevision,
          leaseId: ports.idGenerator.next('lease'),
          leaseAcquiredAt: now,
          leaseExpiresAt: now + leaseDurationMs,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          occurredAt: now,
          eventType: 'AGENT_RUN_STARTED',
          auditAction: 'START_AGENT_RUN',
          runId: next.view().activeRunId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async reopen(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'REOPEN_INVESTIGATION');
        const now = ports.clock.now();
        const next = investigation.reopenCompleted({
          runId: ports.idGenerator.next('run'),
          runtimeRevision: command.runtimeRevision,
          leaseId: ports.idGenerator.next('lease'),
          leaseAcquiredAt: now,
          leaseExpiresAt: now + leaseDurationMs,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          occurredAt: now,
          eventType: 'AGENT_RUN_STARTED',
          auditAction: 'START_AGENT_RUN',
          runId: next.view().activeRunId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async advance(command) {
      try {
        const investigation = await load(command.investigationId);
        const investigationScope = investigation.view().scope;
        const authorization = await authorize(investigationScope, 'ADVANCE_AGENT_RUN');
        const now = ports.clock.now();
        const run = investigation.assertRunAuthority({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
        });
        const checkpoint = await ports.checkpointRepository.load(command.investigationId, run.id);
        if (checkpoint !== null && checkpoint.runtimeRevision !== run.runtimeRevision) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'Runtime Checkpoint revision does not match the active Agent Run.',
          );
        }
        const runtimeContext = createRuntimePlanningContext(investigation.view(), run);
        const modelBudget = await checkRunResourceBudget({
          investigationId: command.investigationId,
          run,
          at: now,
          operationId: `runtime-plan:${checkpoint?.position ?? 'start'}`,
          cost: { ...ZERO_RUN_RESOURCE_BUDGET_COST, modelInvocations: 1 },
        });
        if (modelBudget !== null) {
          return {
            outcome: 'BUDGET_EXHAUSTED',
            investigation: investigation.view(),
            budget: modelBudget,
          };
        }
        const runtimeStartedAt = ports.clock.now();
        const runtimeSpan = safeStartOperationsTelemetrySpan(ports.telemetry, {
          name: 'operations.runtime.plan',
          kind: 'INTERNAL',
          correlation: telemetryCorrelationFor({
            investigationId: command.investigationId,
            runId: run.id,
            ...(authorization.traceparent === undefined
              ? {} : { traceparent: authorization.traceparent }),
            ...(authorization.tracestate === undefined
              ? {} : { tracestate: authorization.tracestate }),
          }),
          attributes: {
            operation: 'PLAN_READS',
            restarted: checkpoint !== null,
            retryCount: checkpoint === null ? 0 : 1,
          },
        });
        if (checkpoint !== null) {
          safeAddOperationsTelemetryCounter(ports.telemetry, {
            name: 'operations_agent_retries_total',
            labels: { operation: 'PLAN_READS', outcome: 'SUCCESS' },
          });
        }
        let runtimePlanning: RuntimePlanningResult;
        try {
          runtimePlanning = await ports.agentExecutionRuntime.planReads({
            context: runtimeContext,
            checkpoint,
          });
          const planning = validateRuntimePlanningResult(runtimePlanning);
          if (planning.status === 'UNABLE_TO_CONCLUDE') {
            runtimeSpan.setStatus('UNABLE_TO_CONCLUDE');
            throw new InvestigationCoordinatorError(
              'UNABLE_TO_CONCLUDE',
              'The Runtime has no remaining authorized bounded READ Step.',
            );
          }
          runtimeSpan.setStatus('SUCCESS');
        } catch (cause) {
          if (cause instanceof InvestigationCoordinatorError) throw cause;
          runtimeSpan.setStatus('INVALID');
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'The Agent execution Runtime rejected the active Run or Checkpoint.',
            { cause },
          );
        } finally {
          const runtimeDurationMs = Math.max(0, ports.clock.now() - runtimeStartedAt);
          runtimeSpan.setAttributes({ durationMs: runtimeDurationMs });
          runtimeSpan.end();
          safeObserveOperationsTelemetryHistogram(ports.telemetry, {
            name: 'operations_agent_operation_duration_ms',
            value: runtimeDurationMs,
            labels: { operation: 'PLAN_READS' },
          });
        }
        const planning = validateRuntimePlanningResult(runtimePlanning);
        if (planning.status !== 'PLANNED') {
          throw new InvestigationCoordinatorError(
            'UNABLE_TO_CONCLUDE',
            'The Runtime has no remaining authorized bounded READ Step.',
          );
        }

        countValidatedReads(planning.plan, runtimeContext);
        const ownerReadContext: OwnerReadContext = {
          investigationId: command.investigationId,
          runId: run.id,
          scope: investigationScope,
          authorization,
          correlationId: `${command.investigationId}:${run.id}`,
        };
        const results: OwnerReadResult[] = [];
        for (const batch of planning.plan.batches) {
          const stepStartedAt = ports.clock.now();
          const stepSpan = safeStartOperationsTelemetrySpan(ports.telemetry, {
            name: 'operations.runtime.step',
            kind: 'INTERNAL',
            correlation: telemetryCorrelationFor({
              investigationId: command.investigationId,
              runId: run.id,
              stepId: batch.batchId,
              ...(authorization.traceparent === undefined
                ? {} : { traceparent: authorization.traceparent }),
              ...(authorization.tracestate === undefined
                ? {} : { tracestate: authorization.tracestate }),
            }),
            attributes: { operation: 'EXECUTE_STEP' },
          });
          let stepOutcome: OperationsTelemetryOutcome = 'ERROR';
          try {
            const readBudget = await checkRunResourceBudget({
              investigationId: command.investigationId,
              run,
              at: ports.clock.now(),
              operationId: runResourceReadBatchOperationId(batch.requests),
              cost: runResourceReadBatchCost(batch.requests),
            });
            if (readBudget !== null) {
              stepOutcome = 'EXHAUSTED';
              return {
                outcome: 'BUDGET_EXHAUSTED',
                investigation: investigation.view(),
                budget: readBudget,
              };
            }
            const stepContext: OwnerReadContext = {
              ...ownerReadContext,
              stepId: batch.batchId,
              authorization: {
                ...ownerReadContext.authorization,
                ...(stepSpan.traceparent === undefined
                  ? {} : { traceparent: stepSpan.traceparent }),
                ...(stepSpan.tracestate === undefined
                  ? {} : { tracestate: stepSpan.tracestate }),
              },
            };
            const batchResults = await Promise.all(batch.requests.map((request) => (
              executeRead(request, stepContext)
            )));
            const resultBudget = await checkRunResourceBudget({
              investigationId: command.investigationId,
              run,
              at: ports.clock.now(),
              operationId: runResourceOwnerResultBatchOperationId(batch.requests),
              cost: runResourceOwnerResultBatchCost(batchResults),
            });
            if (resultBudget !== null) {
              stepOutcome = 'EXHAUSTED';
              return {
                outcome: 'BUDGET_EXHAUSTED',
                investigation: investigation.view(),
                budget: resultBudget,
              };
            }
            results.push(...batchResults);
            stepOutcome = 'SUCCESS';
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
        }

        const checkpointId = ports.idGenerator.next('checkpoint');
        const checkpointSavedAt = checkpoint === null
          ? now
          : Math.max(now, checkpoint.savedAt + 1);
        if (!Number.isSafeInteger(checkpointSavedAt)) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'Runtime Checkpoint time is outside the safe integer range.',
          );
        }
        await ports.checkpointRepository.save({
          id: checkpointId,
          investigationId: command.investigationId,
          runId: run.id,
          runtimeRevision: run.runtimeRevision,
          position: planning.checkpoint.position,
          opaqueState: planning.checkpoint.opaqueState,
          savedAt: checkpointSavedAt,
        });

        const view = investigation.view();
        await recordMutation(
          {
            type: 'READ_PLAN_COMPLETED',
            investigationId: view.id,
            revision: view.revision,
            occurredAt: now,
          },
          {
            action: 'PLAN_READS',
            investigationId: view.id,
            runId: run.id,
            revision: view.revision,
            occurredAt: now,
          },
        );
        return {
          outcome: 'READ_PLAN_COMPLETED',
          investigation: view,
          plan: planning.plan,
          results,
          checkpointId,
        };
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async commitEffect(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'COMMIT_EFFECT');
        const record = command.record === undefined
          ? undefined
          : createInvestigationBusinessRecord(command.record);
        const now = ports.clock.now();
        if (record?.recordType === 'OPERATOR_INPUT_ACCEPTED') {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'Accepted Operator Input cannot be committed as an Agent effect.',
          );
        }
        if (record !== undefined
          && (record.investigationId !== command.investigationId
            || record.id !== command.recordId
            || record.recordType !== command.kind
            || record.recordedAt > now)) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'Business record identity, type, and time must match the committed effect.',
          );
        }
        if (record?.recordType === 'TOOL_EXECUTION_RECEIPT'
          && (record.runId !== command.runId || record.stepId !== command.stepId)) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'Tool Execution Receipt Run and Step must match the committing command.',
          );
        }
        const result = investigation.commitEffect({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
          stepId: createStepIdentity(command.stepId),
          idempotencyKey: createIdempotencyKey(command.idempotencyKey),
          kind: command.kind,
          recordId: command.recordId,
        });
        if (result.outcome === 'COMMITTED') {
          const run = investigation.view().runs.find(({ id }) => id === command.runId);
          if (run === undefined) {
            throw new InvestigationCoordinatorError(
              'INVALID_INVESTIGATION_STATE',
              'The committing Agent Run is missing from the Investigation.',
            );
          }
          const budget = await checkRunResourceBudget({
            investigationId: command.investigationId,
            run,
            at: now,
            operationId: runResourceEffectOperationId(command.idempotencyKey),
            cost: ZERO_RUN_RESOURCE_BUDGET_COST,
          });
          if (budget !== null) {
            throw new InvestigationCoordinatorError(
              'BUDGET_EXHAUSTED',
              `Agent Run resource budget exhausted: ${budget.exhaustedDimension}.`,
            );
          }
        }
        if (command.kind !== 'PROPOSED_ACTION' && record === undefined) {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            `${command.kind} effects require a typed business record.`,
          );
        }
        const view = result.outcome === 'COMMITTED'
          ? await persistMutation({
            investigation: result.investigation,
            expectedRevision: command.expectedRevision,
            expectedAuthority: {
              runId: command.runId,
              leaseId: command.leaseId,
              at: now,
            },
            effect: result.effect,
            ...(record === undefined ? {} : { record }),
            occurredAt: now,
            eventType: 'INVESTIGATION_EFFECT_COMMITTED',
            auditAction: 'COMMIT_EFFECT',
            runId: command.runId,
          })
          : result.investigation.view();
        let persistedRecord = record;
        if (result.outcome === 'DUPLICATE' && record !== undefined) {
          const existing = await ports.businessRecordRepository.get(
            command.investigationId,
            record.id,
          );
          if (existing === null || existing.recordType === 'OPERATOR_INPUT_ACCEPTED') {
            throw new InvestigationCoordinatorError(
              'INVALID_INVESTIGATION_STATE',
              'Committed effect is missing its typed business record.',
            );
          }
          if (!businessRecordsEqual(existing, record)) {
            throw new InvestigationCoordinatorError(
              'DUPLICATE_RECORD',
              `Business record ${record.id} is already committed with different content.`,
            );
          }
          persistedRecord = existing;
        }
        return {
          outcome: result.outcome,
          investigation: view,
          effect: result.effect,
          ...(persistedRecord === undefined ? {} : { record: persistedRecord }),
        };
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async pause(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'PAUSE_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.pauseRun({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          expectedAuthority: {
            runId: command.runId,
            leaseId: command.leaseId,
            at: now,
          },
          occurredAt: now,
          eventType: 'AGENT_RUN_PAUSED',
          auditAction: 'PAUSE_AGENT_RUN',
          runId: command.runId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async resume(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'RESUME_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.resumeRun({
          runId: command.runId,
          leaseId: ports.idGenerator.next('lease'),
          leaseAcquiredAt: now,
          leaseExpiresAt: now + leaseDurationMs,
          expectedRevision: command.expectedRevision,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          occurredAt: now,
          eventType: 'AGENT_RUN_RESUMED',
          auditAction: 'RESUME_AGENT_RUN',
          runId: command.runId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async requestOperatorInput(command) {
      try {
        const investigation = await loadAuthorized(
          command.investigationId,
          'REQUEST_OPERATOR_INPUT',
        );
        const now = ports.clock.now();
        const next = investigation.requestOperatorInput({
          requestId: ports.idGenerator.next('operator-input-request'),
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
          kind: command.kind,
        });
        return await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          expectedAuthority: {
            runId: command.runId,
            leaseId: command.leaseId,
            at: now,
          },
          occurredAt: now,
          eventType: 'OPERATOR_INPUT_REQUESTED',
          auditAction: 'REQUEST_OPERATOR_INPUT',
          runId: command.runId,
        });
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async acceptOperatorInput(command) {
      try {
        const investigation = await load(command.investigationId);
        const current = investigation.view();
        const authorization = await authorize(current.scope, 'ACCEPT_OPERATOR_INPUT');
        if (authorization.decisionId.trim().length === 0
          || authorization.policyRevision === undefined
          || authorization.policyRevision.trim().length === 0) {
          throw new InvestigationCoordinatorError(
            'AUTHORIZATION_DENIED',
            'Operator Input authorization provenance is incomplete.',
          );
        }
        const request = current.activeOperatorInputRequest;
        if (request === null && !current.operatorInputAcceptances.some((acceptance) => (
          acceptance.requestId === command.requestId
        ))) {
          throw new InvestigationCoordinatorError(
            'OPERATOR_INPUT_CONFLICT',
            'The Investigation is not waiting for this Operator Input Request.',
          );
        }
        const values = normalizeOperatorInputValues(command.values);
        const inputDigest = operatorInputDigest(command.requestId, values);
        const idempotencyKey = createIdempotencyKey(command.idempotencyKey);
        const duplicateAcceptance = current.operatorInputAcceptances.find((acceptance) => (
          acceptance.idempotencyKey === idempotencyKey
        ));
        const duplicateRun = duplicateAcceptance === undefined
          ? undefined
          : current.runs.find(({ id }) => id === duplicateAcceptance.runId);
        const now = ports.clock.now();
        const result = investigation.acceptOperatorInput({
          requestId: command.requestId,
          runId: request?.runId
            ?? current.operatorInputAcceptances.find(({ requestId }) => (
              requestId === command.requestId
            ))?.runId
            ?? '',
          expectedRevision: command.expectedRevision,
          idempotencyKey,
          recordId: duplicateAcceptance?.recordId
            ?? ports.idGenerator.next('operator-input-record'),
          inputDigest,
          acceptedAt: now,
          leaseId: duplicateRun?.lease?.id
            ?? duplicateRun?.leaseHistory.at(-1)?.id
            ?? ports.idGenerator.next('lease'),
          leaseExpiresAt: now + leaseDurationMs,
        });
        if (result.outcome === 'DUPLICATE') {
          const existing = await ports.businessRecordRepository.get(
            command.investigationId,
            result.acceptance.recordId,
          );
          if (existing?.recordType !== 'OPERATOR_INPUT_ACCEPTED'
            || !operatorInputRecordMatches(
              existing,
              command.requestId,
              command.idempotencyKey,
              inputDigest,
              values,
            )) {
            throw new InvestigationCoordinatorError(
              'DUPLICATE_RECORD',
              'Accepted Operator Input retry does not match the committed record.',
            );
          }
          return {
            outcome: 'DUPLICATE',
            investigation: result.investigation.view(),
            record: existing,
          };
        }
        const record = createInvestigationBusinessRecord({
          schemaVersion: 1,
          recordType: 'OPERATOR_INPUT_ACCEPTED',
          id: result.acceptance.recordId,
          investigationId: command.investigationId,
          recordedAt: result.acceptance.acceptedAt,
          requestId: result.acceptance.requestId,
          runId: result.acceptance.runId,
          idempotencyKey: result.acceptance.idempotencyKey,
          inputKind: result.acceptance.kind,
          inputDigest: result.acceptance.inputDigest,
          scope: current.scope,
          values,
          provenance: {
            actorType: 'OPERATOR',
            source: 'PLATFORM_GATEWAY',
            authorizationDecisionId: authorization.decisionId,
            policyRevision: authorization.policyRevision,
            submittedAt: result.acceptance.acceptedAt,
          },
        });
        if (record.recordType !== 'OPERATOR_INPUT_ACCEPTED') {
          throw new InvestigationCoordinatorError(
            'INVALID_INVESTIGATION_STATE',
            'Accepted Operator Input record normalization failed.',
          );
        }
        const view = await persistMutation({
          investigation: result.investigation,
          expectedRevision: command.expectedRevision,
          record,
          occurredAt: result.acceptance.acceptedAt,
          eventType: 'OPERATOR_INPUT_ACCEPTED',
          auditAction: 'ACCEPT_OPERATOR_INPUT',
          runId: result.acceptance.runId,
        });
        return {
          outcome: 'COMMITTED',
          investigation: view,
          record,
        };
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async cancel(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'CANCEL_INVESTIGATION');
        const activeRunId = investigation.view().activeRunId;
        const now = ports.clock.now();
        const next = investigation.cancel({
          at: now,
          expectedRevision: command.expectedRevision,
        });
        const view = await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          occurredAt: now,
          eventType: 'INVESTIGATION_CANCELLED',
          auditAction: 'CANCEL_INVESTIGATION',
          runId: activeRunId,
        });
        recordTerminalOutcome({
          investigationId: command.investigationId,
          runId: activeRunId,
          operation: 'CANCEL',
          outcome: 'CANCELLED',
        });
        return view;
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async complete(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'COMPLETE_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.completeRun({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
        });
        const view = await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          expectedAuthority: {
            runId: command.runId,
            leaseId: command.leaseId,
            at: now,
          },
          occurredAt: now,
          eventType: 'AGENT_RUN_COMPLETED',
          auditAction: 'COMPLETE_AGENT_RUN',
          runId: command.runId,
        });
        recordTerminalOutcome({
          investigationId: command.investigationId,
          runId: command.runId,
          operation: 'COMPLETE_RUN',
          outcome: 'SUCCESS',
        });
        return view;
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async fail(command) {
      try {
        const investigation = await loadAuthorized(command.investigationId, 'FAIL_AGENT_RUN');
        const now = ports.clock.now();
        const next = investigation.failRun({
          runId: command.runId,
          leaseId: command.leaseId,
          at: now,
          expectedRevision: command.expectedRevision,
        });
        const view = await persistMutation({
          investigation: next,
          expectedRevision: command.expectedRevision,
          expectedAuthority: {
            runId: command.runId,
            leaseId: command.leaseId,
            at: now,
          },
          occurredAt: now,
          eventType: 'AGENT_RUN_FAILED',
          auditAction: 'FAIL_AGENT_RUN',
          runId: command.runId,
        });
        recordTerminalOutcome({
          investigationId: command.investigationId,
          runId: command.runId,
          operation: 'FAIL_RUN',
          outcome: 'ERROR',
        });
        return view;
      } catch (error) {
        return mapApplicationError(error);
      }
    },

    async get(query) {
      try {
        return (await loadAuthorized(query.investigationId, 'READ_INVESTIGATION')).view();
      } catch (error) {
        return mapApplicationError(error);
      }
    },
  };
};

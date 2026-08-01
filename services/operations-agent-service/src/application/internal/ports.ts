import type {
  CommittedEffectView,
  InvestigationRevision,
  InvestigationScope,
  InvestigationBusinessRecord,
  LogicalTool,
  OperationsInvestigation,
} from '../../domain/index.js';
import { OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY } from './generated-runtime-control-contract.js';

export type InvestigationRepositoryConflictCode =
  | 'IDENTITY_CONFLICT'
  | 'REVISION_CONFLICT'
  | 'LEASE_CONFLICT'
  | 'DUPLICATE_EFFECT'
  | 'DUPLICATE_RECORD'
  | 'RECORD_REFERENCE_CONFLICT';

export class InvestigationRepositoryConflictError extends Error {
  readonly code: InvestigationRepositoryConflictCode;

  constructor(code: InvestigationRepositoryConflictCode, message: string) {
    super(message);
    this.name = 'InvestigationRepositoryConflictError';
    this.code = code;
  }
}

export interface InvestigationRepository {
  get(investigationId: string): Promise<OperationsInvestigation | null>;
  listByScope?(input: {
    readonly organizationId: string;
    readonly siteId: string;
    readonly limit: number;
  }): Promise<readonly OperationsInvestigation[]>;
}

export interface InvestigationBusinessRecordRepository {
  get(
    investigationId: string,
    recordId: string,
  ): Promise<InvestigationBusinessRecord | null>;
}

export interface AuthorizationDecision {
  readonly decision: 'ALLOW' | 'DENY';
  readonly decisionId: string;
  readonly reason?: string;
  readonly delegationGrant?: string;
  readonly toolDelegationGrants?: Readonly<Partial<Record<ParallelReadRequest['tool'], string>>>;
  readonly policyRevision?: string;
  readonly traceparent?: string;
}

export type InvestigationAuthorizationAction =
  | 'CREATE_INVESTIGATION'
  | 'LIST_INVESTIGATIONS'
  | 'READ_INVESTIGATION'
  | 'START_AGENT_RUN'
  | 'REOPEN_INVESTIGATION'
  | 'ADVANCE_AGENT_RUN'
  | 'COMMIT_EFFECT'
  | 'PAUSE_AGENT_RUN'
  | 'RESUME_AGENT_RUN'
  | 'REQUEST_OPERATOR_INPUT'
  | 'ACCEPT_OPERATOR_INPUT'
  | 'CANCEL_INVESTIGATION'
  | 'COMPLETE_AGENT_RUN'
  | 'FAIL_AGENT_RUN';

export interface AuthorizationDecisionReader {
  authorizeScope(input: {
    readonly scope: InvestigationScope;
    readonly action: InvestigationAuthorizationAction;
  }): Promise<AuthorizationDecision>;
}

export type RegistryReadRequest =
  | {
    readonly requestId: string;
    readonly tool: 'registry.getSite';
    readonly input: {
      readonly siteId: string;
    };
  }
  | {
    readonly requestId: string;
    readonly tool: 'registry.listSiteEquipment';
    readonly input: {
      readonly siteId: string;
    };
  };

export interface CurrentTelemetryReadRequest {
  readonly requestId: string;
  readonly tool: 'telemetry.getCurrentSnapshot';
  readonly input: {
    readonly equipmentId: string;
    readonly pointKeys?: readonly string[];
  };
}

export interface EnergyAnalyticsReadRequest {
  readonly requestId: string;
  readonly tool: 'analytics.getEnergySeries';
  readonly input: {
    readonly organizationId: string;
    readonly siteId: string;
    readonly energyType: 'electricity';
    readonly granularity: 'hour' | 'day' | 'month';
    readonly timezone: string;
    readonly from: string;
    readonly to: string;
    readonly qualityPolicy: 'VALID_ONLY' | 'VALID_AND_SUSPECT';
  };
}

export interface CommandCapabilityReadRequest {
  readonly requestId: string;
  readonly tool: 'commands.getCapabilities';
  readonly input: {
    readonly equipmentId: string;
  };
}

export type ParallelReadRequest =
  | RegistryReadRequest
  | CurrentTelemetryReadRequest
  | EnergyAnalyticsReadRequest
  | CommandCapabilityReadRequest;

export interface ParallelReadBatch {
  readonly batchId: string;
  readonly requests: readonly ParallelReadRequest[];
}

export interface RuntimeReadPlan {
  readonly batches: readonly ParallelReadBatch[];
}

export interface RuntimeCheckpointDraft {
  readonly position: string;
  readonly opaqueState: string;
}

export interface RuntimePlanningContext {
  readonly schemaVersion: typeof OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.schemaVersion;
  readonly source: typeof OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.source;
  readonly trust: typeof OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.trust;
  readonly investigationId: string;
  readonly scope: InvestigationScope;
  readonly revision: InvestigationRevision;
  readonly runId: string;
  readonly runStatus: typeof OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.runStatus;
  readonly runtimeRevision: string;
  readonly allowedReadTools: readonly LogicalTool[];
  readonly effectPolicy: typeof OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.effectPolicy;
  readonly scopePolicy: typeof OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.scopePolicy;
  readonly untrustedContentPolicy: typeof OPERATIONS_AGENT_TRUSTED_RUNTIME_CONTROL_POLICY.untrustedContentPolicy;
}

export type RuntimePlanningResult =
  | {
    readonly status: 'PLANNED';
    readonly plan: RuntimeReadPlan;
    readonly checkpoint: RuntimeCheckpointDraft;
  }
  | {
    readonly status: 'UNABLE_TO_CONCLUDE';
    readonly reasonCode: 'NO_REMAINING_READ_STEP';
  };

export interface AgentExecutionRuntime {
  planReads(input: {
    readonly context: RuntimePlanningContext;
    readonly checkpoint: RuntimeCheckpoint | null;
  }): Promise<RuntimePlanningResult>;
}

export interface RuntimeCheckpoint {
  readonly id: string;
  readonly investigationId: string;
  readonly runId: string;
  readonly runtimeRevision: string;
  readonly position: string;
  readonly opaqueState: string;
  readonly savedAt: number;
}

export interface CheckpointRepository {
  save(checkpoint: RuntimeCheckpoint): Promise<void>;
  load(investigationId: string, runId: string): Promise<RuntimeCheckpoint | null>;
  delete(investigationId: string, runId: string): Promise<void>;
}

export interface ApplicationEvent {
  readonly type:
    | 'INVESTIGATION_CREATED'
    | 'AGENT_RUN_STARTED'
    | 'READ_PLAN_COMPLETED'
    | 'INVESTIGATION_EFFECT_COMMITTED'
    | 'AGENT_RUN_PAUSED'
    | 'AGENT_RUN_RESUMED'
    | 'OPERATOR_INPUT_REQUESTED'
    | 'OPERATOR_INPUT_ACCEPTED'
    | 'INVESTIGATION_CANCELLED'
    | 'AGENT_RUN_COMPLETED'
    | 'AGENT_RUN_FAILED';
  readonly investigationId: string;
  readonly revision: InvestigationRevision;
  readonly occurredAt: number;
}

export interface ApplicationOutbox {
  append(event: ApplicationEvent): Promise<void>;
}

export interface AuditRecord {
  readonly action:
    | 'CREATE_INVESTIGATION'
    | 'START_AGENT_RUN'
    | 'PLAN_READS'
    | 'COMMIT_EFFECT'
    | 'PAUSE_AGENT_RUN'
    | 'RESUME_AGENT_RUN'
    | 'REQUEST_OPERATOR_INPUT'
    | 'ACCEPT_OPERATOR_INPUT'
    | 'CANCEL_INVESTIGATION'
    | 'COMPLETE_AGENT_RUN'
    | 'FAIL_AGENT_RUN';
  readonly investigationId: string;
  readonly runId: string | null;
  readonly revision: InvestigationRevision;
  readonly occurredAt: number;
}

export interface AuditRecorder {
  record(record: AuditRecord): Promise<void>;
}

export interface InvestigationWriteAuthority {
  readonly runId: string;
  readonly leaseId: string;
  readonly at: number;
}

export interface InvestigationTransaction {
  create(input: {
    readonly investigation: OperationsInvestigation;
    readonly event: ApplicationEvent;
    readonly audit: AuditRecord;
  }): Promise<void>;
  save(input: {
    readonly investigation: OperationsInvestigation;
    readonly expectedRevision: InvestigationRevision;
    readonly expectedAuthority?: InvestigationWriteAuthority;
    readonly effect?: CommittedEffectView;
    readonly record?: InvestigationBusinessRecord;
    readonly event: ApplicationEvent;
    readonly audit: AuditRecord;
  }): Promise<void>;
}

export type RunResourceBudgetDimension =
  | 'MODEL_INVOCATIONS'
  | 'TOOL_REQUESTS'
  | 'WALL_CLOCK_MS'
  | 'QUERY_RANGE_MS'
  | 'QUERY_BUCKETS'
  | 'OWNER_RECORDS'
  | 'PAYLOAD_BYTES';

export interface RunResourceBudgetLimits {
  readonly modelInvocations: number;
  readonly toolRequests: number;
  readonly wallClockMs: number;
  readonly queryRangeMs: number;
  readonly queryBuckets: number;
  readonly ownerRecords: number;
  readonly payloadBytes: number;
}

export interface RunResourceBudgetPolicy {
  readonly schemaVersion: 1;
  readonly revision: string;
  readonly limits: RunResourceBudgetLimits;
}

export interface RunResourceBudgetUsage {
  readonly modelInvocations: number;
  readonly toolRequests: number;
  readonly maximumQueryRangeMs: number;
  readonly queryBuckets: number;
  readonly ownerRecords: number;
  readonly payloadBytes: number;
}

export interface RunResourceBudgetExhaustion {
  readonly dimension: RunResourceBudgetDimension;
  readonly at: number;
  readonly consumed: number;
  readonly limit: number;
  readonly outcome: 'PARTIAL' | 'UNABLE_TO_CONCLUDE';
}

export interface RunResourceBudgetSnapshot {
  readonly schemaVersion: 1;
  readonly investigationId: string;
  readonly runId: string;
  readonly policyRevision: string;
  readonly startedAt: number;
  readonly usage: RunResourceBudgetUsage;
  readonly exhaustion: RunResourceBudgetExhaustion | null;
}

export interface RunResourceBudgetCost {
  readonly modelInvocations: number;
  readonly toolRequests: number;
  readonly queryRangeMs: number;
  readonly queryBuckets: number;
  readonly ownerRecords: number;
  readonly payloadBytes: number;
}

export interface RunResourceBudgetOutcome {
  readonly schemaVersion: 1;
  readonly policyRevision: string;
  readonly outcome: 'PARTIAL' | 'UNABLE_TO_CONCLUDE';
  readonly exhaustedDimension: RunResourceBudgetDimension;
  readonly consumed: number;
  readonly limit: number;
}

export interface BudgetDecision {
  readonly decision: 'ALLOW' | 'DENY';
  readonly duplicate: boolean;
  readonly snapshot: RunResourceBudgetSnapshot;
}

export interface BudgetGuard {
  check(input: {
    readonly investigationId: string;
    readonly runId: string;
    readonly startedAt: number;
    readonly at: number;
    readonly operationId: string;
    readonly policy: RunResourceBudgetPolicy;
    readonly cost: RunResourceBudgetCost;
  }): Promise<BudgetDecision>;
  get(investigationId: string, runId: string): Promise<RunResourceBudgetSnapshot | null>;
}

export interface OwnerReadResult {
  readonly requestId: string;
  readonly owner: 'registry' | 'telemetry-query-service' | 'command-service';
  readonly scope: InvestigationScope;
  readonly revision: string;
  readonly quality: 'GOOD' | 'UNCERTAIN' | 'BAD' | 'STALE';
  readonly provenance: string;
  readonly payload: unknown;
}

export type OwnerReadErrorCode =
  | 'OWNER_REQUEST_INVALID'
  | 'OWNER_RESOURCE_NOT_FOUND'
  | 'OWNER_READ_TIMEOUT'
  | 'OWNER_READ_UNAVAILABLE'
  | 'OWNER_RESPONSE_TOO_LARGE'
  | 'OWNER_RESPONSE_INVALID';

export class OwnerReadError extends Error {
  readonly code: OwnerReadErrorCode;

  constructor(code: OwnerReadErrorCode, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'OwnerReadError';
    this.code = code;
  }
}

export interface OwnerReadContext {
  readonly investigationId: string;
  readonly runId: string;
  readonly scope: InvestigationScope;
  readonly authorization: AuthorizationDecision;
  readonly correlationId: string;
}

export interface OwnerReadInput<TRequest extends ParallelReadRequest> {
  readonly request: TRequest;
  readonly context: OwnerReadContext;
}

export interface ToolAuthorizationGrant {
  readonly delegationGrant: string;
  readonly policyRevision?: string;
}

export interface ToolAuthorizationReader {
  authorize(input: OwnerReadInput<ParallelReadRequest>): Promise<ToolAuthorizationGrant>;
}

export interface RegistryReader {
  read(input: OwnerReadInput<RegistryReadRequest>): Promise<OwnerReadResult>;
}

export interface CurrentTelemetryReader {
  read(input: OwnerReadInput<CurrentTelemetryReadRequest>): Promise<OwnerReadResult>;
}

export interface EnergyAnalyticsReader {
  read(input: OwnerReadInput<EnergyAnalyticsReadRequest>): Promise<OwnerReadResult>;
}

export interface CommandCapabilityReader {
  read(input: OwnerReadInput<CommandCapabilityReadRequest>): Promise<OwnerReadResult>;
}

export interface OwnerReaders {
  readonly registry: RegistryReader;
  readonly currentTelemetry: CurrentTelemetryReader;
  readonly energyAnalytics: EnergyAnalyticsReader;
  readonly commandCapabilities: CommandCapabilityReader;
}

export interface Clock {
  now(): number;
}

export type GeneratedIdentityKind =
  | 'investigation'
  | 'run'
  | 'lease'
  | 'checkpoint'
  | 'operator-input-request'
  | 'operator-input-record';

export interface IdGenerator {
  next(kind: GeneratedIdentityKind): string;
}

export interface InvestigationCoordinatorPorts {
  readonly investigationRepository: InvestigationRepository;
  readonly businessRecordRepository: InvestigationBusinessRecordRepository;
  readonly investigationTransaction: InvestigationTransaction;
  readonly authorizationDecisionReader: AuthorizationDecisionReader;
  readonly agentExecutionRuntime: AgentExecutionRuntime;
  readonly checkpointRepository: CheckpointRepository;
  readonly applicationOutbox: ApplicationOutbox;
  readonly auditRecorder: AuditRecorder;
  readonly budgetGuard: BudgetGuard;
  readonly resourceBudgetPolicy?: RunResourceBudgetPolicy;
  readonly toolAuthorizationReader?: ToolAuthorizationReader;
  readonly ownerReaders: OwnerReaders;
  readonly clock: Clock;
  readonly idGenerator: IdGenerator;
  readonly leaseDurationMs: number;
}

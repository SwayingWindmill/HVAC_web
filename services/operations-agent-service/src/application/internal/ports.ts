import type {
  CommittedEffectView,
  InvestigationRevision,
  InvestigationScope,
  InvestigationBusinessRecord,
  OperationsInvestigation,
  OperationsInvestigationView,
} from '../../domain/index.js';

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
  readonly policyRevision?: string;
  readonly traceparent?: string;
}

export type InvestigationAuthorizationAction =
  | 'CREATE_INVESTIGATION'
  | 'READ_INVESTIGATION'
  | 'START_AGENT_RUN'
  | 'REOPEN_INVESTIGATION'
  | 'ADVANCE_AGENT_RUN'
  | 'COMMIT_EFFECT'
  | 'PAUSE_AGENT_RUN'
  | 'RESUME_AGENT_RUN'
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

export type RuntimePlanningResult =
  | {
    readonly status: 'PLANNED';
    readonly plan: RuntimeReadPlan;
    readonly checkpoint: RuntimeCheckpointDraft;
  }
  | {
    readonly status: 'UNABLE_TO_CONCLUDE';
    readonly reason: string;
  };

export interface AgentExecutionRuntime {
  planReads(input: {
    readonly investigation: OperationsInvestigationView;
    readonly runId: string;
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

export interface BudgetDecision {
  readonly decision: 'ALLOW' | 'DENY';
  readonly reason?: string;
}

export interface BudgetGuard {
  check(input: {
    readonly investigationId: string;
    readonly runId: string;
    readonly plannedReadCount: number;
  }): Promise<BudgetDecision>;
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

export type GeneratedIdentityKind = 'investigation' | 'run' | 'lease' | 'checkpoint';

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
  readonly ownerReaders: OwnerReaders;
  readonly clock: Clock;
  readonly idGenerator: IdGenerator;
  readonly leaseDurationMs: number;
}

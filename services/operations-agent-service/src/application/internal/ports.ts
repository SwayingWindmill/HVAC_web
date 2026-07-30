import type {
  InvestigationRevision,
  InvestigationScope,
  OperationsInvestigation,
  OperationsInvestigationView,
} from '../../domain/index.js';

export class InvestigationRepositoryConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvestigationRepositoryConflictError';
  }
}

export interface InvestigationRepository {
  get(investigationId: string): Promise<OperationsInvestigation | null>;
}

export interface AuthorizationDecision {
  readonly decision: 'ALLOW' | 'DENY';
  readonly decisionId: string;
  readonly reason?: string;
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

export interface RegistryReadRequest {
  readonly requestId: string;
  readonly tool: 'registry.getEquipment';
  readonly input: {
    readonly equipmentId: string;
  };
}

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
    readonly siteId: string;
    readonly rangeStart: string;
    readonly rangeEnd: string;
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

export interface InvestigationTransaction {
  create(input: {
    readonly investigation: OperationsInvestigation;
    readonly event: ApplicationEvent;
    readonly audit: AuditRecord;
  }): Promise<void>;
  save(input: {
    readonly investigation: OperationsInvestigation;
    readonly expectedRevision: InvestigationRevision;
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
  readonly owner: 'registry' | 'telemetry-query-service' | 'analytics-service' | 'command-service';
  readonly scope: InvestigationScope;
  readonly revision: string;
  readonly quality: 'GOOD' | 'UNCERTAIN' | 'BAD' | 'STALE';
  readonly provenance: string;
  readonly payload: unknown;
}

export interface RegistryReader {
  read(request: RegistryReadRequest): Promise<OwnerReadResult>;
}

export interface CurrentTelemetryReader {
  read(request: CurrentTelemetryReadRequest): Promise<OwnerReadResult>;
}

export interface EnergyAnalyticsReader {
  read(request: EnergyAnalyticsReadRequest): Promise<OwnerReadResult>;
}

export interface CommandCapabilityReader {
  read(request: CommandCapabilityReadRequest): Promise<OwnerReadResult>;
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

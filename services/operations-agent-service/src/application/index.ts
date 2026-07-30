import { domainModule } from '../domain/index.js';

export {
  InvestigationCoordinatorError,
  createInvestigationCoordinator,
  type AdvanceInvestigationCommand,
  type AdvanceInvestigationResult,
  type CancelInvestigationCommand,
  type CommitInvestigationEffectCommand,
  type CommitInvestigationEffectResult,
  type CreateInvestigationCommand,
  type GetInvestigationQuery,
  type InvestigationCoordinator,
  type InvestigationCoordinatorErrorCode,
  type ReopenInvestigationCommand,
  type ResumeInvestigationCommand,
  type RunLeaseMutationCommand,
  type StartInvestigationCommand,
} from './internal/investigation-coordinator.js';

export {
  InvestigationRepositoryConflictError,
  type AgentExecutionRuntime,
  type ApplicationEvent,
  type ApplicationOutbox,
  type AuditRecord,
  type AuditRecorder,
  type AuthorizationDecision,
  type AuthorizationDecisionReader,
  type BudgetDecision,
  type BudgetGuard,
  type CheckpointRepository,
  type Clock,
  type CommandCapabilityReadRequest,
  type CommandCapabilityReader,
  type CurrentTelemetryReadRequest,
  type CurrentTelemetryReader,
  type EnergyAnalyticsReadRequest,
  type EnergyAnalyticsReader,
  type GeneratedIdentityKind,
  type IdGenerator,
  type InvestigationAuthorizationAction,
  type InvestigationCoordinatorPorts,
  type InvestigationRepository,
  type InvestigationTransaction,
  type OwnerReadResult,
  type OwnerReaders,
  type ParallelReadBatch,
  type ParallelReadRequest,
  type RegistryReadRequest,
  type RegistryReader,
  type RuntimeCheckpoint,
  type RuntimeCheckpointDraft,
  type RuntimePlanningResult,
  type RuntimeReadPlan,
} from './internal/ports.js';

export const applicationModule = Object.freeze({
  name: 'application',
  layer: 'application',
  dependencies: [domainModule.name],
} as const);

export type ApplicationModule = typeof applicationModule;

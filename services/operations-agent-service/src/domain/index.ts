export {
  OperationsInvestigation,
  OperationsInvestigationError,
  createIdempotencyKey,
  createInvestigationRevision,
  createStepIdentity,
  type AgentRunLeaseView,
  type AgentRunStatus,
  type AgentRunView,
  type CancelInvestigation,
  type CommitEffectResult,
  type CommitInvestigationEffect,
  type CommittedEffectKind,
  type CommittedEffectView,
  type CreateOperationsInvestigation,
  type EndAgentRun,
  type IdempotencyKey,
  type InvestigationRevision,
  type InvestigationScope,
  type InvestigationStatus,
  type OperationsInvestigationErrorCode,
  type OperationsInvestigationView,
  type PauseAgentRun,
  type ReopenCompletedInvestigation,
  type ResumeAgentRun,
  type StartAgentRun,
  type StepIdentity,
} from './internal/operations-investigation.js';

export const domainModule = Object.freeze({
  name: 'domain',
  layer: 'domain',
  dependencies: [],
} as const);

export type DomainModule = typeof domainModule;

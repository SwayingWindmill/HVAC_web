import type { InvestigationRevision, InvestigationScope } from '../../domain/index.js';

export const OPERATIONS_AUDIT_SCHEMA_VERSION = 1 as const;
export const OPERATIONS_AUDIT_MESSAGE_TYPE = 'hvac.operations.audit.v1' as const;
export const OPERATIONS_AUDIT_PRODUCER = 'operations-agent-service' as const;

export type OperationsAuditActorType = 'OPERATOR' | 'SERVICE';

export interface OperationsAuditActor {
  readonly actorType: OperationsAuditActorType;
  readonly actorId: string;
  readonly actorIssuer: string;
  readonly executingService: 'operations-agent-service';
  readonly executingSpiffeId: string;
}

export type OperationsAuditOperation =
  | 'CREATE_INVESTIGATION'
  | 'LIST_INVESTIGATIONS'
  | 'READ_INVESTIGATION'
  | 'START_AGENT_RUN'
  | 'REOPEN_INVESTIGATION'
  | 'ADVANCE_AGENT_RUN'
  | 'PLAN_READS'
  | 'COMMIT_EFFECT'
  | 'PAUSE_AGENT_RUN'
  | 'RESUME_AGENT_RUN'
  | 'REQUEST_OPERATOR_INPUT'
  | 'ACCEPT_OPERATOR_INPUT'
  | 'CANCEL_INVESTIGATION'
  | 'COMPLETE_AGENT_RUN'
  | 'FAIL_AGENT_RUN';

export type OperationsAuditOutcome =
  | 'SUCCEEDED'
  | 'DENIED'
  | 'DUPLICATE'
  | 'PARTIAL'
  | 'UNABLE_TO_CONCLUDE'
  | 'FAILED';

export type OperationsAuditRecordType =
  | 'EVIDENCE'
  | 'ANALYSIS_REFERENCE'
  | 'FINDING'
  | 'TOOL_EXECUTION_RECEIPT'
  | 'OPERATOR_INPUT_ACCEPTED';

export interface OperationsAuditRecordReference {
  readonly recordType: OperationsAuditRecordType;
  readonly recordId: string;
}

export interface OperationsAuditEventV1 {
  readonly eventId: string;
  readonly schemaVersion: typeof OPERATIONS_AUDIT_SCHEMA_VERSION;
  readonly messageType: typeof OPERATIONS_AUDIT_MESSAGE_TYPE;
  readonly producer: typeof OPERATIONS_AUDIT_PRODUCER;
  readonly tenantId: string;
  readonly siteId: string;
  readonly investigationId: string | null;
  readonly runId: string | null;
  readonly investigationRevision: InvestigationRevision | null;
  readonly actor: OperationsAuditActor;
  readonly authorizationDecisionId: string;
  readonly policyRevision: string;
  readonly action: OperationsAuditOperation;
  readonly operation: OperationsAuditOperation;
  readonly outcome: OperationsAuditOutcome;
  readonly occurredAt: number;
  readonly recordReferences: readonly OperationsAuditRecordReference[];
}

export interface CreateOperationsAuditEventInput {
  readonly eventId: string;
  readonly scope: InvestigationScope;
  readonly investigationId: string | null;
  readonly runId: string | null;
  readonly investigationRevision: InvestigationRevision | null;
  readonly actor: OperationsAuditActor;
  readonly authorizationDecisionId: string;
  readonly policyRevision: string;
  readonly operation: OperationsAuditOperation;
  readonly outcome: OperationsAuditOutcome;
  readonly occurredAt: number;
  readonly recordReferences?: readonly OperationsAuditRecordReference[];
}

const maximumIdentityLength = 512;
const maximumEventIdentityLength = 768;
const maximumRecordReferences = 32;

const operations = new Set<OperationsAuditOperation>([
  'CREATE_INVESTIGATION',
  'LIST_INVESTIGATIONS',
  'READ_INVESTIGATION',
  'START_AGENT_RUN',
  'REOPEN_INVESTIGATION',
  'ADVANCE_AGENT_RUN',
  'PLAN_READS',
  'COMMIT_EFFECT',
  'PAUSE_AGENT_RUN',
  'RESUME_AGENT_RUN',
  'REQUEST_OPERATOR_INPUT',
  'ACCEPT_OPERATOR_INPUT',
  'CANCEL_INVESTIGATION',
  'COMPLETE_AGENT_RUN',
  'FAIL_AGENT_RUN',
]);

const outcomes = new Set<OperationsAuditOutcome>([
  'SUCCEEDED',
  'DENIED',
  'DUPLICATE',
  'PARTIAL',
  'UNABLE_TO_CONCLUDE',
  'FAILED',
]);

const actorTypes = new Set<OperationsAuditActorType>(['OPERATOR', 'SERVICE']);
const recordTypes = new Set<OperationsAuditRecordType>([
  'EVIDENCE',
  'ANALYSIS_REFERENCE',
  'FINDING',
  'TOOL_EXECUTION_RECEIPT',
  'OPERATOR_INPUT_ACCEPTED',
]);

const exactKeys = (value: Record<string, unknown>, expected: readonly string[]): boolean => {
  const actual = Object.keys(value).sort();
  const required = [...expected].sort();
  return actual.length === required.length && actual.every((key, index) => key === required[index]);
};

const boundedText = (value: unknown, maximum = maximumIdentityLength): value is string => (
  typeof value === 'string'
  && value.trim().length > 0
  && value.length <= maximum
  && !value.includes(String.fromCharCode(13))
  && !value.includes(String.fromCharCode(10))
);

const nullableIdentity = (value: unknown): value is string | null => (
  value === null || boundedText(value)
);

const safeNonNegativeInteger = (value: unknown): value is number => (
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
);

const actorFromUnknown = (value: unknown): OperationsAuditActor => {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error('Operations Audit actor is invalid.');
  }
  const candidate = value as Record<string, unknown>;
  if (!exactKeys(candidate, [
    'actorType',
    'actorId',
    'actorIssuer',
    'executingService',
    'executingSpiffeId',
  ])
    || !actorTypes.has(candidate.actorType as OperationsAuditActorType)
    || !boundedText(candidate.actorId)
    || !boundedText(candidate.actorIssuer)
    || candidate.executingService !== OPERATIONS_AUDIT_PRODUCER
    || !boundedText(candidate.executingSpiffeId)
    || !candidate.executingSpiffeId.startsWith('spiffe://')) {
    throw new Error('Operations Audit actor is invalid.');
  }
  return Object.freeze({
    actorType: candidate.actorType as OperationsAuditActorType,
    actorId: candidate.actorId,
    actorIssuer: candidate.actorIssuer,
    executingService: OPERATIONS_AUDIT_PRODUCER,
    executingSpiffeId: candidate.executingSpiffeId,
  });
};

const recordReferencesFromUnknown = (
  value: unknown,
): readonly OperationsAuditRecordReference[] => {
  if (!Array.isArray(value) || value.length > maximumRecordReferences) {
    throw new Error('Operations Audit record references are invalid.');
  }
  const identities = new Set<string>();
  const references = value.map((entry) => {
    if (typeof entry !== 'object' || entry === null || Array.isArray(entry)) {
      throw new Error('Operations Audit record reference is invalid.');
    }
    const candidate = entry as Record<string, unknown>;
    if (!exactKeys(candidate, ['recordId', 'recordType'])
      || !recordTypes.has(candidate.recordType as OperationsAuditRecordType)
      || !boundedText(candidate.recordId)) {
      throw new Error('Operations Audit record reference is invalid.');
    }
    const identity = String(candidate.recordType) + ':' + candidate.recordId;
    if (identities.has(identity)) {
      throw new Error('Operations Audit record references must be unique.');
    }
    identities.add(identity);
    return Object.freeze({
      recordType: candidate.recordType as OperationsAuditRecordType,
      recordId: candidate.recordId,
    });
  });
  return Object.freeze(references);
};

export const createOperationsAuditEvent = (
  input: CreateOperationsAuditEventInput,
): OperationsAuditEventV1 => {
  if (input.scope.siteId === null) {
    throw new Error('Operations Audit events require a Site identity.');
  }
  return parseOperationsAuditEvent({
    eventId: input.eventId,
    schemaVersion: OPERATIONS_AUDIT_SCHEMA_VERSION,
    messageType: OPERATIONS_AUDIT_MESSAGE_TYPE,
    producer: OPERATIONS_AUDIT_PRODUCER,
    tenantId: input.scope.tenantId,
    siteId: input.scope.siteId,
    investigationId: input.investigationId,
    runId: input.runId,
    investigationRevision: input.investigationRevision,
    actor: input.actor,
    authorizationDecisionId: input.authorizationDecisionId,
    policyRevision: input.policyRevision,
    action: input.operation,
    operation: input.operation,
    outcome: input.outcome,
    occurredAt: input.occurredAt,
    recordReferences: input.recordReferences ?? [],
  });
};

export const parseOperationsAuditEvent = (value: unknown): OperationsAuditEventV1 => {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error('Operations Audit event is invalid.');
  }
  const candidate = value as Record<string, unknown>;
  if (!exactKeys(candidate, [
    'eventId',
    'schemaVersion',
    'messageType',
    'producer',
    'tenantId',
    'siteId',
    'investigationId',
    'runId',
    'investigationRevision',
    'actor',
    'authorizationDecisionId',
    'policyRevision',
    'action',
    'operation',
    'outcome',
    'occurredAt',
    'recordReferences',
  ])
    || !boundedText(candidate.eventId, maximumEventIdentityLength)
    || candidate.schemaVersion !== OPERATIONS_AUDIT_SCHEMA_VERSION
    || candidate.messageType !== OPERATIONS_AUDIT_MESSAGE_TYPE
    || candidate.producer !== OPERATIONS_AUDIT_PRODUCER
    || !boundedText(candidate.tenantId)
    || !boundedText(candidate.siteId)
    || !nullableIdentity(candidate.investigationId)
    || !nullableIdentity(candidate.runId)
    || !(candidate.investigationRevision === null
      || safeNonNegativeInteger(candidate.investigationRevision))
    || !boundedText(candidate.authorizationDecisionId)
    || !boundedText(candidate.policyRevision)
    || !operations.has(candidate.action as OperationsAuditOperation)
    || !operations.has(candidate.operation as OperationsAuditOperation)
    || candidate.action !== candidate.operation
    || !outcomes.has(candidate.outcome as OperationsAuditOutcome)
    || !safeNonNegativeInteger(candidate.occurredAt)) {
    throw new Error('Operations Audit event is invalid.');
  }
  if ((candidate.investigationId === null) !== (candidate.investigationRevision === null)) {
    throw new Error('Operations Audit Investigation correlation is invalid.');
  }
  const actor = actorFromUnknown(candidate.actor);
  const recordReferences = recordReferencesFromUnknown(candidate.recordReferences);
  if (recordReferences.length > 0 && candidate.investigationId === null) {
    throw new Error('Operations Audit record references require an Investigation.');
  }
  return Object.freeze({
    eventId: candidate.eventId,
    schemaVersion: OPERATIONS_AUDIT_SCHEMA_VERSION,
    messageType: OPERATIONS_AUDIT_MESSAGE_TYPE,
    producer: OPERATIONS_AUDIT_PRODUCER,
    tenantId: candidate.tenantId,
    siteId: candidate.siteId,
    investigationId: candidate.investigationId,
    runId: candidate.runId,
    investigationRevision: candidate.investigationRevision as InvestigationRevision | null,
    actor,
    authorizationDecisionId: candidate.authorizationDecisionId,
    policyRevision: candidate.policyRevision,
    action: candidate.action as OperationsAuditOperation,
    operation: candidate.operation as OperationsAuditOperation,
    outcome: candidate.outcome as OperationsAuditOutcome,
    occurredAt: candidate.occurredAt,
    recordReferences,
  });
};

export const operationsAuditEventId = (input: {
  readonly tenantId: string;
  readonly siteId: string;
  readonly investigationId: string | null;
  readonly runId: string | null;
  readonly revision: number | null;
  readonly operation: OperationsAuditOperation;
  readonly outcome: OperationsAuditOutcome;
  readonly discriminator?: string;
}): string => {
  const parts = [
    'operations-audit-v1',
    input.tenantId,
    input.siteId,
    input.investigationId ?? 'none',
    input.runId ?? 'none',
    input.revision === null ? 'none' : String(input.revision),
    input.operation,
    input.outcome,
    input.discriminator ?? 'primary',
  ];
  for (const part of parts) {
    if (!boundedText(part, maximumIdentityLength)) {
      throw new Error('Operations Audit event identity input is invalid.');
    }
  }
  const identity = parts.join(':');
  if (identity.length > maximumEventIdentityLength) {
    throw new Error('Operations Audit event identity is too long.');
  }
  return identity;
};

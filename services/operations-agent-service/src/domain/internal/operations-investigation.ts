export type InvestigationStatus =
  | 'CREATED'
  | 'RUNNING'
  | 'PAUSED'
  | 'CANCELLED'
  | 'COMPLETED'
  | 'FAILED';

export type AgentRunStatus = 'ACTIVE' | 'PAUSED' | 'CANCELLED' | 'COMPLETED' | 'FAILED';

declare const stepIdentityBrand: unique symbol;
declare const idempotencyKeyBrand: unique symbol;
declare const investigationRevisionBrand: unique symbol;

export type StepIdentity = string & { readonly [stepIdentityBrand]: true };
export type IdempotencyKey = string & { readonly [idempotencyKeyBrand]: true };
export type InvestigationRevision = number & { readonly [investigationRevisionBrand]: true };

export interface InvestigationScope {
  readonly organizationId: string;
  readonly siteId: string | null;
  readonly equipmentId: string | null;
  readonly deviceId: string | null;
}

export interface AgentRunLeaseView {
  readonly id: string;
  readonly runId: string;
  readonly acquiredAt: number;
  readonly expiresAt: number;
}

export interface AgentRunView {
  readonly id: string;
  readonly runtimeRevision: string;
  readonly status: AgentRunStatus;
  readonly startedAt: number;
  readonly pausedAt: number | null;
  readonly endedAt: number | null;
  readonly lease: AgentRunLeaseView | null;
  readonly leaseHistory: readonly AgentRunLeaseView[];
}

export interface CommittedEffectView {
  readonly runId: string;
  readonly stepId: StepIdentity;
  readonly idempotencyKey: IdempotencyKey;
  readonly kind:
    | 'EVIDENCE'
    | 'ANALYSIS_REFERENCE'
    | 'FINDING'
    | 'TOOL_EXECUTION_RECEIPT'
    | 'PROPOSED_ACTION';
  readonly recordId: string;
  readonly committedAt: number;
}

export interface OperationsInvestigationView {
  readonly id: string;
  readonly scope: InvestigationScope;
  readonly status: InvestigationStatus;
  readonly revision: InvestigationRevision;
  readonly activeRunId: string | null;
  readonly runs: readonly AgentRunView[];
  readonly committedEffects: readonly CommittedEffectView[];
  readonly evidenceIds: readonly string[];
  readonly analysisReferenceIds: readonly string[];
  readonly findingIds: readonly string[];
  readonly toolReceiptIds: readonly string[];
  readonly proposedActionIds: readonly string[];
}

export interface OperationsInvestigationSnapshot extends OperationsInvestigationView {
  readonly createdAt: number;
}

export type OperationsInvestigationErrorCode =
  | 'IDENTITY_INVALID'
  | 'TIMESTAMP_INVALID'
  | 'REVISION_INVALID'
  | 'REVISION_STALE'
  | 'INVESTIGATION_STATE_INVALID'
  | 'RUN_NOT_ACTIVE'
  | 'RUN_ID_REUSED'
  | 'LEASE_INVALID'
  | 'LEASE_ID_REUSED'
  | 'LEASE_MISMATCH'
  | 'LEASE_EXPIRED'
  | 'IDEMPOTENCY_KEY_REUSED'
  | 'EFFECT_RECORD_ALREADY_COMMITTED';

export class OperationsInvestigationError extends Error {
  readonly code: OperationsInvestigationErrorCode;

  constructor(code: OperationsInvestigationErrorCode, message: string) {
    super(message);
    this.name = 'OperationsInvestigationError';
    this.code = code;
  }
}

type InternalState = OperationsInvestigationSnapshot;

export interface CreateOperationsInvestigation {
  readonly id: string;
  readonly scope: InvestigationScope;
  readonly createdAt: number;
}

export interface StartAgentRun {
  readonly runId: string;
  readonly runtimeRevision: string;
  readonly leaseId: string;
  readonly leaseAcquiredAt: number;
  readonly leaseExpiresAt: number;
  readonly expectedRevision: InvestigationRevision;
}

export interface PauseAgentRun {
  readonly runId: string;
  readonly leaseId: string;
  readonly at: number;
  readonly expectedRevision: InvestigationRevision;
}

export interface ResumeAgentRun {
  readonly runId: string;
  readonly leaseId: string;
  readonly leaseAcquiredAt: number;
  readonly leaseExpiresAt: number;
  readonly expectedRevision: InvestigationRevision;
}

export interface EndAgentRun {
  readonly runId: string;
  readonly leaseId: string;
  readonly at: number;
  readonly expectedRevision: InvestigationRevision;
}

export interface AssertAgentRunAuthority {
  readonly runId: string;
  readonly leaseId: string;
  readonly at: number;
  readonly expectedRevision: InvestigationRevision;
}

export interface CancelInvestigation {
  readonly at: number;
  readonly expectedRevision: InvestigationRevision;
}

export type ReopenCompletedInvestigation = StartAgentRun;

export type CommittedEffectKind = CommittedEffectView['kind'];

export interface CommitInvestigationEffect {
  readonly runId: string;
  readonly leaseId: string;
  readonly at: number;
  readonly expectedRevision: InvestigationRevision;
  readonly stepId: StepIdentity;
  readonly idempotencyKey: IdempotencyKey;
  readonly kind: CommittedEffectKind;
  readonly recordId: string;
}

export interface CommitEffectResult {
  readonly outcome: 'COMMITTED' | 'DUPLICATE';
  readonly investigation: OperationsInvestigation;
  readonly effect: CommittedEffectView;
}

const requireIdentity = (value: string, label: string): string => {
  if (value.trim().length === 0) {
    throw new OperationsInvestigationError('IDENTITY_INVALID', `${label} must not be empty.`);
  }
  return value;
};

export const createStepIdentity = (value: string): StepIdentity => (
  requireIdentity(value, 'Step Identity') as StepIdentity
);

export const createIdempotencyKey = (value: string): IdempotencyKey => (
  requireIdentity(value, 'Idempotency Key') as IdempotencyKey
);

export const createInvestigationRevision = (value: number): InvestigationRevision => {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new OperationsInvestigationError(
      'REVISION_INVALID',
      'Investigation Revision must be a non-negative safe integer.',
    );
  }
  return value as InvestigationRevision;
};

const requireTimestamp = (value: number, label: string): number => {
  if (!Number.isFinite(value)) {
    throw new OperationsInvestigationError('TIMESTAMP_INVALID', `${label} must be finite.`);
  }
  return value;
};

const requireLeaseWindow = (
  acquiredAt: number,
  expiresAt: number,
): Pick<AgentRunLeaseView, 'acquiredAt' | 'expiresAt'> => {
  requireTimestamp(acquiredAt, 'leaseAcquiredAt');
  requireTimestamp(expiresAt, 'leaseExpiresAt');
  if (expiresAt <= acquiredAt) {
    throw new OperationsInvestigationError(
      'LEASE_INVALID',
      'Agent Run Lease expiry must be after acquisition.',
    );
  }
  return { acquiredAt, expiresAt };
};

const cloneScope = (scope: InvestigationScope): InvestigationScope => ({
  organizationId: scope.organizationId,
  siteId: scope.siteId,
  equipmentId: scope.equipmentId,
  deviceId: scope.deviceId,
});

const cloneLease = (lease: AgentRunLeaseView | null): AgentRunLeaseView | null => (
  lease === null ? null : { ...lease }
);

const cloneRun = (run: AgentRunView): AgentRunView => ({
  ...run,
  lease: cloneLease(run.lease),
  leaseHistory: run.leaseHistory.map((lease) => ({ ...lease })),
});

const cloneEffect = (effect: CommittedEffectView): CommittedEffectView => ({ ...effect });

const cloneState = (state: InternalState): InternalState => ({
  ...state,
  scope: cloneScope(state.scope),
  runs: state.runs.map(cloneRun),
  committedEffects: state.committedEffects.map(cloneEffect),
  evidenceIds: [...state.evidenceIds],
  analysisReferenceIds: [...state.analysisReferenceIds],
  findingIds: [...state.findingIds],
  toolReceiptIds: [...state.toolReceiptIds],
  proposedActionIds: [...state.proposedActionIds],
});

const investigationStatuses = new Set<InvestigationStatus>([
  'CREATED',
  'RUNNING',
  'PAUSED',
  'CANCELLED',
  'COMPLETED',
  'FAILED',
]);

const runStatuses = new Set<AgentRunStatus>([
  'ACTIVE',
  'PAUSED',
  'CANCELLED',
  'COMPLETED',
  'FAILED',
]);

const effectKinds = new Set<CommittedEffectKind>([
  'EVIDENCE',
  'ANALYSIS_REFERENCE',
  'FINDING',
  'TOOL_EXECUTION_RECEIPT',
  'PROPOSED_ACTION',
]);

const arraysEqual = (left: readonly string[], right: readonly string[]): boolean => (
  left.length === right.length && left.every((value, index) => value === right[index])
);

const requireNullableIdentity = (value: string | null, label: string): string | null => (
  value === null ? null : requireIdentity(value, label)
);

const validateSnapshot = (snapshot: OperationsInvestigationSnapshot): InternalState => {
  const id = requireIdentity(snapshot.id, 'Investigation identity');
  const scope: InvestigationScope = {
    organizationId: requireIdentity(snapshot.scope.organizationId, 'Organization identity'),
    siteId: requireNullableIdentity(snapshot.scope.siteId, 'Site identity'),
    equipmentId: requireNullableIdentity(snapshot.scope.equipmentId, 'Equipment identity'),
    deviceId: requireNullableIdentity(snapshot.scope.deviceId, 'Device identity'),
  };
  const createdAt = requireTimestamp(snapshot.createdAt, 'createdAt');
  const revision = createInvestigationRevision(snapshot.revision);
  if (!investigationStatuses.has(snapshot.status)) {
    throw new OperationsInvestigationError(
      'INVESTIGATION_STATE_INVALID',
      `Unknown Investigation status ${String(snapshot.status)}.`,
    );
  }

  const runIds = new Set<string>();
  const leaseIds = new Set<string>();
  const runs = snapshot.runs.map((run): AgentRunView => {
    const runId = requireIdentity(run.id, 'Agent Run identity');
    if (runIds.has(runId)) {
      throw new OperationsInvestigationError('RUN_ID_REUSED', `Agent Run ${runId} is duplicated.`);
    }
    runIds.add(runId);
    requireIdentity(run.runtimeRevision, 'Agent Runtime Revision');
    if (!runStatuses.has(run.status)) {
      throw new OperationsInvestigationError(
        'INVESTIGATION_STATE_INVALID',
        `Unknown Agent Run status ${String(run.status)}.`,
      );
    }
    const startedAt = requireTimestamp(run.startedAt, 'Agent Run startedAt');
    if (startedAt < createdAt) {
      throw new OperationsInvestigationError(
        'TIMESTAMP_INVALID',
        'Agent Run cannot start before Investigation creation.',
      );
    }
    const pausedAt = run.pausedAt === null
      ? null
      : requireTimestamp(run.pausedAt, 'Agent Run pausedAt');
    const endedAt = run.endedAt === null
      ? null
      : requireTimestamp(run.endedAt, 'Agent Run endedAt');
    const leaseHistory = run.leaseHistory.map((lease): AgentRunLeaseView => {
      const leaseId = requireIdentity(lease.id, 'Agent Run Lease identity');
      if (leaseIds.has(leaseId)) {
        throw new OperationsInvestigationError(
          'LEASE_ID_REUSED',
          `Agent Run Lease ${leaseId} is duplicated.`,
        );
      }
      leaseIds.add(leaseId);
      if (lease.runId !== runId) {
        throw new OperationsInvestigationError(
          'LEASE_MISMATCH',
          `Agent Run Lease ${leaseId} is bound to another Run.`,
        );
      }
      const window = requireLeaseWindow(lease.acquiredAt, lease.expiresAt);
      return { ...window, id: leaseId, runId };
    });
    const lease = run.lease === null
      ? null
      : leaseHistory.find((candidate) => candidate.id === run.lease?.id) ?? null;
    if (run.lease !== null && (lease === null
      || lease.runId !== run.lease.runId
      || lease.acquiredAt !== run.lease.acquiredAt
      || lease.expiresAt !== run.lease.expiresAt)) {
      throw new OperationsInvestigationError(
        'LEASE_MISMATCH',
        `Active Agent Run Lease for ${runId} is not present in Lease History.`,
      );
    }
    if ((run.status === 'ACTIVE') !== (lease !== null)) {
      throw new OperationsInvestigationError(
        'INVESTIGATION_STATE_INVALID',
        `Agent Run ${runId} active status and Lease do not agree.`,
      );
    }
    if (run.status === 'PAUSED' && pausedAt === null) {
      throw new OperationsInvestigationError(
        'INVESTIGATION_STATE_INVALID',
        `Paused Agent Run ${runId} requires pausedAt.`,
      );
    }
    if ((run.status === 'CANCELLED' || run.status === 'COMPLETED' || run.status === 'FAILED')
      && endedAt === null) {
      throw new OperationsInvestigationError(
        'INVESTIGATION_STATE_INVALID',
        `Terminal Agent Run ${runId} requires endedAt.`,
      );
    }
    return {
      id: runId,
      runtimeRevision: run.runtimeRevision,
      status: run.status,
      startedAt,
      pausedAt,
      endedAt,
      lease,
      leaseHistory,
    };
  });

  const activeRun = snapshot.activeRunId === null
    ? null
    : runs.find((run) => run.id === snapshot.activeRunId) ?? null;
  const nonTerminalRuns = runs.filter((run) => run.status === 'ACTIVE' || run.status === 'PAUSED');
  if (nonTerminalRuns.length > 1
    || (nonTerminalRuns.length === 1 && nonTerminalRuns[0]?.id !== snapshot.activeRunId)) {
    throw new OperationsInvestigationError(
      'INVESTIGATION_STATE_INVALID',
      'An Investigation may retain at most one non-terminal Agent Run.',
    );
  }
  if (snapshot.activeRunId !== null && activeRun === null) {
    throw new OperationsInvestigationError(
      'RUN_NOT_ACTIVE',
      `Active Agent Run ${snapshot.activeRunId} does not exist.`,
    );
  }
  if (snapshot.status === 'RUNNING' && activeRun?.status !== 'ACTIVE') {
    throw new OperationsInvestigationError(
      'INVESTIGATION_STATE_INVALID',
      'A running Investigation requires one active Agent Run.',
    );
  }
  if (snapshot.status === 'PAUSED' && activeRun?.status !== 'PAUSED') {
    throw new OperationsInvestigationError(
      'INVESTIGATION_STATE_INVALID',
      'A paused Investigation requires one paused Agent Run.',
    );
  }
  if (snapshot.status !== 'RUNNING' && snapshot.status !== 'PAUSED'
    && snapshot.activeRunId !== null) {
    throw new OperationsInvestigationError(
      'INVESTIGATION_STATE_INVALID',
      `Investigation status ${snapshot.status} cannot retain an active Run.`,
    );
  }
  if (snapshot.status === 'CREATED' && runs.length !== 0) {
    throw new OperationsInvestigationError(
      'INVESTIGATION_STATE_INVALID',
      'A created Investigation cannot already contain Agent Runs.',
    );
  }

  const idempotencyKeys = new Set<string>();
  const effectRecords = new Set<string>();
  const committedEffects = snapshot.committedEffects.map((effect): CommittedEffectView => {
    const effectRun = runs.find((run) => run.id === effect.runId);
    if (effectRun === undefined) {
      throw new OperationsInvestigationError(
        'RUN_NOT_ACTIVE',
        `Committed effect references unknown Agent Run ${effect.runId}.`,
      );
    }
    const stepId = createStepIdentity(effect.stepId);
    const idempotencyKey = createIdempotencyKey(effect.idempotencyKey);
    const recordId = requireIdentity(effect.recordId, 'Committed effect record identity');
    if (!effectKinds.has(effect.kind)) {
      throw new OperationsInvestigationError(
        'INVESTIGATION_STATE_INVALID',
        `Unknown committed effect kind ${String(effect.kind)}.`,
      );
    }
    if (idempotencyKeys.has(idempotencyKey)) {
      throw new OperationsInvestigationError(
        'IDEMPOTENCY_KEY_REUSED',
        `Idempotency Key ${idempotencyKey} is duplicated.`,
      );
    }
    idempotencyKeys.add(idempotencyKey);
    const recordIdentity = `${effect.kind}:${recordId}`;
    if (effectRecords.has(recordIdentity)) {
      throw new OperationsInvestigationError(
        'EFFECT_RECORD_ALREADY_COMMITTED',
        `${recordIdentity} is duplicated.`,
      );
    }
    effectRecords.add(recordIdentity);
    const committedAt = requireTimestamp(effect.committedAt, 'Committed effect committedAt');
    const authorizedByLease = effectRun.leaseHistory.some((lease) => (
      committedAt >= lease.acquiredAt && committedAt < lease.expiresAt
    ));
    if (!authorizedByLease || (effectRun.endedAt !== null && committedAt > effectRun.endedAt)) {
      throw new OperationsInvestigationError(
        'LEASE_MISMATCH',
        `Committed effect ${idempotencyKey} is outside the Agent Run Lease history.`,
      );
    }
    return {
      runId: effect.runId,
      stepId,
      idempotencyKey,
      kind: effect.kind,
      recordId,
      committedAt,
    };
  });

  const evidenceIds = committedEffects
    .filter((effect) => effect.kind === 'EVIDENCE')
    .map((effect) => effect.recordId);
  const analysisReferenceIds = committedEffects
    .filter((effect) => effect.kind === 'ANALYSIS_REFERENCE')
    .map((effect) => effect.recordId);
  const findingIds = committedEffects
    .filter((effect) => effect.kind === 'FINDING')
    .map((effect) => effect.recordId);
  const toolReceiptIds = committedEffects
    .filter((effect) => effect.kind === 'TOOL_EXECUTION_RECEIPT')
    .map((effect) => effect.recordId);
  const proposedActionIds = committedEffects
    .filter((effect) => effect.kind === 'PROPOSED_ACTION')
    .map((effect) => effect.recordId);
  if (!arraysEqual(snapshot.evidenceIds, evidenceIds)
    || !arraysEqual(snapshot.analysisReferenceIds ?? [], analysisReferenceIds)
    || !arraysEqual(snapshot.findingIds, findingIds)
    || !arraysEqual(snapshot.toolReceiptIds ?? [], toolReceiptIds)
    || !arraysEqual(snapshot.proposedActionIds, proposedActionIds)) {
    throw new OperationsInvestigationError(
      'INVESTIGATION_STATE_INVALID',
      'Committed effect record indexes do not match the effect journal.',
    );
  }

  return {
    id,
    scope: cloneScope(scope),
    createdAt,
    status: snapshot.status,
    revision,
    activeRunId: snapshot.activeRunId,
    runs,
    committedEffects,
    evidenceIds,
    analysisReferenceIds,
    findingIds,
    toolReceiptIds,
    proposedActionIds,
  };
};

const replaceRun = (
  runs: readonly AgentRunView[],
  runId: string,
  replacement: AgentRunView,
): readonly AgentRunView[] => runs.map((run) => (run.id === runId ? replacement : run));

export class OperationsInvestigation {
  readonly #state: InternalState;

  private constructor(state: InternalState) {
    this.#state = cloneState(state);
  }

  static create(command: CreateOperationsInvestigation): OperationsInvestigation {
    const id = requireIdentity(command.id, 'Investigation identity');
    requireIdentity(command.scope.organizationId, 'Organization identity');
    requireTimestamp(command.createdAt, 'createdAt');

    return new OperationsInvestigation({
      id,
      scope: cloneScope(command.scope),
      createdAt: command.createdAt,
      status: 'CREATED',
      revision: createInvestigationRevision(0),
      activeRunId: null,
      runs: [],
      committedEffects: [],
      evidenceIds: [],
      analysisReferenceIds: [],
      findingIds: [],
      toolReceiptIds: [],
      proposedActionIds: [],
    });
  }

  static restore(snapshot: OperationsInvestigationSnapshot): OperationsInvestigation {
    return new OperationsInvestigation(validateSnapshot(snapshot));
  }

  view(): OperationsInvestigationView {
    const { createdAt: _createdAt, ...view } = cloneState(this.#state);
    return view;
  }

  snapshot(): OperationsInvestigationSnapshot {
    return cloneState(this.#state);
  }

  startRun(command: StartAgentRun): OperationsInvestigation {
    this.#requireRevision(command.expectedRevision);
    this.#requireStatus('CREATED');
    const run = this.#buildRun(command);

    return this.#next({
      status: 'RUNNING',
      activeRunId: run.id,
      runs: [...this.#state.runs, run],
    });
  }

  pauseRun(command: PauseAgentRun): OperationsInvestigation {
    this.#requireRevision(command.expectedRevision);
    this.#requireStatus('RUNNING');
    const run = this.#requireActiveRun(command.runId);
    this.#requireLease(run, command.leaseId, command.at);

    return this.#next({
      status: 'PAUSED',
      runs: replaceRun(this.#state.runs, run.id, {
        ...run,
        status: 'PAUSED',
        pausedAt: command.at,
        lease: null,
      }),
    });
  }

  resumeRun(command: ResumeAgentRun): OperationsInvestigation {
    this.#requireRevision(command.expectedRevision);
    this.#requireStatus('PAUSED');
    const run = this.#requireActiveRun(command.runId);
    if (run.status !== 'PAUSED' || run.lease !== null) {
      throw new OperationsInvestigationError(
        'INVESTIGATION_STATE_INVALID',
        'Only a paused Agent Run without a lease can resume.',
      );
    }
    const leaseId = requireIdentity(command.leaseId, 'Agent Run Lease identity');
    this.#requireUnusedLeaseId(leaseId);
    const window = requireLeaseWindow(command.leaseAcquiredAt, command.leaseExpiresAt);
    if (command.leaseAcquiredAt < (run.pausedAt ?? run.startedAt)) {
      throw new OperationsInvestigationError(
        'TIMESTAMP_INVALID',
        'A resumed Agent Run Lease cannot begin before the Run was paused.',
      );
    }
    const lease: AgentRunLeaseView = { ...window, id: leaseId, runId: run.id };

    return this.#next({
      status: 'RUNNING',
      runs: replaceRun(this.#state.runs, run.id, {
        ...run,
        status: 'ACTIVE',
        pausedAt: null,
        lease,
        leaseHistory: [...run.leaseHistory, lease],
      }),
    });
  }

  assertRunAuthority(command: AssertAgentRunAuthority): AgentRunView {
    this.#requireRevision(command.expectedRevision);
    this.#requireStatus('RUNNING');
    const run = this.#requireActiveRun(command.runId);
    this.#requireLease(run, command.leaseId, command.at);
    return cloneRun(run);
  }

  cancel(command: CancelInvestigation): OperationsInvestigation {
    this.#requireRevision(command.expectedRevision);
    requireTimestamp(command.at, 'at');
    if (command.at < this.#state.createdAt) {
      throw new OperationsInvestigationError(
        'TIMESTAMP_INVALID',
        'Investigation cancellation cannot occur before creation.',
      );
    }
    if (this.#state.status === 'CANCELLED'
      || this.#state.status === 'COMPLETED'
      || this.#state.status === 'FAILED') {
      throw new OperationsInvestigationError(
        'INVESTIGATION_STATE_INVALID',
        `Investigation cannot be cancelled from ${this.#state.status}.`,
      );
    }

    const runs = this.#state.activeRunId === null
      ? this.#state.runs
      : replaceRun(
        this.#state.runs,
        this.#state.activeRunId,
        this.#terminalRun(this.#requireActiveRun(this.#state.activeRunId), 'CANCELLED', command.at),
      );

    return this.#next({
      status: 'CANCELLED',
      activeRunId: null,
      runs,
    });
  }

  completeRun(command: EndAgentRun): OperationsInvestigation {
    return this.#finishRun(command, 'COMPLETED');
  }

  failRun(command: EndAgentRun): OperationsInvestigation {
    return this.#finishRun(command, 'FAILED');
  }

  reopenCompleted(command: ReopenCompletedInvestigation): OperationsInvestigation {
    this.#requireRevision(command.expectedRevision);
    this.#requireStatus('COMPLETED');
    const run = this.#buildRun(command);

    return this.#next({
      status: 'RUNNING',
      activeRunId: run.id,
      runs: [...this.#state.runs, run],
    });
  }

  commitEffect(command: CommitInvestigationEffect): CommitEffectResult {
    const stepId = createStepIdentity(command.stepId);
    const idempotencyKey = createIdempotencyKey(command.idempotencyKey);
    const recordId = requireIdentity(command.recordId, 'Committed effect record identity');
    const duplicate = this.#state.committedEffects.find(
      (effect) => effect.idempotencyKey === idempotencyKey,
    );

    if (duplicate !== undefined) {
      if (duplicate.runId === command.runId
        && duplicate.stepId === stepId
        && duplicate.kind === command.kind
        && duplicate.recordId === recordId) {
        return {
          outcome: 'DUPLICATE',
          investigation: this,
          effect: cloneEffect(duplicate),
        };
      }
      throw new OperationsInvestigationError(
        'IDEMPOTENCY_KEY_REUSED',
        `Idempotency Key ${idempotencyKey} is already bound to another effect.`,
      );
    }

    this.#requireRevision(command.expectedRevision);
    this.#requireStatus('RUNNING');
    const run = this.#requireActiveRun(command.runId);
    this.#requireLease(run, command.leaseId, command.at);
    const existingRecord = this.#state.committedEffects.find((effect) => (
      effect.recordId === recordId
    ));
    if (existingRecord !== undefined) {
      throw new OperationsInvestigationError(
        'EFFECT_RECORD_ALREADY_COMMITTED',
        `Record ${recordId} is already committed as ${existingRecord.kind}.`,
      );
    }

    const effect: CommittedEffectView = {
      runId: run.id,
      stepId,
      idempotencyKey,
      kind: command.kind,
      recordId,
      committedAt: command.at,
    };
    const investigation = this.#next({
      committedEffects: [...this.#state.committedEffects, effect],
      evidenceIds: command.kind === 'EVIDENCE'
        ? [...this.#state.evidenceIds, recordId]
        : this.#state.evidenceIds,
      analysisReferenceIds: command.kind === 'ANALYSIS_REFERENCE'
        ? [...this.#state.analysisReferenceIds, recordId]
        : this.#state.analysisReferenceIds,
      findingIds: command.kind === 'FINDING'
        ? [...this.#state.findingIds, recordId]
        : this.#state.findingIds,
      toolReceiptIds: command.kind === 'TOOL_EXECUTION_RECEIPT'
        ? [...this.#state.toolReceiptIds, recordId]
        : this.#state.toolReceiptIds,
      proposedActionIds: command.kind === 'PROPOSED_ACTION'
        ? [...this.#state.proposedActionIds, recordId]
        : this.#state.proposedActionIds,
    });

    return {
      outcome: 'COMMITTED',
      investigation,
      effect: cloneEffect(effect),
    };
  }

  #buildRun(command: StartAgentRun): AgentRunView {
    const runId = requireIdentity(command.runId, 'Agent Run identity');
    if (this.#state.runs.some((run) => run.id === runId)) {
      throw new OperationsInvestigationError(
        'RUN_ID_REUSED',
        `Agent Run identity ${runId} has already been used by this Investigation.`,
      );
    }
    const runtimeRevision = requireIdentity(command.runtimeRevision, 'Agent Runtime Revision');
    const leaseId = requireIdentity(command.leaseId, 'Agent Run Lease identity');
    this.#requireUnusedLeaseId(leaseId);
    const window = requireLeaseWindow(command.leaseAcquiredAt, command.leaseExpiresAt);
    if (command.leaseAcquiredAt < this.#state.createdAt) {
      throw new OperationsInvestigationError(
        'TIMESTAMP_INVALID',
        'Agent Run cannot start before the Investigation was created.',
      );
    }
    const lease: AgentRunLeaseView = { ...window, id: leaseId, runId };

    return {
      id: runId,
      runtimeRevision,
      status: 'ACTIVE',
      startedAt: command.leaseAcquiredAt,
      pausedAt: null,
      endedAt: null,
      lease,
      leaseHistory: [lease],
    };
  }

  #finishRun(command: EndAgentRun, status: 'COMPLETED' | 'FAILED'): OperationsInvestigation {
    this.#requireRevision(command.expectedRevision);
    this.#requireStatus('RUNNING');
    const run = this.#requireActiveRun(command.runId);
    this.#requireLease(run, command.leaseId, command.at);

    return this.#next({
      status,
      activeRunId: null,
      runs: replaceRun(this.#state.runs, run.id, this.#terminalRun(run, status, command.at)),
    });
  }

  #terminalRun(
    run: AgentRunView,
    status: 'CANCELLED' | 'COMPLETED' | 'FAILED',
    endedAt: number,
  ): AgentRunView {
    requireTimestamp(endedAt, 'endedAt');
    if (endedAt < run.startedAt) {
      throw new OperationsInvestigationError(
        'TIMESTAMP_INVALID',
        'An Agent Run cannot end before it started.',
      );
    }
    return {
      ...run,
      status,
      endedAt,
      lease: null,
    };
  }

  #next(changes: Partial<InternalState>): OperationsInvestigation {
    return new OperationsInvestigation({
      ...this.#state,
      ...changes,
      revision: createInvestigationRevision(this.#state.revision + 1),
    });
  }

  #requireRevision(expectedRevision: InvestigationRevision): void {
    createInvestigationRevision(expectedRevision);
    if (expectedRevision !== this.#state.revision) {
      throw new OperationsInvestigationError(
        'REVISION_STALE',
        `Expected Investigation Revision ${expectedRevision}, current revision is ${this.#state.revision}.`,
      );
    }
  }

  #requireStatus(expected: InvestigationStatus): void {
    if (this.#state.status !== expected) {
      throw new OperationsInvestigationError(
        'INVESTIGATION_STATE_INVALID',
        `Investigation must be ${expected}, current status is ${this.#state.status}.`,
      );
    }
  }

  #requireActiveRun(runId: string): AgentRunView {
    if (this.#state.activeRunId !== runId) {
      throw new OperationsInvestigationError('RUN_NOT_ACTIVE', `Agent Run ${runId} is not active.`);
    }
    const run = this.#state.runs.find((candidate) => candidate.id === runId);
    if (run === undefined) {
      throw new OperationsInvestigationError('RUN_NOT_ACTIVE', `Agent Run ${runId} does not exist.`);
    }
    return run;
  }

  #requireLease(run: AgentRunView, leaseId: string, at: number): AgentRunLeaseView {
    requireTimestamp(at, 'at');
    if (run.lease === null || run.lease.id !== leaseId || run.lease.runId !== run.id) {
      throw new OperationsInvestigationError('LEASE_MISMATCH', 'Agent Run Lease does not match.');
    }
    if (at < run.lease.acquiredAt) {
      throw new OperationsInvestigationError(
        'TIMESTAMP_INVALID',
        'A write cannot occur before the Agent Run Lease was acquired.',
      );
    }
    if (at >= run.lease.expiresAt) {
      throw new OperationsInvestigationError('LEASE_EXPIRED', 'Agent Run Lease has expired.');
    }
    return run.lease;
  }

  #requireUnusedLeaseId(leaseId: string): void {
    const alreadyUsed = this.#state.runs.some((run) => (
      run.leaseHistory.some((lease) => lease.id === leaseId)
    ));
    if (alreadyUsed) {
      throw new OperationsInvestigationError(
        'LEASE_ID_REUSED',
        `Agent Run Lease identity ${leaseId} has already been used by this Investigation.`,
      );
    }
  }

}

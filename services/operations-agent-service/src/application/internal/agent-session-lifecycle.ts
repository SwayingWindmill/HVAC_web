import type {
  AgentArtifact,
  AgentMessage,
  AgentRun,
  AgentSession,
  AgentToolExecution,
} from '../../agent/index.js';

export type AgentSessionLifecycleErrorCode =
  | 'SESSION_NOT_FOUND'
  | 'SESSION_REVISION_CONFLICT'
  | 'SESSION_TERMINAL'
  | 'SESSION_IDENTITY_CONFLICT'
  | 'RUN_ALREADY_ACTIVE'
  | 'RUN_NOT_FOUND'
  | 'RUN_STALE'
  | 'IDEMPOTENCY_CONFLICT'
  | 'LIFECYCLE_INPUT_INVALID';

export class AgentSessionLifecycleError extends Error {
  readonly code: AgentSessionLifecycleErrorCode;

  constructor(code: AgentSessionLifecycleErrorCode, message: string) {
    super(message);
    this.name = 'AgentSessionLifecycleError';
    this.code = code;
  }
}

export interface AgentSessionDescriptor {
  readonly id: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly agentDefinitionId: string;
  readonly createdBy: string;
  readonly createdAt: number;
}

export interface AgentSessionState {
  readonly session: AgentSession;
  readonly runs: readonly AgentRun[];
  readonly messages: readonly AgentMessage[];
  readonly toolExecutions: readonly AgentToolExecution[];
  readonly artifacts: readonly AgentArtifact[];
}

export interface AgentSessionStateStore {
  get(sessionId: string): Promise<AgentSessionState | null>;
  transact(
    sessionId: string,
    update: (current: AgentSessionState | null) => AgentSessionState,
  ): Promise<AgentSessionState>;
}

export interface StartAgentSessionRunCommand {
  readonly session: AgentSessionDescriptor;
  readonly expectedSessionRevision: number | null;
  readonly run: AgentRun;
  readonly operatorMessage: AgentMessage;
}

export interface InterruptAgentSessionRunCommand {
  readonly sessionId: string;
  readonly runId: string;
  readonly at: number;
}

export interface CompleteAgentSessionRunCommand {
  readonly sessionId: string;
  readonly runId: string;
  readonly expectedSessionRevision: number;
  readonly run: AgentRun;
  readonly sessionStatus: 'WAITING_FOR_INPUT' | 'COMPLETED' | 'FAILED';
  readonly finalizedMessages: readonly AgentMessage[];
  readonly toolExecutions: readonly AgentToolExecution[];
  readonly artifacts: readonly AgentArtifact[];
}

export interface CancelAgentSessionRunCommand {
  readonly sessionId: string;
  readonly runId: string;
  readonly expectedSessionRevision: number;
  readonly at: number;
}

export interface AgentSessionLifecycle {
  get(sessionId: string): Promise<AgentSessionState | null>;
  start(command: StartAgentSessionRunCommand): Promise<AgentSessionState>;
  interrupt(command: InterruptAgentSessionRunCommand): Promise<AgentSessionState>;
  complete(command: CompleteAgentSessionRunCommand): Promise<AgentSessionState>;
  cancel(command: CancelAgentSessionRunCommand): Promise<AgentSessionState>;
}

export interface CreateAgentSessionLifecycleInput {
  readonly store: AgentSessionStateStore;
}

const fail = (code: AgentSessionLifecycleErrorCode, message: string): never => {
  throw new AgentSessionLifecycleError(code, message);
};

const requireIdentity = (value: string, label: string): string => {
  if (value.trim().length === 0) fail('LIFECYCLE_INPUT_INVALID', `${label} must not be empty.`);
  return value;
};

const requireTimestamp = (value: number, label: string): number => {
  if (!Number.isSafeInteger(value) || value < 0) {
    fail('LIFECYCLE_INPUT_INVALID', `${label} must be a non-negative safe integer.`);
  }
  return value;
};

const freezeState = (state: AgentSessionState): AgentSessionState => Object.freeze({
  session: Object.freeze(state.session),
  runs: Object.freeze([...state.runs]),
  messages: Object.freeze([...state.messages]),
  toolExecutions: Object.freeze([...state.toolExecutions]),
  artifacts: Object.freeze([...state.artifacts]),
});

const descriptorMatches = (session: AgentSession, descriptor: AgentSessionDescriptor): boolean => (
  session.id === descriptor.id
  && session.tenantId === descriptor.tenantId
  && session.siteId === descriptor.siteId
  && session.agentDefinitionId === descriptor.agentDefinitionId
  && session.createdBy === descriptor.createdBy
  && session.createdAt === descriptor.createdAt
);

const requireState = (current: AgentSessionState | null): AgentSessionState => {
  if (current === null) return fail('SESSION_NOT_FOUND', 'Session was not found.');
  return current;
};

const requireRun = (run: AgentRun | undefined, message: string): AgentRun => {
  if (run === undefined) return fail('RUN_NOT_FOUND', message);
  return run;
};

const validateDescriptor = (descriptor: AgentSessionDescriptor): void => {
  requireIdentity(descriptor.id, 'Session id');
  requireIdentity(descriptor.tenantId, 'Tenant id');
  requireIdentity(descriptor.siteId, 'Site id');
  requireIdentity(descriptor.agentDefinitionId, 'Agent definition id');
  requireIdentity(descriptor.createdBy, 'Session creator');
  requireTimestamp(descriptor.createdAt, 'Session createdAt');
};

const validateInitialRun = (
  descriptor: AgentSessionDescriptor,
  run: AgentRun,
  operatorMessage: AgentMessage,
): void => {
  requireIdentity(run.id, 'Run id');
  if (run.sessionId !== descriptor.id
    || run.status !== 'RUNNING'
    || run.finishedAt !== null
    || run.failureCode !== null) {
    fail('LIFECYCLE_INPUT_INVALID', 'A started Run must be an unfinalized RUNNING Run for this Session.');
  }
  requireTimestamp(run.startedAt, 'Run startedAt');
  if (operatorMessage.sessionId !== descriptor.id
    || (operatorMessage.runId !== null && operatorMessage.runId !== run.id)
    || operatorMessage.role !== 'OPERATOR') {
    fail('LIFECYCLE_INPUT_INVALID', 'The start Message must be an OPERATOR Message for this Session and Run.');
  }
  requireIdentity(operatorMessage.id, 'Operator Message id');
  requireIdentity(operatorMessage.content, 'Operator Message content');
  requireTimestamp(operatorMessage.createdAt, 'Operator Message createdAt');
};

const sameStartIdentity = (
  persistedRun: AgentRun,
  requestedRun: AgentRun,
  persistedMessage: AgentMessage | undefined,
  requestedMessage: AgentMessage,
): boolean => (
  persistedRun.id === requestedRun.id
  && persistedRun.sessionId === requestedRun.sessionId
  && persistedRun.modelRef.provider === requestedRun.modelRef.provider
  && persistedRun.modelRef.model === requestedRun.modelRef.model
  && persistedRun.startedAt === requestedRun.startedAt
  && persistedMessage?.id === requestedMessage.id
  && persistedMessage.sessionId === requestedMessage.sessionId
  && persistedMessage.runId === requestedMessage.runId
  && persistedMessage.role === requestedMessage.role
  && persistedMessage.content === requestedMessage.content
  && persistedMessage.createdAt === requestedMessage.createdAt
);

const startState = (
  current: AgentSessionState | null,
  command: StartAgentSessionRunCommand,
): AgentSessionState => {
  validateDescriptor(command.session);
  validateInitialRun(command.session, command.run, command.operatorMessage);

  if (current === null) {
    if (command.expectedSessionRevision !== null) {
      fail('SESSION_REVISION_CONFLICT', 'A new Session start cannot require an existing revision.');
    }
    return freezeState({
      session: {
        ...command.session,
        status: 'ACTIVE',
        activeRunId: command.run.id,
        revision: 0,
        updatedAt: command.run.startedAt,
      },
      runs: [command.run],
      messages: [command.operatorMessage],
      toolExecutions: [],
      artifacts: [],
    });
  }

  if (!descriptorMatches(current.session, command.session)) {
    fail('SESSION_IDENTITY_CONFLICT', 'Session immutable identity does not match the persisted Session.');
  }

  const persistedRun = current.runs.find(({ id }) => id === command.run.id);
  if (persistedRun !== undefined) {
    const persistedMessage = current.messages.find(({ id }) => id === command.operatorMessage.id);
    if (!sameStartIdentity(persistedRun, command.run, persistedMessage, command.operatorMessage)) {
      fail('IDEMPOTENCY_CONFLICT', 'Run start identity was already used with different durable input.');
    }
    return current;
  }

  if (command.expectedSessionRevision !== current.session.revision) {
    fail('SESSION_REVISION_CONFLICT', 'Session revision changed before the Run could start.');
  }
  if (current.session.status === 'COMPLETED'
    || current.session.status === 'FAILED'
    || current.session.status === 'CANCELLED') {
    fail('SESSION_TERMINAL', 'A terminal Session cannot start another Run.');
  }
  if (current.session.status === 'ACTIVE') {
    const activeRun = requireRun(
      current.runs.find(({ id }) => id === current.session.activeRunId),
      'The active Run is missing from committed Session state.',
    );
    if (activeRun.status === 'RUNNING') {
      fail('RUN_ALREADY_ACTIVE', 'A Session may have only one RUNNING Run.');
    }
    if (activeRun.status !== 'FAILED' || activeRun.failureCode !== 'RUN_INTERRUPTED') {
      fail('RUN_STALE', 'Only an interrupted active Run may be replaced after recovery.');
    }
  }

  return freezeState({
    ...current,
    session: {
      ...current.session,
      status: 'ACTIVE',
      activeRunId: command.run.id,
      revision: current.session.revision + 1,
      updatedAt: command.run.startedAt,
    },
    runs: [...current.runs, command.run],
    messages: [...current.messages, command.operatorMessage],
  });
};

const canonicalize = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (typeof value !== 'object' || value === null) return value;
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => [key, canonicalize(item)]),
  );
};

const recordMatches = (left: unknown, right: unknown): boolean => (
  JSON.stringify(canonicalize(left)) === JSON.stringify(canonicalize(right))
);

const appendExactRecords = <T extends { readonly id: string }>(
  persisted: readonly T[],
  requested: readonly T[],
  label: string,
): readonly T[] => {
  const ids = new Set<string>();
  for (const record of requested) {
    requireIdentity(record.id, `${label} id`);
    if (ids.has(record.id)) fail('LIFECYCLE_INPUT_INVALID', `${label} ids must be unique.`);
    ids.add(record.id);
    const existing = persisted.find(({ id }) => id === record.id);
    if (existing !== undefined && !recordMatches(existing, record)) {
      fail('IDEMPOTENCY_CONFLICT', `${label} id was already committed with different content.`);
    }
  }
  return Object.freeze([
    ...persisted,
    ...requested.filter((record) => !persisted.some(({ id }) => id === record.id)),
  ]);
};

const validateTerminalRecords = (command: CompleteAgentSessionRunCommand): void => {
  requireIdentity(command.sessionId, 'Session id');
  requireIdentity(command.runId, 'Run id');
  if (command.run.id !== command.runId || command.run.sessionId !== command.sessionId) {
    fail('LIFECYCLE_INPUT_INVALID', 'Terminal Run identity does not match the completion command.');
  }
  if (command.run.status === 'RUNNING' || command.run.status === 'CANCELLED' || command.run.finishedAt === null) {
    fail('LIFECYCLE_INPUT_INVALID', 'Completion requires a finalized non-cancelled Run.');
  }
  if (command.sessionStatus === 'FAILED' && command.run.status !== 'FAILED') {
    fail('LIFECYCLE_INPUT_INVALID', 'A failed Session requires a failed Run.');
  }
  if (command.sessionStatus !== 'FAILED' && command.run.status !== 'COMPLETED') {
    fail('LIFECYCLE_INPUT_INVALID', 'A completed or waiting Session requires a completed Run.');
  }
  if (command.run.status === 'COMPLETED' && command.run.failureCode !== null) {
    fail('LIFECYCLE_INPUT_INVALID', 'A completed Run cannot carry a failure code.');
  }
  if (command.run.status === 'FAILED'
    && (command.run.failureCode === null || command.run.failureCode.trim().length === 0)) {
    fail('LIFECYCLE_INPUT_INVALID', 'A failed Run requires a failure code.');
  }
  for (const message of command.finalizedMessages) {
    if (message.sessionId !== command.sessionId || message.runId !== command.runId || message.role !== 'ASSISTANT') {
      fail('LIFECYCLE_INPUT_INVALID', 'Finalized Messages must be ASSISTANT Messages from the completed Run.');
    }
    requireIdentity(message.content, 'Finalized Message content');
  }
  for (const execution of command.toolExecutions) {
    if (execution.sessionId !== command.sessionId
      || execution.runId !== command.runId
      || execution.status === 'RUNNING') {
      fail('LIFECYCLE_INPUT_INVALID', 'Tool executions must be finalized records from the completed Run.');
    }
  }
  for (const artifact of command.artifacts) {
    if (artifact.sessionId !== command.sessionId || artifact.runId !== command.runId) {
      fail('LIFECYCLE_INPUT_INVALID', 'Artifacts must belong to the completed Run.');
    }
  }
};

const completionAlreadyCommitted = (
  state: AgentSessionState,
  command: CompleteAgentSessionRunCommand,
): boolean => {
  const run = state.runs.find(({ id }) => id === command.runId);
  if (run === undefined || !recordMatches(run, command.run)) return false;
  if (state.session.status !== command.sessionStatus || state.session.activeRunId !== null) return false;
  return command.finalizedMessages.every((record) => (
    state.messages.some((existing) => existing.id === record.id && recordMatches(existing, record))
  )) && command.toolExecutions.every((record) => (
    state.toolExecutions.some((existing) => existing.id === record.id && recordMatches(existing, record))
  )) && command.artifacts.every((record) => (
    state.artifacts.some((existing) => existing.id === record.id && recordMatches(existing, record))
  ));
};

const completeState = (
  current: AgentSessionState | null,
  command: CompleteAgentSessionRunCommand,
): AgentSessionState => {
  validateTerminalRecords(command);
  const state = requireState(current);
  if (completionAlreadyCommitted(state, command)) return state;
  const finishedAt = command.run.finishedAt
    ?? fail('LIFECYCLE_INPUT_INVALID', 'Completion requires a finished Run.');

  const runIndex = state.runs.findIndex(({ id }) => id === command.runId);
  if (runIndex < 0) fail('RUN_NOT_FOUND', 'Run was not found.');
  const persistedRun = requireRun(state.runs[runIndex], 'Run was not found.');
  if (persistedRun.status !== 'RUNNING'
    || state.session.status !== 'ACTIVE'
    || state.session.activeRunId !== command.runId) {
    fail('RUN_STALE', 'Only the currently active RUNNING Run may complete the Session.');
  }
  if (command.expectedSessionRevision !== state.session.revision) {
    fail('SESSION_REVISION_CONFLICT', 'Session revision changed before the Run could complete.');
  }

  const runs = [...state.runs];
  runs[runIndex] = Object.freeze(command.run);
  return freezeState({
    session: {
      ...state.session,
      status: command.sessionStatus,
      activeRunId: null,
      revision: state.session.revision + 1,
      updatedAt: finishedAt,
    },
    runs,
    messages: appendExactRecords(state.messages, command.finalizedMessages, 'Message'),
    toolExecutions: appendExactRecords(state.toolExecutions, command.toolExecutions, 'ToolExecution'),
    artifacts: appendExactRecords(state.artifacts, command.artifacts, 'Artifact'),
  });
};

const cancelState = (
  current: AgentSessionState | null,
  command: CancelAgentSessionRunCommand,
): AgentSessionState => {
  requireIdentity(command.sessionId, 'Session id');
  requireIdentity(command.runId, 'Run id');
  requireTimestamp(command.at, 'Cancel timestamp');
  const state = requireState(current);
  const runIndex = state.runs.findIndex(({ id }) => id === command.runId);
  if (runIndex < 0) fail('RUN_NOT_FOUND', 'Run was not found.');
  const run = requireRun(state.runs[runIndex], 'Run was not found.');
  if (run.status === 'CANCELLED'
    && run.failureCode === 'RUN_CANCELLED'
    && run.finishedAt === command.at
    && state.session.status === 'CANCELLED') {
    return state;
  }
  if (run.status !== 'RUNNING'
    || state.session.status !== 'ACTIVE'
    || state.session.activeRunId !== command.runId) {
    fail('RUN_STALE', 'Only the currently active RUNNING Run may be cancelled.');
  }
  if (command.expectedSessionRevision !== state.session.revision) {
    fail('SESSION_REVISION_CONFLICT', 'Session revision changed before cancellation.');
  }

  const runs = [...state.runs];
  runs[runIndex] = Object.freeze({
    ...run,
    status: 'CANCELLED',
    finishedAt: command.at,
    failureCode: 'RUN_CANCELLED',
  });
  return freezeState({
    ...state,
    session: {
      ...state.session,
      status: 'CANCELLED',
      activeRunId: null,
      revision: state.session.revision + 1,
      updatedAt: command.at,
    },
    runs,
  });
};

const interruptState = (
  current: AgentSessionState | null,
  command: InterruptAgentSessionRunCommand,
): AgentSessionState => {
  requireIdentity(command.sessionId, 'Session id');
  requireIdentity(command.runId, 'Run id');
  requireTimestamp(command.at, 'Interrupt timestamp');
  const state = requireState(current);

  const runIndex = state.runs.findIndex(({ id }) => id === command.runId);
  if (runIndex < 0) fail('RUN_NOT_FOUND', 'Run was not found.');
  const run = requireRun(state.runs[runIndex], 'Run was not found.');
  if (run.status === 'FAILED' && run.failureCode === 'RUN_INTERRUPTED') return state;
  if (state.session.status !== 'ACTIVE'
    || state.session.activeRunId !== command.runId
    || run.status !== 'RUNNING') {
    fail('RUN_STALE', 'Only the currently active RUNNING Run may be interrupted.');
  }

  const runs = [...state.runs];
  runs[runIndex] = Object.freeze({
    ...run,
    status: 'FAILED',
    finishedAt: command.at,
    failureCode: 'RUN_INTERRUPTED',
  });
  return freezeState({ ...state, runs });
};

export const createAgentSessionLifecycle = ({
  store,
}: CreateAgentSessionLifecycleInput): AgentSessionLifecycle => Object.freeze({
  get(sessionId: string) {
    requireIdentity(sessionId, 'Session id');
    return store.get(sessionId);
  },
  start(command: StartAgentSessionRunCommand) {
    return store.transact(command.session.id, (current) => startState(current, command));
  },
  interrupt(command: InterruptAgentSessionRunCommand) {
    return store.transact(command.sessionId, (current) => interruptState(current, command));
  },
  complete(command: CompleteAgentSessionRunCommand) {
    return store.transact(command.sessionId, (current) => completeState(current, command));
  },
  cancel(command: CancelAgentSessionRunCommand) {
    return store.transact(command.sessionId, (current) => cancelState(current, command));
  },
});

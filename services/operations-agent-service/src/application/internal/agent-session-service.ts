import type {
  AgentEngine,
  AgentEvent,
  AgentEventSink,
  AgentModelRef,
  AgentRun,
  AgentRunBudget,
  AgentRunContext,
  AgentTool,
} from '../../agent/index.js';
import type { AuthorizationDecision } from './ports.js';
import {
  HVAC_AGENT_EVENT_VERSION,
} from '../../agent/index.js';
import {
  AgentSessionLifecycleError,
  type AgentSessionLifecycle,
  type AgentSessionState,
} from './agent-session-lifecycle.js';

export interface AgentSessionAccessContext {
  readonly tenantId: string;
  readonly siteId: string;
  readonly principalId: string;
  readonly capabilities: readonly string[];
  readonly correlationId: string;
  readonly authorization: AuthorizationDecision;
}

export interface AgentSessionServiceCreateInput {
  readonly message: string;
}

export interface AgentSessionServiceStartInput {
  readonly sessionId: string;
  readonly expectedRevision: number;
  readonly message: string;
}

export interface AgentSessionServiceCancelInput {
  readonly sessionId: string;
  readonly expectedRevision: number;
}

export interface AgentSessionServiceInputResponse {
  readonly sessionId: string;
  readonly expectedRevision: number;
  readonly requestArtifactId: string;
  readonly value: string;
}

export type AgentSessionEvent = AgentEvent;
export type AgentSessionEventListener = (event: AgentSessionEvent) => void;

export type AgentSessionServiceErrorCode =
  | 'SESSION_NOT_FOUND'
  | 'SESSION_SCOPE_MISMATCH'
  | 'SESSION_INPUT_INVALID';

export class AgentSessionServiceError extends Error {
  readonly code: AgentSessionServiceErrorCode;

  constructor(code: AgentSessionServiceErrorCode, message: string) {
    super(message);
    this.name = 'AgentSessionServiceError';
    this.code = code;
  }
}

export interface AgentSessionService {
  create(context: AgentSessionAccessContext, input: AgentSessionServiceCreateInput): Promise<AgentSessionState>;
  list(context: AgentSessionAccessContext): Promise<readonly AgentSessionState[]>;
  get(context: AgentSessionAccessContext, sessionId: string): Promise<AgentSessionState>;
  start(context: AgentSessionAccessContext, input: AgentSessionServiceStartInput): Promise<AgentSessionState>;
  cancel(context: AgentSessionAccessContext, input: AgentSessionServiceCancelInput): Promise<AgentSessionState>;
  submitInput(context: AgentSessionAccessContext, input: AgentSessionServiceInputResponse): Promise<AgentSessionState>;
  subscribe(
    context: AgentSessionAccessContext,
    sessionId: string,
    listener: AgentSessionEventListener,
  ): Promise<() => void>;
}

export interface CreateAgentSessionServiceInput {
  readonly lifecycle: AgentSessionLifecycle;
  readonly engine: AgentEngine;
  readonly modelRef: AgentModelRef;
  readonly createTools: (context: AgentSessionAccessContext) => readonly AgentTool[];
  readonly budget: AgentRunBudget;
  readonly now: () => number;
  readonly nextId: (kind: 'session' | 'run' | 'message' | 'artifact') => string;
  readonly agentDefinitionId?: string;
}

interface LiveRun {
  readonly runId: string;
  readonly controller: AbortController;
  readonly events: AgentEvent[];
  readonly listeners: Set<AgentSessionEventListener>;
  settled: boolean;
}

const DEFAULT_AGENT_DEFINITION_ID = 'operations-investigation.v1';
const MAXIMUM_MESSAGE_LENGTH = 4_000;
const MAXIMUM_INPUT_LENGTH = 4_000;
const MAXIMUM_LISTED_SESSIONS = 50;

const requireIdentity = (value: string, label: string): string => {
  const normalized = value.trim();
  if (normalized.length === 0 || normalized.length > 256) {
    throw new AgentSessionServiceError('SESSION_INPUT_INVALID', `${label} is invalid.`);
  }
  return normalized;
};

const requireMessage = (value: string): string => {
  const normalized = value.trim();
  if (normalized.length === 0 || normalized.length > MAXIMUM_MESSAGE_LENGTH) {
    throw new AgentSessionServiceError('SESSION_INPUT_INVALID', 'Operator message is invalid.');
  }
  return normalized;
};

const requireRevision = (value: number): number => {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new AgentSessionServiceError('SESSION_INPUT_INVALID', 'Session revision is invalid.');
  }
  return value;
};

const requireContext = (context: AgentSessionAccessContext): AgentSessionAccessContext => {
  if (context.authorization.decision !== 'ALLOW') {
    throw new AgentSessionServiceError('SESSION_INPUT_INVALID', 'Agent Session requires an allowed authorization decision.');
  }
  return Object.freeze({
    tenantId: requireIdentity(context.tenantId, 'Tenant id'),
    siteId: requireIdentity(context.siteId, 'Site id'),
    principalId: requireIdentity(context.principalId, 'Principal id'),
    capabilities: Object.freeze([...context.capabilities]),
    correlationId: requireIdentity(context.correlationId, 'Correlation id'),
    authorization: Object.freeze({
      ...context.authorization,
      ...(context.authorization.capabilities === undefined
        ? {}
        : { capabilities: Object.freeze([...context.authorization.capabilities]) }),
      ...(context.authorization.toolDelegationGrants === undefined
        ? {}
        : { toolDelegationGrants: Object.freeze({ ...context.authorization.toolDelegationGrants }) }),
    }),
  });
};

const requireScopedState = (
  state: AgentSessionState | null,
  context: AgentSessionAccessContext,
): AgentSessionState => {
  if (state === null) {
    throw new AgentSessionServiceError('SESSION_NOT_FOUND', 'Agent Session was not found.');
  }
  if (state.session.tenantId !== context.tenantId || state.session.siteId !== context.siteId) {
    throw new AgentSessionServiceError('SESSION_SCOPE_MISMATCH', 'Agent Session was not found in this Site scope.');
  }
  return state;
};

const snapshotEvent = (state: AgentSessionState, at: number): AgentEvent => Object.freeze({
  version: HVAC_AGENT_EVENT_VERSION,
  type: 'session.snapshot',
  sessionId: state.session.id,
  runId: state.session.activeRunId,
  sequence: 0,
  at,
  payload: Object.freeze({ snapshot: state }),
});

export const createAgentSessionService = ({
  lifecycle,
  engine,
  modelRef,
  createTools,
  budget,
  now,
  nextId,
  agentDefinitionId = DEFAULT_AGENT_DEFINITION_ID,
}: CreateAgentSessionServiceInput): AgentSessionService => {
  const liveRuns = new Map<string, LiveRun>();

  const publish = (sessionId: string, event: AgentEvent): void => {
    const live = liveRuns.get(sessionId);
    if (live === undefined || live.runId !== event.runId) return;
    live.events.push(event);
    for (const listener of live.listeners) listener(event);
  };

  const finishRun = async (
    context: AgentSessionAccessContext,
    started: AgentSessionState,
    run: AgentRun,
    controller: AbortController,
  ): Promise<void> => {
    const result = await engine({
      session: started.session,
      run,
      messages: started.messages,
      tools: createTools(context),
      context: Object.freeze({
        tenantId: context.tenantId,
        siteId: context.siteId,
        principalId: context.principalId,
        capabilities: context.capabilities,
        sessionId: started.session.id,
        runId: run.id,
        correlationId: context.correlationId,
      } satisfies AgentRunContext),
      budget,
      signal: controller.signal,
      emit: ((event) => publish(started.session.id, event)) satisfies AgentEventSink,
    });

    if (result.runStatus === 'CANCELLED') return;
    const terminalRun: AgentRun = Object.freeze({
      ...run,
      status: result.runStatus,
      finishedAt: now(),
      usage: result.usage,
      failureCode: result.failureCode,
    });
    try {
      const terminal = await lifecycle.complete({
        sessionId: started.session.id,
        runId: run.id,
        expectedSessionRevision: started.session.revision,
        run: terminalRun,
        sessionStatus: result.sessionStatus === 'WAITING_FOR_INPUT'
          ? 'WAITING_FOR_INPUT'
          : result.runStatus === 'FAILED' ? 'FAILED' : 'COMPLETED',
        finalizedMessages: result.finalizedMessages,
        toolExecutions: result.toolExecutions,
        artifacts: result.artifacts,
      });
      const live = liveRuns.get(started.session.id);
      if (live !== undefined && live.runId === run.id) {
        const event = snapshotEvent(terminal, now());
        live.events.push(event);
        for (const listener of live.listeners) listener(event);
      }
    } catch (error) {
      if (!(error instanceof AgentSessionLifecycleError && error.code === 'RUN_STALE')) throw error;
    }
  };

  const launch = (
    context: AgentSessionAccessContext,
    started: AgentSessionState,
    run: AgentRun,
  ): void => {
    const controller = new AbortController();
    const live: LiveRun = {
      runId: run.id,
      controller,
      events: [],
      listeners: new Set(),
      settled: false,
    };
    liveRuns.set(started.session.id, live);
    void finishRun(context, started, run, controller).finally(() => {
      live.settled = true;
      const current = liveRuns.get(started.session.id);
      if (current === live && current.listeners.size === 0) liveRuns.delete(started.session.id);
    });
  };

  const createRunningRun = (sessionId: string): AgentRun => Object.freeze({
    id: nextId('run'),
    sessionId,
    modelRef,
    status: 'RUNNING',
    startedAt: now(),
    finishedAt: null,
    usage: Object.freeze({ inputTokens: 0, outputTokens: 0, modelCalls: 0, toolCalls: 0 }),
    failureCode: null,
  });

  const createOperatorMessage = (sessionId: string, content: string) => Object.freeze({
    id: nextId('message'),
    sessionId,
    runId: null,
    role: 'OPERATOR' as const,
    content,
    createdAt: now(),
  });

  const scopedGet = async (
    context: AgentSessionAccessContext,
    sessionId: string,
  ): Promise<AgentSessionState> => requireScopedState(
    await lifecycle.get(requireIdentity(sessionId, 'Session id')),
    context,
  );

  return Object.freeze({
    async create(rawContext: AgentSessionAccessContext, input: AgentSessionServiceCreateInput) {
      const context = requireContext(rawContext);
      const message = requireMessage(input.message);
      const sessionId = nextId('session');
      const run = createRunningRun(sessionId);
      const started = await lifecycle.start({
        session: Object.freeze({
          id: sessionId,
          tenantId: context.tenantId,
          siteId: context.siteId,
          agentDefinitionId,
          createdBy: context.principalId,
          createdAt: now(),
        }),
        expectedSessionRevision: null,
        run,
        operatorMessage: createOperatorMessage(sessionId, message),
      });
      launch(context, started, run);
      return started;
    },

    async list(rawContext: AgentSessionAccessContext) {
      const context = requireContext(rawContext);
      return Object.freeze((await lifecycle.list(context.tenantId, context.siteId)).slice(0, MAXIMUM_LISTED_SESSIONS));
    },

    async get(rawContext: AgentSessionAccessContext, sessionId: string) {
      return scopedGet(requireContext(rawContext), sessionId);
    },

    async start(rawContext: AgentSessionAccessContext, input: AgentSessionServiceStartInput) {
      const context = requireContext(rawContext);
      const state = await scopedGet(context, input.sessionId);
      requireRevision(input.expectedRevision);
      const message = requireMessage(input.message);
      const run = createRunningRun(state.session.id);
      const started = await lifecycle.start({
        session: Object.freeze({
          id: state.session.id,
          tenantId: state.session.tenantId,
          siteId: state.session.siteId,
          agentDefinitionId: state.session.agentDefinitionId,
          createdBy: state.session.createdBy,
          createdAt: state.session.createdAt,
        }),
        expectedSessionRevision: input.expectedRevision,
        run,
        operatorMessage: createOperatorMessage(state.session.id, message),
      });
      launch(context, started, run);
      return started;
    },

    async cancel(rawContext: AgentSessionAccessContext, input: AgentSessionServiceCancelInput) {
      const context = requireContext(rawContext);
      const state = await scopedGet(context, input.sessionId);
      requireRevision(input.expectedRevision);
      if (state.session.status !== 'ACTIVE') {
        throw new AgentSessionServiceError('SESSION_INPUT_INVALID', 'Only an active Agent Run can be cancelled.');
      }
      const cancelled = await lifecycle.cancel({
        sessionId: state.session.id,
        runId: state.session.activeRunId,
        expectedSessionRevision: input.expectedRevision,
        at: now(),
      });
      liveRuns.get(state.session.id)?.controller.abort();
      const live = liveRuns.get(state.session.id);
      if (live !== undefined) {
        const event = snapshotEvent(cancelled, now());
        live.events.push(event);
        for (const listener of live.listeners) listener(event);
      }
      return cancelled;
    },

    async submitInput(rawContext: AgentSessionAccessContext, input: AgentSessionServiceInputResponse) {
      const context = requireContext(rawContext);
      const state = await scopedGet(context, input.sessionId);
      requireRevision(input.expectedRevision);
      const requestArtifactId = requireIdentity(input.requestArtifactId, 'Input request Artifact id');
      const value = input.value.trim();
      if (value.length === 0 || value.length > MAXIMUM_INPUT_LENGTH) {
        throw new AgentSessionServiceError('SESSION_INPUT_INVALID', 'Operator Input value is invalid.');
      }
      const request = state.artifacts.find((artifact) => (
        artifact.kind === 'INPUT_REQUEST' && artifact.id === requestArtifactId
      ));
      if (request?.kind !== 'INPUT_REQUEST') {
        throw new AgentSessionServiceError('SESSION_INPUT_INVALID', 'Input request Artifact was not found.');
      }
      const displayValue = request.request.response.kind === 'SINGLE_SELECT'
        ? request.request.response.choices.find((choice) => choice.value === value)?.label ?? value
        : value;
      const run = createRunningRun(state.session.id);
      const continued = await lifecycle.continueWithInput({
        sessionId: state.session.id,
        expectedSessionRevision: input.expectedRevision,
        run,
        operatorMessage: createOperatorMessage(state.session.id, displayValue),
        inputResponse: Object.freeze({
          id: nextId('artifact'),
          sessionId: state.session.id,
          runId: run.id,
          kind: 'INPUT_RESPONSE',
          requestArtifactId,
          value,
          submittedBy: context.principalId,
          createdAt: now(),
        }),
      });
      launch(context, continued, run);
      return continued;
    },

    async subscribe(
      rawContext: AgentSessionAccessContext,
      sessionId: string,
      listener: AgentSessionEventListener,
    ) {
      const context = requireContext(rawContext);
      const state = await scopedGet(context, sessionId);
      listener(snapshotEvent(state, now()));
      const live = liveRuns.get(state.session.id);
      if (live === undefined || state.session.activeRunId !== live.runId) {
        const refreshed = await scopedGet(context, state.session.id);
        if (refreshed.session.revision !== state.session.revision) {
          listener(snapshotEvent(refreshed, now()));
        }
        return () => undefined;
      }
      for (const event of live.events) listener(event);
      live.listeners.add(listener);
      return () => {
        live.listeners.delete(listener);
        if (live.listeners.size === 0) {
          const current = liveRuns.get(state.session.id);
          if (current === live && (current.settled || current.controller.signal.aborted)) {
            liveRuns.delete(state.session.id);
          }
        }
      };
    },
  });
};

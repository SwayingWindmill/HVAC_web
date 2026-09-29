import { createHash } from 'node:crypto';

import {
  Agent,
  type AgentEvent as PiAgentEvent,
  type AgentMessage as PiAgentMessage,
} from '@earendil-works/pi-agent-core';
import type { Model, ModelThinkingLevel } from '@earendil-works/pi-ai';

import {
  HVAC_AGENT_EVENT_VERSION,
  NOOP_AGENT_RUNTIME_TELEMETRY,
  type AgentArtifact,
  type AgentEngine,
  type AgentRuntimeBudgetDimension,
  type AgentRuntimeOwnerErrorClass,
  type AgentRuntimeTelemetrySink,
  type AgentEngineResult,
  type AgentEvent,
  type AgentMessage,
  type AgentRun,
  type AgentRunStatus,
  type AgentRunUsage,
  type AgentToolExecution,
} from '../../agent/index.js';
import {
  createPiTools,
  projectToolFailureCodeFromPiResult,
} from './pi-tools.js';

type PiStreamFn = ConstructorParameters<typeof Agent>[0]['streamFn'];

export interface PiAgentEngineDependencies {
  readonly model: Model<any>;
  readonly streamFn: PiStreamFn;
  readonly systemPrompt: string;
  readonly thinkingLevel: ModelThinkingLevel;
  readonly telemetry?: AgentRuntimeTelemetrySink;
}

interface PendingToolExecution {
  readonly id: string;
  readonly toolName: string;
  readonly argumentsDigest: string;
  readonly startedAt: number;
}

const digestArguments = (argumentsValue: unknown): string => createHash('sha256')
  .update(JSON.stringify(argumentsValue))
  .digest('hex');

const safeRecordTelemetry = (
  telemetry: AgentRuntimeTelemetrySink,
  event: Parameters<AgentRuntimeTelemetrySink['record']>[0],
): void => {
  try {
    telemetry.record(event);
  } catch {
    // Runtime telemetry is diagnostic only and cannot change Agent execution.
  }
};

const budgetDimensionFromFailureCode = (failureCode: string): AgentRuntimeBudgetDimension | null => {
  switch (failureCode) {
    case 'MODEL_CALL_LIMIT':
      return 'MODEL_CALLS';
    case 'TOOL_CALL_LIMIT':
      return 'TOOL_CALLS';
    case 'TOOL_CONCURRENCY_LIMIT':
      return 'TOOL_CONCURRENCY';
    case 'TOOL_RESULT_TOO_LARGE':
      return 'TOOL_RESULT_BYTES';
    case 'WALL_CLOCK_LIMIT':
      return 'WALL_CLOCK_MS';
    case 'INPUT_TOKEN_LIMIT':
      return 'INPUT_TOKENS';
    case 'OUTPUT_TOKEN_LIMIT':
      return 'OUTPUT_TOKENS';
    default:
      return null;
  }
};

const ownerErrorClassFromFailureCode = (failureCode: string | null): AgentRuntimeOwnerErrorClass | null => {
  switch (failureCode) {
    case 'TOOL_OWNER_REQUEST_REJECTED':
      return 'REQUEST_REJECTED';
    case 'TOOL_OWNER_RESOURCE_NOT_FOUND':
      return 'RESOURCE_NOT_FOUND';
    case 'TOOL_OWNER_TIMEOUT':
      return 'TIMEOUT';
    case 'TOOL_OWNER_UNAVAILABLE':
      return 'UNAVAILABLE';
    case 'TOOL_OWNER_RESPONSE_INVALID':
      return 'RESPONSE_INVALID';
    default:
      return null;
  }
};

const textFromAssistantMessage = (message: Extract<PiAgentEvent, { type: 'message_end' }>['message']): string => {
  if (message.role !== 'assistant') return '';
  return message.content
    .filter((block) => block.type === 'text')
    .map((block) => block.text)
    .join('\n');
};

const EMPTY_PI_USAGE = Object.freeze({
  input: 0,
  output: 0,
  cacheRead: 0,
  cacheWrite: 0,
  totalTokens: 0,
  cost: Object.freeze({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 }),
});

const restoreCommittedPiMessages = (
  messages: readonly AgentMessage[],
  model: Model<any>,
): PiAgentMessage[] => messages.map((message): PiAgentMessage => {
  if (message.role === 'OPERATOR') {
    return {
      role: 'user',
      content: message.content,
      timestamp: message.createdAt,
    };
  }
  return {
    role: 'assistant',
    content: [{ type: 'text', text: message.content }],
    api: model.api,
    provider: model.provider,
    model: model.id,
    usage: EMPTY_PI_USAGE,
    stopReason: 'stop',
    timestamp: message.createdAt,
  };
});

const terminalRun = (
  run: AgentRun,
  status: AgentRunStatus,
  usage: AgentRunUsage,
  failureCode: string | null,
): AgentRun => Object.freeze({
  ...run,
  status,
  usage,
  finishedAt: Date.now(),
  failureCode,
});

export const createPiAgentEngine = ({
  model,
  streamFn,
  systemPrompt,
  thinkingLevel,
  telemetry = NOOP_AGENT_RUNTIME_TELEMETRY,
}: PiAgentEngineDependencies): AgentEngine => async (input) => {
  const artifacts: AgentArtifact[] = [];
  const emittedArtifactIds = new Set<string>();
  const toolExecutions: AgentToolExecution[] = [];
  const pendingTools = new Map<string, PendingToolExecution>();
  const finalizedMessages: AgentMessage[] = [];
  let sequence = 0;
  let assistantMessageIndex = 0;
  let activeAssistantMessageId: string | null = null;
  let modelCalls = 0;
  let toolCalls = 0;
  let inputTokens = 0;
  let outputTokens = 0;
  let budgetFailureCode: string | null = null;
  let activeModelStartedAt: number | null = null;
  let activeModelCall = 0;
  const runStartedAt = Date.now();

  const usage = (): AgentRunUsage => Object.freeze({
    inputTokens,
    outputTokens,
    modelCalls,
    toolCalls,
  });

  const markBudgetFailure = (failureCode: string): void => {
    if (budgetFailureCode !== null) return;
    budgetFailureCode = failureCode;
    const dimension = budgetDimensionFromFailureCode(failureCode);
    if (dimension === null) return;
    safeRecordTelemetry(telemetry, {
      type: 'budget.exhausted',
      sessionId: input.session.id,
      runId: input.run.id,
      correlationId: input.context.correlationId,
      at: Date.now(),
      dimension,
      failureCode,
    });
  };

  const recordRunCompleted = (
    status: AgentEngineResult['runStatus'],
    failureCode: string | null,
  ): void => {
    const at = Date.now();
    safeRecordTelemetry(telemetry, {
      type: 'run.completed',
      sessionId: input.session.id,
      runId: input.run.id,
      correlationId: input.context.correlationId,
      at,
      status,
      durationMs: Math.max(0, at - runStartedAt),
      usage: usage(),
      failureCode,
    });
  };

  safeRecordTelemetry(telemetry, {
    type: 'run.started',
    sessionId: input.session.id,
    runId: input.run.id,
    correlationId: input.context.correlationId,
    at: runStartedAt,
    modelRef: input.run.modelRef,
  });

  const emit = <TType extends AgentEvent['type']>(
    type: TType,
    payload: Extract<AgentEvent, { type: TType }>['payload'],
  ): void => {
    input.emit({
      version: HVAC_AGENT_EVENT_VERSION,
      type,
      sessionId: input.session.id,
      runId: input.run.id,
      sequence,
      at: Date.now(),
      payload,
    } as Extract<AgentEvent, { type: TType }>);
    sequence += 1;
  };

  const allowedTools = input.tools.filter((tool) => (
    tool.definition.requiredCapabilities.every((capability) => (
      input.context.capabilities.includes(capability)
    ))
  ));
  const piTools = createPiTools({
    tools: allowedTools,
    context: input.context,
    budget: input.budget,
    runSignal: input.signal,
    sessionId: input.session.id,
    runId: input.run.id,
    onArtifact: (artifact) => artifacts.push(artifact),
    onBudgetExhausted: (code) => markBudgetFailure(code),
  });

  const agent = new Agent({
    initialState: {
      systemPrompt,
      model,
      thinkingLevel,
      tools: piTools,
      messages: restoreCommittedPiMessages(input.messages.slice(0, -1), model),
    },
    streamFn,
    sessionId: input.session.id,
    toolExecution: 'parallel',
    shouldStopAfterTurn: () => {
      if (artifacts.length > 0) return true;
      if (budgetFailureCode !== null) return true;
      if (inputTokens >= input.budget.maxInputTokens) {
        markBudgetFailure('INPUT_TOKEN_LIMIT');
        return true;
      }
      if (outputTokens >= input.budget.maxOutputTokens) {
        markBudgetFailure('OUTPUT_TOKEN_LIMIT');
        return true;
      }
      if (modelCalls >= input.budget.maxModelCalls) {
        markBudgetFailure('MODEL_CALL_LIMIT');
        return true;
      }
      return false;
    },
  });

  const abortAgent = (): void => agent.abort();
  input.signal.addEventListener('abort', abortAgent, { once: true });

  const unsubscribeAgent = agent.subscribe((event) => {
    switch (event.type) {
      case 'turn_start': {
        modelCalls += 1;
        activeModelCall = modelCalls;
        activeModelStartedAt = Date.now();
        safeRecordTelemetry(telemetry, {
          type: 'model.started',
          sessionId: input.session.id,
          runId: input.run.id,
          correlationId: input.context.correlationId,
          at: activeModelStartedAt,
          modelRef: input.run.modelRef,
          modelCall: activeModelCall,
        });
        break;
      }
      case 'message_start':
        if (event.message.role === 'assistant') {
          activeAssistantMessageId = `message-${input.run.id}-${assistantMessageIndex}`;
          assistantMessageIndex += 1;
        }
        break;
      case 'message_update':
        if (event.assistantMessageEvent.type === 'text_delta' && activeAssistantMessageId !== null) {
          emit('assistant.delta', {
            messageId: activeAssistantMessageId,
            delta: event.assistantMessageEvent.delta,
          });
        }
        break;
      case 'message_end':
        if (event.message.role === 'assistant') {
          inputTokens += event.message.usage.input;
          outputTokens += event.message.usage.output;
          const completedAt = Date.now();
          const turnUsage = event.message.usage;
          if (activeModelStartedAt !== null) {
            safeRecordTelemetry(telemetry, {
              type: 'model.completed',
              sessionId: input.session.id,
              runId: input.run.id,
              correlationId: input.context.correlationId,
              at: completedAt,
              modelRef: input.run.modelRef,
              modelCall: activeModelCall,
              durationMs: Math.max(0, completedAt - activeModelStartedAt),
              inputTokens: turnUsage.input,
              outputTokens: turnUsage.output,
            });
            activeModelStartedAt = null;
          }
          if (inputTokens >= input.budget.maxInputTokens) markBudgetFailure('INPUT_TOKEN_LIMIT');
          if (outputTokens >= input.budget.maxOutputTokens) markBudgetFailure('OUTPUT_TOKEN_LIMIT');
          if (!input.signal.aborted && activeAssistantMessageId !== null) {
            const content = textFromAssistantMessage(event.message);
            if (content.length > 0) {
              finalizedMessages.push(Object.freeze({
                id: activeAssistantMessageId,
                sessionId: input.session.id,
                runId: input.run.id,
                role: 'ASSISTANT',
                content,
                createdAt: event.message.timestamp,
              }));
            }
          }
          activeAssistantMessageId = null;
        }
        break;
      case 'tool_execution_start': {
        toolCalls += 1;
        const pending = Object.freeze({
          id: event.toolCallId,
          toolName: event.toolName,
          argumentsDigest: digestArguments(event.args),
          startedAt: Date.now(),
        });
        pendingTools.set(event.toolCallId, pending);
        safeRecordTelemetry(telemetry, {
          type: 'tool.started',
          sessionId: input.session.id,
          runId: input.run.id,
          correlationId: input.context.correlationId,
          at: pending.startedAt,
          toolExecutionId: event.toolCallId,
          toolName: event.toolName,
          activeToolCalls: pendingTools.size,
        });
        emit('tool.started', {
          toolExecutionId: event.toolCallId,
          toolName: event.toolName,
        });
        break;
      }
      case 'tool_execution_end': {
        const pending = pendingTools.get(event.toolCallId);
        if (pending === undefined) break;
        pendingTools.delete(event.toolCallId);
        const terminalArtifact = artifacts.find(({ id }) => id === `artifact-${event.toolCallId}`);
        const provenance = terminalArtifact?.kind === 'FINDING'
          ? terminalArtifact.finding.evidenceRefs
          : [];
        const finishedAt = Date.now();
        const failureCode = event.isError
          ? projectToolFailureCodeFromPiResult(event.result) ?? 'TOOL_EXECUTION_FAILED'
          : null;
        const execution = Object.freeze({
          id: pending.id,
          sessionId: input.session.id,
          runId: input.run.id,
          toolName: pending.toolName,
          argumentsDigest: pending.argumentsDigest,
          status: event.isError ? 'FAILED' : 'COMPLETED',
          startedAt: pending.startedAt,
          finishedAt,
          resultSummary: event.isError ? null : 'Tool completed.',
          provenance,
          failureCode,
        } as const satisfies AgentToolExecution);
        toolExecutions.push(execution);
        safeRecordTelemetry(telemetry, {
          type: 'tool.completed',
          sessionId: input.session.id,
          runId: input.run.id,
          correlationId: input.context.correlationId,
          at: finishedAt,
          toolExecutionId: pending.id,
          toolName: pending.toolName,
          status: execution.status,
          durationMs: Math.max(0, finishedAt - pending.startedAt),
          failureCode,
          ownerErrorClass: ownerErrorClassFromFailureCode(failureCode),
        });
        emit('tool.completed', { toolExecution: execution });
        if (terminalArtifact !== undefined && !emittedArtifactIds.has(terminalArtifact.id)) {
          emittedArtifactIds.add(terminalArtifact.id);
          emit('artifact.created', { artifact: terminalArtifact });
          if (terminalArtifact.kind === 'INPUT_REQUEST') {
            emit('input.required', { artifact: terminalArtifact });
          }
        }
        break;
      }
      default:
        break;
    }
  });

  emit('run.started', { run: input.run });

  const prompt = input.messages.at(-1);
  if (prompt === undefined || prompt.role !== 'OPERATOR') {
    input.signal.removeEventListener('abort', abortAgent);
    const failedRun = terminalRun(input.run, 'FAILED', usage(), 'OPERATOR_PROMPT_REQUIRED');
    emit('run.failed', { run: failedRun });
    recordRunCompleted('FAILED', 'OPERATOR_PROMPT_REQUIRED');
    return Object.freeze({
      runStatus: 'FAILED',
      sessionStatus: 'FAILED',
      failureCode: 'OPERATOR_PROMPT_REQUIRED',
      usage: usage(),
      finalizedMessages,
      toolExecutions,
      artifacts,
    });
  }

  let wallClockTimer: ReturnType<typeof setTimeout> | undefined;
  const wallClockExpired = new Promise<void>((resolve) => {
    wallClockTimer = setTimeout(() => {
      markBudgetFailure('WALL_CLOCK_LIMIT');
      agent.abort();
      resolve();
    }, input.budget.maxWallClockMs);
  });

  try {
    await Promise.race([
      agent.prompt(prompt.content),
      wallClockExpired,
    ]);
  } catch {
    if (!input.signal.aborted && budgetFailureCode === null) {
      const failedRun = terminalRun(input.run, 'FAILED', usage(), 'PI_RUNTIME_FAILED');
      emit('run.failed', { run: failedRun });
      recordRunCompleted('FAILED', 'PI_RUNTIME_FAILED');
      return Object.freeze({
        runStatus: 'FAILED',
        sessionStatus: 'FAILED',
        failureCode: 'PI_RUNTIME_FAILED',
        usage: usage(),
        finalizedMessages,
        toolExecutions,
        artifacts,
      });
    }
  } finally {
    if (wallClockTimer !== undefined) clearTimeout(wallClockTimer);
    input.signal.removeEventListener('abort', abortAgent);
    unsubscribeAgent();
  }

  if (input.signal.aborted) {
    const cancelledRun = terminalRun(input.run, 'CANCELLED', usage(), 'RUN_CANCELLED');
    emit('run.failed', { run: cancelledRun });
    recordRunCompleted('CANCELLED', 'RUN_CANCELLED');
    return Object.freeze({
      runStatus: 'CANCELLED',
      sessionStatus: 'CANCELLED',
      failureCode: 'RUN_CANCELLED',
      usage: usage(),
      finalizedMessages: [],
      toolExecutions,
      artifacts,
    });
  }

  const terminalArtifact = artifacts.at(-1);
  if (terminalArtifact === undefined && budgetFailureCode !== null) {
    const failedRun = terminalRun(input.run, 'FAILED', usage(), budgetFailureCode);
    emit('run.failed', { run: failedRun });
    recordRunCompleted('FAILED', budgetFailureCode);
    return Object.freeze({
      runStatus: 'FAILED',
      sessionStatus: 'FAILED',
      failureCode: budgetFailureCode,
      usage: usage(),
      finalizedMessages,
      toolExecutions,
      artifacts,
    });
  }
  if (terminalArtifact?.kind === 'FINDING' || terminalArtifact?.kind === 'INPUT_REQUEST') {
    const completedRun = terminalRun(input.run, 'COMPLETED', usage(), null);
    emit('run.completed', { run: completedRun });
    recordRunCompleted('COMPLETED', null);
    return Object.freeze({
      runStatus: 'COMPLETED',
      sessionStatus: terminalArtifact.kind === 'INPUT_REQUEST' ? 'WAITING_FOR_INPUT' : 'COMPLETED',
      failureCode: null,
      usage: usage(),
      finalizedMessages,
      toolExecutions,
      artifacts,
    });
  }

  const failedRun = terminalRun(input.run, 'FAILED', usage(), 'TERMINAL_ARTIFACT_REQUIRED');
  emit('run.failed', { run: failedRun });
  recordRunCompleted('FAILED', 'TERMINAL_ARTIFACT_REQUIRED');
  return Object.freeze({
    runStatus: 'FAILED',
    sessionStatus: 'FAILED',
    failureCode: 'TERMINAL_ARTIFACT_REQUIRED',
    usage: usage(),
    finalizedMessages,
    toolExecutions,
    artifacts,
  } satisfies AgentEngineResult);
};

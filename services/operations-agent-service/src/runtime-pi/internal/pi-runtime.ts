import { createHash } from 'node:crypto';

import {
  Agent,
  type AgentEvent as PiAgentEvent,
} from '@earendil-works/pi-agent-core';
import type { Model } from '@earendil-works/pi-ai';

import {
  HVAC_AGENT_EVENT_VERSION,
  type AgentArtifact,
  type AgentEngine,
  type AgentEngineResult,
  type AgentEvent,
  type AgentMessage,
  type AgentRun,
  type AgentRunStatus,
  type AgentRunUsage,
  type AgentToolExecution,
} from '../../agent/index.js';
import { createPiTools } from './pi-tools.js';

type PiStreamFn = ConstructorParameters<typeof Agent>[0]['streamFn'];

export interface PiAgentEngineDependencies {
  readonly model: Model<any>;
  readonly streamFn: PiStreamFn;
  readonly systemPrompt: string;
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

const textFromAssistantMessage = (message: Extract<PiAgentEvent, { type: 'message_end' }>['message']): string => {
  if (message.role !== 'assistant') return '';
  return message.content
    .filter((block) => block.type === 'text')
    .map((block) => block.text)
    .join('\n');
};

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

  const usage = (): AgentRunUsage => Object.freeze({
    inputTokens,
    outputTokens,
    modelCalls,
    toolCalls,
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

  const piTools = createPiTools({
    tools: input.tools,
    context: input.context,
    runSignal: input.signal,
    sessionId: input.session.id,
    runId: input.run.id,
    onArtifact: (artifact) => artifacts.push(artifact),
  });

  const agent = new Agent({
    initialState: {
      systemPrompt,
      model,
      thinkingLevel: 'off',
      tools: piTools,
    },
    streamFn,
    sessionId: input.session.id,
    toolExecution: 'parallel',
  });

  const abortAgent = (): void => agent.abort();
  input.signal.addEventListener('abort', abortAgent, { once: true });

  agent.subscribe((event) => {
    switch (event.type) {
      case 'turn_start':
        modelCalls += 1;
        break;
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
        const execution = Object.freeze({
          id: pending.id,
          sessionId: input.session.id,
          runId: input.run.id,
          toolName: pending.toolName,
          argumentsDigest: pending.argumentsDigest,
          status: event.isError ? 'FAILED' : 'COMPLETED',
          startedAt: pending.startedAt,
          finishedAt: Date.now(),
          resultSummary: event.isError ? null : 'Tool completed.',
          provenance,
          failureCode: event.isError ? 'TOOL_EXECUTION_FAILED' : null,
        } as const satisfies AgentToolExecution);
        toolExecutions.push(execution);
        emit('tool.completed', { toolExecution: execution });
        if (terminalArtifact !== undefined && !emittedArtifactIds.has(terminalArtifact.id)) {
          emittedArtifactIds.add(terminalArtifact.id);
          emit('artifact.created', { artifact: terminalArtifact });
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
    return Object.freeze({
      runStatus: 'FAILED',
      sessionStatus: 'FAILED',
      usage: usage(),
      finalizedMessages,
      toolExecutions,
      artifacts,
    });
  }

  try {
    await agent.prompt(prompt.content);
  } catch {
    if (!input.signal.aborted) {
      input.signal.removeEventListener('abort', abortAgent);
      const failedRun = terminalRun(input.run, 'FAILED', usage(), 'PI_RUNTIME_FAILED');
      emit('run.failed', { run: failedRun });
      return Object.freeze({
        runStatus: 'FAILED',
        sessionStatus: 'FAILED',
        usage: usage(),
        finalizedMessages,
        toolExecutions,
        artifacts,
      });
    }
  } finally {
    input.signal.removeEventListener('abort', abortAgent);
  }

  if (input.signal.aborted) {
    const cancelledRun = terminalRun(input.run, 'CANCELLED', usage(), 'RUN_CANCELLED');
    emit('run.failed', { run: cancelledRun });
    return Object.freeze({
      runStatus: 'CANCELLED',
      sessionStatus: 'CANCELLED',
      usage: usage(),
      finalizedMessages: [],
      toolExecutions,
      artifacts,
    });
  }

  const terminalArtifact = artifacts.at(-1);
  if (terminalArtifact?.kind === 'FINDING') {
    const completedRun = terminalRun(input.run, 'COMPLETED', usage(), null);
    emit('run.completed', { run: completedRun });
    return Object.freeze({
      runStatus: 'COMPLETED',
      sessionStatus: 'COMPLETED',
      usage: usage(),
      finalizedMessages,
      toolExecutions,
      artifacts,
    });
  }

  const failedRun = terminalRun(input.run, 'FAILED', usage(), 'TERMINAL_ARTIFACT_REQUIRED');
  emit('run.failed', { run: failedRun });
  return Object.freeze({
    runStatus: 'FAILED',
    sessionStatus: 'FAILED',
    usage: usage(),
    finalizedMessages,
    toolExecutions,
    artifacts,
  } satisfies AgentEngineResult);
};

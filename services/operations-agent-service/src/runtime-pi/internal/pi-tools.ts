import type { AgentTool as PiAgentTool } from '@earendil-works/pi-agent-core';
import { Type, type TSchema } from '@earendil-works/pi-ai';

import {
  AgentToolError,
  INVESTIGATION_COMPLETE_TOOL_NAME,
  INVESTIGATION_REQUEST_INPUT_TOOL_NAME,
  parseInvestigationComplete,
  parseInvestigationRequestInput,
  type AgentArtifact,
  type AgentRunBudget,
  type AgentRunContext,
  type AgentTool,
  type AgentToolErrorCode,
} from '../../agent/index.js';

interface CreatePiToolsInput {
  readonly tools: readonly AgentTool[];
  readonly context: AgentRunContext;
  readonly budget: AgentRunBudget;
  readonly runSignal: AbortSignal;
  readonly sessionId: string;
  readonly runId: string;
  readonly onArtifact: (artifact: AgentArtifact) => void;
  readonly onBudgetExhausted: (code: AgentToolErrorCode) => void;
}

const toolFailurePrefix = 'HVAC_AGENT_TOOL_ERROR:';
const toolFailureCodes = new Set<AgentToolErrorCode>([
  'TOOL_ARGUMENTS_INVALID',
  'TOOL_UNAUTHORIZED',
  'TOOL_CANCELLED',
  'TOOL_TIMEOUT',
  'TOOL_CALL_LIMIT',
  'TOOL_CONCURRENCY_LIMIT',
  'TOOL_RESULT_TOO_LARGE',
  'TOOL_OWNER_REQUEST_REJECTED',
  'TOOL_OWNER_RESOURCE_NOT_FOUND',
  'TOOL_OWNER_TIMEOUT',
  'TOOL_OWNER_UNAVAILABLE',
  'TOOL_OWNER_RESPONSE_INVALID',
  'TOOL_EXECUTION_FAILED',
]);

interface ProjectToolBudgetState {
  readonly budget: AgentRunBudget;
  readonly completedToolIds: Set<string>;
  readonly onBudgetExhausted: (code: AgentToolErrorCode) => void;
  terminalToolCallId: string | null;
  calls: number;
  active: number;
}

const safeToolFailure = (error: unknown): Error => {
  const code = error instanceof AgentToolError ? error.code : 'TOOL_EXECUTION_FAILED';
  return new Error(`${toolFailurePrefix}${code}`);
};

const claimTerminalOperation = (state: ProjectToolBudgetState, toolCallId: string): void => {
  if (state.terminalToolCallId !== null) {
    throw new Error('Only one terminal investigation operation may be accepted per Run.');
  }
  state.terminalToolCallId = toolCallId;
};

const toolFailureText = (result: unknown): string | null => {
  if (typeof result !== 'object' || result === null || !('content' in result)) return null;
  const content = (result as { readonly content?: unknown }).content;
  if (!Array.isArray(content)) return null;
  const first = content[0];
  if (typeof first !== 'object' || first === null || !('text' in first)) return null;
  const text = (first as { readonly text?: unknown }).text;
  return typeof text === 'string' ? text : null;
};

export const projectToolFailureCodeFromPiResult = (result: unknown): AgentToolErrorCode | null => {
  const text = toolFailureText(result);
  if (text === null || !text.startsWith(toolFailurePrefix)) return null;
  const code = text.slice(toolFailurePrefix.length) as AgentToolErrorCode;
  return toolFailureCodes.has(code) ? code : null;
};

const investigationCompleteParameters = Type.Object({
  outcome: Type.Union([
    Type.Literal('SUPPORTED_FINDING'),
    Type.Literal('UNABLE_TO_CONCLUDE'),
  ]),
  summary: Type.String({ minLength: 1, maxLength: 2_000 }),
  evidenceRefs: Type.Array(Type.Object({
    owner: Type.Union([
      Type.Literal('REGISTRY'),
      Type.Literal('TELEMETRY'),
      Type.Literal('ENERGY'),
      Type.Literal('ALARM'),
      Type.Literal('FDD'),
      Type.Literal('WORK_ORDER'),
      Type.Literal('COMMAND'),
    ]),
    resourceType: Type.String({ minLength: 1, maxLength: 256 }),
    resourceId: Type.String({ minLength: 1, maxLength: 256 }),
    revision: Type.Optional(Type.String({ minLength: 1, maxLength: 256 })),
    toolExecutionId: Type.String({ minLength: 1, maxLength: 256 }),
  }, { additionalProperties: false }), { maxItems: 32 }),
  limitations: Type.Array(Type.String({ minLength: 1, maxLength: 512 }), { maxItems: 16 }),
  recommendedNext: Type.Array(Type.String({ minLength: 1, maxLength: 512 }), { maxItems: 16 }),
}, { additionalProperties: false });

const investigationRequestInputParameters = Type.Object({
  prompt: Type.String({ minLength: 1 }),
  response: Type.Union([
    Type.Object({
      kind: Type.Literal('TEXT'),
      maxLength: Type.Integer({ minimum: 1 }),
    }, { additionalProperties: false }),
    Type.Object({
      kind: Type.Literal('SINGLE_SELECT'),
      choices: Type.Array(Type.Object({
        value: Type.String({ minLength: 1 }),
        label: Type.String({ minLength: 1 }),
      }, { additionalProperties: false }), { minItems: 1 }),
    }, { additionalProperties: false }),
  ]),
}, { additionalProperties: false });

const adaptProjectTool = (
  tool: AgentTool,
  context: AgentRunContext,
  runSignal: AbortSignal,
  state: ProjectToolBudgetState,
): PiAgentTool => ({
  name: tool.definition.name,
  label: tool.definition.name,
  description: tool.definition.description,
  parameters: tool.definition.inputSchema as TSchema,
  executionMode: tool.definition.executionMode,
  execute: async (toolCallId, parameters, signal) => {
    state.calls += 1;
    if (state.calls > state.budget.maxToolCalls) {
      state.onBudgetExhausted('TOOL_CALL_LIMIT');
      throw safeToolFailure(new AgentToolError('TOOL_CALL_LIMIT'));
    }
    if (state.active >= state.budget.maxParallelToolCalls) {
      state.onBudgetExhausted('TOOL_CONCURRENCY_LIMIT');
      throw safeToolFailure(new AgentToolError('TOOL_CONCURRENCY_LIMIT'));
    }
    state.active += 1;
    try {
      const result = await tool.execute({
        context,
        arguments: parameters,
        signal: signal ?? runSignal,
      });
      const serialized = JSON.stringify(result);
      if (Buffer.byteLength(serialized, 'utf8') > state.budget.maxToolResultBytes) {
        state.onBudgetExhausted('TOOL_RESULT_TOO_LARGE');
        throw new AgentToolError('TOOL_RESULT_TOO_LARGE');
      }
      state.completedToolIds.add(toolCallId);
      return {
        content: [{ type: 'text', text: serialized }],
        details: { kind: 'project-tool', result },
      };
    } catch (error) {
      throw safeToolFailure(error);
    } finally {
      state.active -= 1;
    }
  },
});

const createInvestigationCompleteTool = ({
  sessionId,
  runId,
  onArtifact,
}: Pick<CreatePiToolsInput, 'sessionId' | 'runId' | 'onArtifact'>, state: ProjectToolBudgetState): PiAgentTool => ({
  name: INVESTIGATION_COMPLETE_TOOL_NAME,
  label: INVESTIGATION_COMPLETE_TOOL_NAME,
  description: 'Complete the investigation with a typed evidence-backed outcome.',
  parameters: investigationCompleteParameters,
  executionMode: 'sequential',
  execute: async (toolCallId, parameters) => {
    const finding = parseInvestigationComplete(parameters);
    if (finding.evidenceRefs.some(({ toolExecutionId }) => !state.completedToolIds.has(toolExecutionId))) {
      throw new Error('Finding evidence must reference successful Tool executions from this Run.');
    }
    claimTerminalOperation(state, toolCallId);
    const artifact = Object.freeze({
      id: `artifact-${toolCallId}`,
      sessionId,
      runId,
      kind: 'FINDING',
      finding,
      createdAt: Date.now(),
    } as const);
    onArtifact(artifact);
    return {
      content: [{ type: 'text', text: 'Investigation completion accepted.' }],
      details: { kind: 'terminal-artifact', artifact },
      terminate: true,
    };
  },
});

const createInvestigationRequestInputTool = ({
  sessionId,
  runId,
  onArtifact,
}: Pick<CreatePiToolsInput, 'sessionId' | 'runId' | 'onArtifact'>, state: ProjectToolBudgetState): PiAgentTool => ({
  name: INVESTIGATION_REQUEST_INPUT_TOOL_NAME,
  label: INVESTIGATION_REQUEST_INPUT_TOOL_NAME,
  description: 'Request one bounded operator input and end the current investigation run.',
  parameters: investigationRequestInputParameters,
  executionMode: 'sequential',
  execute: async (toolCallId, parameters) => {
    const request = parseInvestigationRequestInput(parameters);
    claimTerminalOperation(state, toolCallId);
    const artifact = Object.freeze({
      id: `artifact-${toolCallId}`,
      sessionId,
      runId,
      kind: 'INPUT_REQUEST',
      request,
      createdAt: Date.now(),
    } as const);
    onArtifact(artifact);
    return {
      content: [{ type: 'text', text: 'Operator input request accepted.' }],
      details: { kind: 'terminal-artifact', artifact },
      terminate: true,
    };
  },
});

export const createPiTools = (input: CreatePiToolsInput): PiAgentTool[] => {
  const state: ProjectToolBudgetState = {
    budget: input.budget,
    completedToolIds: new Set<string>(),
    onBudgetExhausted: input.onBudgetExhausted,
    terminalToolCallId: null,
    calls: 0,
    active: 0,
  };
  return [
    ...input.tools.map((tool) => adaptProjectTool(tool, input.context, input.runSignal, state)),
    createInvestigationCompleteTool(input, state),
    createInvestigationRequestInputTool(input, state),
  ];
};

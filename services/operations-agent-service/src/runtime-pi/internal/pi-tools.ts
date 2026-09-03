import type { AgentTool as PiAgentTool } from '@earendil-works/pi-agent-core';
import { Type, type TSchema } from '@earendil-works/pi-ai';

import {
  AgentToolError,
  INVESTIGATION_COMPLETE_TOOL_NAME,
  parseInvestigationComplete,
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
  calls: number;
  active: number;
}

const safeToolFailure = (error: unknown): Error => {
  const code = error instanceof AgentToolError ? error.code : 'TOOL_EXECUTION_FAILED';
  return new Error(`${toolFailurePrefix}${code}`);
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
  summary: Type.String({ minLength: 1 }),
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
    resourceType: Type.String({ minLength: 1 }),
    resourceId: Type.String({ minLength: 1 }),
    revision: Type.Optional(Type.String({ minLength: 1 })),
    toolExecutionId: Type.String({ minLength: 1 }),
  }, { additionalProperties: false })),
  limitations: Type.Array(Type.String({ minLength: 1 })),
  recommendedNext: Type.Array(Type.String({ minLength: 1 })),
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
  execute: async (_toolCallId, parameters, signal) => {
    state.calls += 1;
    if (state.calls > state.budget.maxToolCalls) {
      throw safeToolFailure(new AgentToolError('TOOL_CALL_LIMIT'));
    }
    if (state.active >= state.budget.maxParallelToolCalls) {
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
        throw new AgentToolError('TOOL_RESULT_TOO_LARGE');
      }
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
}: Pick<CreatePiToolsInput, 'sessionId' | 'runId' | 'onArtifact'>): PiAgentTool => ({
  name: INVESTIGATION_COMPLETE_TOOL_NAME,
  label: INVESTIGATION_COMPLETE_TOOL_NAME,
  description: 'Complete the investigation with a typed evidence-backed outcome.',
  parameters: investigationCompleteParameters,
  executionMode: 'sequential',
  execute: async (toolCallId, parameters) => {
    const finding = parseInvestigationComplete(parameters);
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

export const createPiTools = (input: CreatePiToolsInput): PiAgentTool[] => {
  const state: ProjectToolBudgetState = {
    budget: input.budget,
    calls: 0,
    active: 0,
  };
  return [
    ...input.tools.map((tool) => adaptProjectTool(tool, input.context, input.runSignal, state)),
    createInvestigationCompleteTool(input),
  ];
};

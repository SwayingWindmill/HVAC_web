import type { AgentTool as PiAgentTool } from '@earendil-works/pi-agent-core';
import { Type, type TSchema } from '@earendil-works/pi-ai';

import {
  AgentToolError,
  INVESTIGATION_COMPLETE_TOOL_NAME,
  INVESTIGATION_REQUEST_INPUT_TOOL_NAME,
  claimsPhysicalExecution,
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
  /** The answer text this Run has shown the operator so far, one entry per message. */
  readonly answers: () => readonly string[];
}

// The answer has already reached the operator, so a claim of operating equipment is corrected in
// writing rather than withdrawn (#592).
const CORRECTION_REQUIRED = '回答里说了操作过设备，但助手只能读取数据。先另写一句以「更正：」开头的话，说明没有也不能操作设备，再调用 investigation_complete。';

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
  /** From which answer on a correction is due, once the answer claimed it operated equipment. */
  correctionFrom: number | null;
  calls: number;
  active: number;
  /** Calls waiting for a free slot, released in order as running calls finish. */
  readonly waiting: (() => void)[];
}

// Only the code, and the Tool's own hint when it wrote one, reach the model.
const safeToolFailure = (error: unknown): Error => {
  const code = error instanceof AgentToolError ? error.code : 'TOOL_EXECUTION_FAILED';
  const hint = error instanceof AgentToolError ? error.hint : undefined;
  return new Error(`${toolFailurePrefix}${code}${hint === undefined ? '' : `\n${hint}`}`);
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
  // Pi checks the arguments against the Tool's schema before executing it; a refusal there is an argument failure.
  if (text?.startsWith('Validation failed for tool ')) return 'TOOL_ARGUMENTS_INVALID';
  if (text === null || !text.startsWith(toolFailurePrefix)) return null;
  const code = text.slice(toolFailurePrefix.length).split('\n')[0] as AgentToolErrorCode;
  return toolFailureCodes.has(code) ? code : null;
};

const investigationCompleteParameters = Type.Object({
  outcome: Type.Union([
    Type.Literal('SUPPORTED_FINDING'),
    Type.Literal('UNABLE_TO_CONCLUDE'),
  ]),
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
  keyFigures: Type.Optional(Type.Array(Type.Object({
    label: Type.String({ minLength: 1, maxLength: 32 }),
    value: Type.String({ minLength: 1, maxLength: 24 }),
    unit: Type.Optional(Type.String({ minLength: 1, maxLength: 16 })),
  }, { additionalProperties: false }), { maxItems: 4 })),
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
    // A batch wider than the limit waits for slots rather than failing the Run.
    if (state.active >= state.budget.maxParallelToolCalls) {
      await new Promise<void>((resolve) => state.waiting.push(resolve));
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
      // The model cites a read in investigation_complete by this id.
      return {
        content: [{ type: 'text', text: JSON.stringify({ evidenceId: toolCallId, result }) }],
        details: { kind: 'project-tool', result },
      };
    } catch (error) {
      throw safeToolFailure(error);
    } finally {
      state.active -= 1;
      state.waiting.shift()?.();
    }
  },
});

const createInvestigationCompleteTool = ({
  sessionId,
  runId,
  onArtifact,
  answers,
}: Pick<CreatePiToolsInput, 'sessionId' | 'runId' | 'onArtifact' | 'answers'>, state: ProjectToolBudgetState): PiAgentTool => ({
  name: INVESTIGATION_COMPLETE_TOOL_NAME,
  label: INVESTIGATION_COMPLETE_TOOL_NAME,
  description: '写完回答正文后调用，给出正文下面卡片里的内容：是否有数据依据、关键数字、数据不足之处、依据和可以接着问的问题。每次回答都要调用。',
  parameters: investigationCompleteParameters,
  executionMode: 'sequential',
  execute: async (toolCallId, parameters) => {
    const finding = parseInvestigationComplete(parameters);
    const shown = answers();
    if (state.correctionFrom === null && shown.some(claimsPhysicalExecution)) state.correctionFrom = shown.length;
    if (state.correctionFrom !== null && !shown.slice(state.correctionFrom).some((answer) => answer.includes('更正：'))) {
      throw new Error(CORRECTION_REQUIRED);
    }
    if (finding.evidenceRefs.some(({ toolExecutionId }) => !state.completedToolIds.has(toolExecutionId))) {
      // The model reads this refusal, so it says which ids it may cite.
      const cited = [...state.completedToolIds];
      throw new Error(cited.length === 0
        ? '这次还没有成功读到数据，不能引用依据：outcome 用 UNABLE_TO_CONCLUDE，evidenceRefs 留空。'
        : `evidenceRefs 的 toolExecutionId 只能填这次读数据结果里的 evidenceId：${cited.join('、')}。`);
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
  description: '缺少关键信息、没法继续时，向值班人员提一个问题，并结束这次回答。',
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
    correctionFrom: null,
    calls: 0,
    active: 0,
    waiting: [],
  };
  return [
    ...input.tools.map((tool) => adaptProjectTool(tool, input.context, input.runSignal, state)),
    createInvestigationCompleteTool(input, state),
    createInvestigationRequestInputTool(input, state),
  ];
};

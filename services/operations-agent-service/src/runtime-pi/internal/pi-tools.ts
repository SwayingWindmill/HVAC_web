import type { AgentTool as PiAgentTool } from '@earendil-works/pi-agent-core';
import { Type, type Static, type TSchema } from '@earendil-works/pi-ai';

import {
  AgentToolError,
  INVESTIGATION_COMPLETE_TOOL_NAME,
  INVESTIGATION_REQUEST_INPUT_TOOL_NAME,
  needsExecutionCorrection,
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
  /** The part of it the Session keeps: the answer without the process talk (#607). */
  readonly keptAnswer: () => string;
  /** Why a terminal Tool refused a call, as a code without the model's text (#624). */
  readonly onRefused: (toolCallId: string, reason: TerminalRefusalReason) => void;
}

/**
 * Why a card or a question to the operator was refused (#624). The model reads the refusal in
 * words; the record keeps only this, so the 运行情况 page can count reasons.
 */
export type TerminalRefusalReason =
  | 'TERMINAL_ALREADY_ACCEPTED'
  | 'FINDING_EVIDENCE_NOT_READ'
  | 'FINDING_CORRECTION_REQUIRED'
  | 'FINDING_CONTENT_INVALID'
  | 'INPUT_REQUEST_CORRECTION_REQUIRED'
  | 'INPUT_REQUEST_INVALID'
  | `FINDING_${'KEY_FIGURES' | 'EVIDENCE_REFS' | 'LIMITATIONS' | 'FOLLOW_UPS' | 'OUTCOME'}_${'TOO_LONG' | 'TOO_MANY' | 'MISSING' | 'INVALID'}`
  | 'FINDING_EXTRA_FIELD'
  | 'FINDING_UNREADABLE';

// The answer has already reached the operator, so a claim of operating equipment is corrected in
// writing rather than withdrawn; both ways of ending a Run check it (#592).
const CORRECTION_REQUIRED = '回答里说了操作过设备，但助手只能读取数据。先另写一句以「更正：」开头的话，说明没有也不能操作设备，再结束这次回答。';

const SCHEMA_FIELDS: Readonly<Record<string, string>> = Object.freeze({
  keyFigures: 'KEY_FIGURES', evidenceRefs: 'EVIDENCE_REFS', limitations: 'LIMITATIONS', recommendedNext: 'FOLLOW_UPS', outcome: 'OUTCOME',
});

/**
 * The reason a card the schema refused before it ran, from pi's message: the first wrong field
 * and how it was wrong ("keyFigures.0.value: must not have more than 24 characters").
 */
export const terminalSchemaRefusal = (toolName: string, result: unknown): TerminalRefusalReason | null => {
  const text = toolFailureText(result);
  if (text === null) return null;
  if (toolName === INVESTIGATION_REQUEST_INPUT_TOOL_NAME) {
    return text.startsWith('Validation failed for tool ') || text.startsWith('Invalid arguments for tool ') ? 'INPUT_REQUEST_INVALID' : null;
  }
  if (toolName !== INVESTIGATION_COMPLETE_TOOL_NAME) return null;
  if (text.startsWith('Invalid arguments for tool ')) return 'FINDING_UNREADABLE';
  if (!text.startsWith('Validation failed for tool ')) return null;
  const errors = [...text.split('\n\nReceived arguments:')[0]!.matchAll(/^ {2}- ([^:]+): (.*)$/gmu)].map(([, path, message]) => ({ path: path!, message: message! }));
  if (errors.some(({ message }) => /additional properties|schema is false/u.test(message))) return 'FINDING_EXTRA_FIELD';
  const first = errors[0];
  if (first === undefined) return 'FINDING_CONTENT_INVALID';
  // The path ends with a missing field's name: "keyFigures.0.value: must have required properties value".
  const field = SCHEMA_FIELDS[first.path.split('.')[0]!];
  if (field === undefined) return 'FINDING_CONTENT_INVALID';
  const how = /required propert(?:y|ies)/u.test(first.message) ? 'MISSING'
    : /more than \d+ characters/u.test(first.message) ? 'TOO_LONG'
      : /more than \d+ items/u.test(first.message) ? 'TOO_MANY'
        : 'INVALID';
  return `FINDING_${field}_${how}` as TerminalRefusalReason;
};

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
  /** Each successful read by the short id the model cites in a card ("E1" → the provider's call id). */
  readonly evidenceIds: Map<string, string>;
  readonly onBudgetExhausted: (code: AgentToolErrorCode) => void;
  terminalToolCallId: string | null;
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

const claimTerminalOperation = (state: ProjectToolBudgetState, toolCallId: string, refuse: Refuse): void => {
  if (state.terminalToolCallId !== null) {
    refuse('TERMINAL_ALREADY_ACCEPTED', '这次回答已经收尾，不能再收尾一次。');
  }
  state.terminalToolCallId = toolCallId;
};

type Refuse = (reason: TerminalRefusalReason, message: string) => never;

const refuser = (onRefused: CreatePiToolsInput['onRefused'], toolCallId: string): Refuse => (reason, message) => {
  onRefused(toolCallId, reason);
  throw new Error(message);
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
    // A figure without a unit (能效) may leave it empty; the card shows no unit then (#624).
    unit: Type.Optional(Type.String({ maxLength: 16, description: '单位，例如 kWh、元；能效没有单位，不写或留空。' })),
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
    // Numbered in call order, so reads started together get their ids in the order they were asked.
    const evidenceId = `E${state.calls}`;
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
      state.evidenceIds.set(evidenceId, toolCallId);
      // The model cites a read in investigation_complete by this id.
      return {
        content: [{ type: 'text', text: JSON.stringify({ evidenceId, result }) }],
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

// A card limitation restates the answer when it names no number the answer does not, and most of
// its neighbouring word pairs are in the answer; the card shows only what the answer does not
// already say (#607). A word is one Chinese character or one number, so the same words about
// another day are new, while a refusal said again in other words is not.
const words = (text: string): string[] => [...text.matchAll(/\p{Script=Han}|\d+/gu)].map(([word]) => word);
const wordPairs = (text: string): Set<string> => {
  const all = words(text);
  const pairs = new Set<string>();
  for (let index = 0; index < all.length - 1; index += 1) pairs.add(`${all[index]} ${all[index + 1]}`);
  return pairs;
};
const RESTATED_SHARE = 0.6;
const withoutRestatedLimitations = <T extends { readonly limitations: readonly string[] }>(finding: T, answer: string): T => {
  const answerWords = new Set(words(answer));
  const answerPairs = wordPairs(answer);
  const limitations = finding.limitations.filter((limitation) => {
    if (words(limitation).some((word) => /\d/u.test(word) && !answerWords.has(word))) return true;
    const own = wordPairs(limitation);
    const shared = [...own].filter((pair) => answerPairs.has(pair)).length;
    return own.size === 0 || shared / own.size < RESTATED_SHARE;
  });
  return limitations.length === finding.limitations.length ? finding : Object.freeze({ ...finding, limitations: Object.freeze(limitations) });
};

// An empty unit means the figure has none (能效); the card leaves it out rather than refusing.
type InvestigationCompleteArguments = Static<typeof investigationCompleteParameters>;
const withoutEmptyUnits = (parameters: InvestigationCompleteArguments): InvestigationCompleteArguments =>
  parameters.keyFigures === undefined ? parameters : {
    ...parameters,
    keyFigures: parameters.keyFigures.map(({ unit, ...figure }) => (unit === '' || unit === undefined ? figure : { ...figure, unit })),
  };

const createInvestigationCompleteTool = ({
  sessionId,
  runId,
  onArtifact,
  answers,
  keptAnswer,
  onRefused,
}: Pick<CreatePiToolsInput, 'sessionId' | 'runId' | 'onArtifact' | 'answers' | 'keptAnswer' | 'onRefused'>, state: ProjectToolBudgetState): PiAgentTool => ({
  name: INVESTIGATION_COMPLETE_TOOL_NAME,
  label: INVESTIGATION_COMPLETE_TOOL_NAME,
  description: '写完回答正文后调用，给出正文下面卡片里的内容：是否有数据依据、关键数字、数据不足之处、依据和可以接着问的问题。每次回答都要调用。',
  parameters: investigationCompleteParameters,
  executionMode: 'sequential',
  execute: async (toolCallId, parameters) => {
    const refuse = refuser(onRefused, toolCallId);
    let parsed: ReturnType<typeof parseInvestigationComplete>;
    try {
      // Pi has checked the arguments against investigationCompleteParameters before this runs.
      parsed = parseInvestigationComplete(withoutEmptyUnits(parameters as InvestigationCompleteArguments));
    } catch (error) {
      return refuse('FINDING_CONTENT_INVALID', error instanceof Error ? error.message : String(error));
    }
    if (needsExecutionCorrection(answers())) refuse('FINDING_CORRECTION_REQUIRED', CORRECTION_REQUIRED);
    // A ref names a read by its short id; the card keeps the read's call id.
    if (parsed.evidenceRefs.some(({ toolExecutionId }) => !state.evidenceIds.has(toolExecutionId))) {
      // The model reads this refusal, so it says which ids it may cite.
      const cited = [...state.evidenceIds.keys()];
      refuse('FINDING_EVIDENCE_NOT_READ', cited.length === 0
        ? '这次还没有成功读到数据，不能引用依据：outcome 用 UNABLE_TO_CONCLUDE，evidenceRefs 留空。'
        : `evidenceRefs 的 toolExecutionId 只能填这次读数据结果里的 evidenceId：${cited.join('、')}。`);
    }
    const finding = Object.freeze({
      ...parsed,
      evidenceRefs: Object.freeze(parsed.evidenceRefs.map((ref) => Object.freeze({ ...ref, toolExecutionId: state.evidenceIds.get(ref.toolExecutionId)! }))),
    });
    claimTerminalOperation(state, toolCallId, refuse);
    const artifact = Object.freeze({
      id: `artifact-${toolCallId}`,
      sessionId,
      runId,
      kind: 'FINDING',
      finding: withoutRestatedLimitations(finding, keptAnswer()),
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
  answers,
  onRefused,
}: Pick<CreatePiToolsInput, 'sessionId' | 'runId' | 'onArtifact' | 'answers' | 'onRefused'>, state: ProjectToolBudgetState): PiAgentTool => ({
  name: INVESTIGATION_REQUEST_INPUT_TOOL_NAME,
  label: INVESTIGATION_REQUEST_INPUT_TOOL_NAME,
  description: '缺少关键信息、没法继续时，向值班人员提一个问题，并结束这次回答。',
  parameters: investigationRequestInputParameters,
  executionMode: 'sequential',
  execute: async (toolCallId, parameters) => {
    const refuse = refuser(onRefused, toolCallId);
    let request: ReturnType<typeof parseInvestigationRequestInput>;
    try {
      request = parseInvestigationRequestInput(parameters);
    } catch (error) {
      return refuse('INPUT_REQUEST_INVALID', error instanceof Error ? error.message : String(error));
    }
    if (needsExecutionCorrection(answers())) refuse('INPUT_REQUEST_CORRECTION_REQUIRED', CORRECTION_REQUIRED);
    claimTerminalOperation(state, toolCallId, refuse);
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
    evidenceIds: new Map<string, string>(),
    onBudgetExhausted: input.onBudgetExhausted,
    terminalToolCallId: null,
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

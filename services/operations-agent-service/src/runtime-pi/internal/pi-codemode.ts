import type { AgentTool as PiAgentTool } from '@earendil-works/pi-agent-core';
import { Type } from '@earendil-works/pi-ai';
import {
  CodemodeSandbox,
  renderDeclarations,
  type CodemodeJsonSchema,
  type CodemodeOutputItem,
  type CodemodeResult,
  type CodemodeTool,
} from '@earendil-works/pi-codemode';

import {
  AgentToolError,
  type AgentRunContext,
  type AgentTool,
} from '../../agent/index.js';
import {
  executeProjectToolCall,
  safeToolFailure,
  toolFailurePrefix,
  type ProjectToolBudgetState,
} from './pi-tools.js';

export const CODEMODE_TOOL_NAME = 'codemode' as const;
const DEFAULT_CODEMODE_TIMEOUT_MS = 30_000;
const CODEMODE_MEMORY_LIMIT_BYTES = 32 * 1024 * 1024;
const MAX_CODEMODE_SOURCE_CHARS = 16_384;

const codemodeParameters = Type.Object({
  code: Type.String({ minLength: 1, maxLength: MAX_CODEMODE_SOURCE_CHARS }),
}, { additionalProperties: false });

const codemodeToolOutputSchema: CodemodeJsonSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    evidenceId: Object.freeze({ type: 'string' }),
    result: true,
  }),
  required: Object.freeze(['evidenceId', 'result']),
  additionalProperties: false,
});

export interface CreateCodemodeToolInput {
  readonly tools: readonly AgentTool[];
  readonly context: AgentRunContext;
  readonly runSignal: AbortSignal;
  readonly state: ProjectToolBudgetState;
  readonly onNestedToolStart: (toolCallId: string, toolName: string, args: unknown) => void;
  readonly onNestedToolEnd: (toolCallId: string, toolName: string, isError: boolean, result: unknown) => void;
}

const codemodeTextOutput = (output: readonly CodemodeOutputItem[]): string => output
  .filter((item): item is Extract<CodemodeOutputItem, { type: 'text' }> => item.type === 'text')
  .map((item) => item.text)
  .join('\n');

const formatCodemodeFailure = (
  error: Extract<CodemodeResult, { ok: false }>['error'],
  output: readonly CodemodeOutputItem[],
): string => {
  if (error.kind === 'script' && error.message.startsWith(toolFailurePrefix)) {
    return error.message;
  }
  const detail = error.kind === 'script'
    ? (error.stack ?? error.message)
    : `Codemode ${error.kind} error: ${error.message}`;
  const priorOutput = codemodeTextOutput(output);
  return priorOutput.length > 0 ? `${detail}\n\nOutput before error:\n${priorOutput}` : detail;
};

const formatCodemodeSuccess = (result: Extract<CodemodeResult, { ok: true }>) => {
  const content: Array<{ type: 'text'; text: string } | { type: 'image'; data: string; mimeType: string }> = result.output.map((item) => (
    item.type === 'text'
      ? { type: 'text' as const, text: item.text }
      : { type: 'image' as const, data: item.data, mimeType: item.mimeType }
  ));
  if (result.value !== undefined) {
    content.push({ type: 'text' as const, text: JSON.stringify(result.value) });
  }
  if (content.length === 0) {
    content.push({ type: 'text' as const, text: 'Script completed with no output.' });
  }
  return {
    content,
    details: {
      kind: 'codemode',
      calls: result.calls.length,
    },
  };
};

const settleNestedCalls = async (
  inFlight: ReadonlySet<Promise<unknown>>,
  signal: AbortSignal,
): Promise<void> => {
  if (signal.aborted) return;
  let onAbort: (() => void) | undefined;
  const aborted = new Promise<void>((resolve) => {
    onAbort = () => resolve();
    signal.addEventListener('abort', onAbort, { once: true });
  });
  try {
    await Promise.race([Promise.allSettled([...inFlight]), aborted]);
  } finally {
    if (onAbort !== undefined) signal.removeEventListener('abort', onAbort);
  }
};

const readCodeArgument = (parameters: unknown): string => {
  if (typeof parameters !== 'object' || parameters === null || !('code' in parameters)) {
    throw safeToolFailure(new AgentToolError('TOOL_ARGUMENTS_INVALID', 'codemode requires a code string.'));
  }
  const code = (parameters as { readonly code?: unknown }).code;
  if (typeof code !== 'string' || code.length === 0) {
    throw safeToolFailure(new AgentToolError('TOOL_ARGUMENTS_INVALID', 'codemode code must be a non-empty string.'));
  }
  return code;
};

/**
 * Exposes all allowed HVAC read tools inside a per-call QuickJS sandbox so the model can batch
 * and filter reads with async JavaScript while preserving per-tool governance and evidence ids.
 */
export const createCodemodeTool = (input: CreateCodemodeToolInput): PiAgentTool => {
  const declaredTools: CodemodeTool[] = input.tools.map((tool) => ({
    name: tool.definition.name,
    description: tool.definition.description,
    inputSchema: tool.definition.inputSchema as CodemodeJsonSchema,
    outputSchema: codemodeToolOutputSchema,
    execute: () => undefined,
  }));
  const declarations = renderDeclarations({ tools: declaredTools });
  const description = [
    '在 QuickJS 沙箱中执行一段异步 JavaScript 代码来读取并整理当前站点的数据。',
    '可用 `await tools.<工具名>(参数)` 调用下列只读工具，多个互不依赖的查询用 `Promise.all([...])` 并行。',
    '每次工具调用会返回 `{ evidenceId, result }`（例如 `{ evidenceId: "E1", result: ... }`）。',
    '请用 `text(...)` 或 `return ...` 只把提炼后的数字、结论要点和对应的 `evidenceId` 返回给对话，原始中间数据不会进入上下文。',
    ...(declarations.length > 0 ? ['', declarations] : []),
  ].join('\n');

  return {
    name: CODEMODE_TOOL_NAME,
    label: CODEMODE_TOOL_NAME,
    description,
    parameters: codemodeParameters,
    executionMode: 'sequential',
    execute: async (outerToolCallId, parameters, outerSignal) => {
      const code = readCodeArgument(parameters);
      const executionSignal = outerSignal !== undefined && outerSignal !== input.runSignal
        ? AbortSignal.any([outerSignal, input.runSignal])
        : input.runSignal;
      let nestedSeq = 0;
      const inFlightNested = new Set<Promise<unknown>>();

      const sandboxTools: CodemodeTool[] = input.tools.map((tool) => ({
        name: tool.definition.name,
        description: tool.definition.description,
        inputSchema: tool.definition.inputSchema as CodemodeJsonSchema,
        outputSchema: codemodeToolOutputSchema,
        execute: (args, { signal: nestedSignal }) => {
          const nestedToolCallId = `${outerToolCallId}-${++nestedSeq}`;
          const normalizedArgs = args === undefined ? {} : args;
          input.onNestedToolStart(nestedToolCallId, tool.definition.name, normalizedArgs);
          const pending = (async () => {
            let isError = true;
            let resultOrErrorPayload: unknown;
            try {
              const output = await executeProjectToolCall({
                tool,
                toolCallId: nestedToolCallId,
                parameters: normalizedArgs,
                signal: nestedSignal,
                context: input.context,
                runSignal: input.runSignal,
                state: input.state,
              });
              isError = false;
              resultOrErrorPayload = output;
              return output;
            } catch (error) {
              isError = true;
              const message = error instanceof Error ? error.message : String(error);
              resultOrErrorPayload = { content: [{ type: 'text', text: message }] };
              throw error instanceof Error ? error : new Error(message);
            } finally {
              input.onNestedToolEnd(nestedToolCallId, tool.definition.name, isError, resultOrErrorPayload);
            }
          })();
          inFlightNested.add(pending);
          const cleanup = () => {
            inFlightNested.delete(pending);
          };
          pending.then(cleanup, cleanup);
          return pending;
        },
      }));

      const sandbox = new CodemodeSandbox({
        tools: sandboxTools,
        timeoutMs: Math.min(input.state.budget.maxWallClockMs, DEFAULT_CODEMODE_TIMEOUT_MS),
        memoryLimitBytes: CODEMODE_MEMORY_LIMIT_BYTES,
      });

      try {
        const result = await sandbox.execute(code, { signal: executionSignal });
        if (inFlightNested.size > 0) {
          await settleNestedCalls(inFlightNested, executionSignal);
        }
        if (input.state.budgetFailureCode !== null) {
          throw safeToolFailure(new AgentToolError(input.state.budgetFailureCode));
        }
        if (!result.ok) {
          throw new Error(formatCodemodeFailure(result.error, result.output));
        }
        return formatCodemodeSuccess(result);
      } finally {
        await sandbox.close();
      }
    },
  };
};

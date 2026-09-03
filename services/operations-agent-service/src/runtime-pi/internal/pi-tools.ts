import type { AgentTool as PiAgentTool } from '@earendil-works/pi-agent-core';
import { Type, type TSchema } from '@earendil-works/pi-ai';

import {
  INVESTIGATION_COMPLETE_TOOL_NAME,
  parseInvestigationComplete,
  type AgentArtifact,
  type AgentRunContext,
  type AgentTool,
} from '../../agent/index.js';

interface CreatePiToolsInput {
  readonly tools: readonly AgentTool[];
  readonly context: AgentRunContext;
  readonly runSignal: AbortSignal;
  readonly sessionId: string;
  readonly runId: string;
  readonly onArtifact: (artifact: AgentArtifact) => void;
}

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
): PiAgentTool => ({
  name: tool.definition.name,
  label: tool.definition.name,
  description: tool.definition.description,
  parameters: tool.definition.inputSchema as TSchema,
  executionMode: tool.definition.executionMode,
  execute: async (_toolCallId, parameters, signal) => {
    const result = await tool.execute({
      context,
      arguments: parameters,
      signal: signal ?? runSignal,
    });
    return {
      content: [{ type: 'text', text: JSON.stringify(result) }],
      details: { kind: 'project-tool', result },
    };
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

export const createPiTools = (input: CreatePiToolsInput): PiAgentTool[] => [
  ...input.tools.map((tool) => adaptProjectTool(tool, input.context, input.runSignal)),
  createInvestigationCompleteTool(input),
];

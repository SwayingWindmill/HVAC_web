import {
  createModels,
  fauxAssistantMessage,
  fauxProvider,
  fauxText,
  fauxThinking,
  fauxToolCall,
} from '@earendil-works/pi-ai';

import type { AgentEngine, AgentModelRef } from '../agent/index.js';
import {
  composePiAgentRuntime,
  type AgentModelThinkingLevel,
  type PiAgentModelPolicy,
} from './internal/model-runtime.js';

export type ScriptedPiPart =
  | Readonly<{ type: 'text'; text: string }>
  | Readonly<{ type: 'thinking'; text: string }>
  | Readonly<{ type: 'tool-call'; name: string; arguments: Record<string, unknown> }>;

export interface ScriptedPiResponse {
  readonly parts: readonly ScriptedPiPart[];
  readonly stopReason: 'stop' | 'toolUse';
}

export interface ScriptedPiEngineOptions {
  readonly responses: readonly ScriptedPiResponse[];
  readonly tokensPerSecond?: number;
  readonly policy?: Readonly<{
    thinkingLevel?: AgentModelThinkingLevel;
    timeoutMs?: number;
    maxOutputTokens?: number;
  }>;
}

export interface ScriptedPiAgentEngine {
  readonly engine: AgentEngine;
  readonly modelRef: AgentModelRef;
  readonly policy: PiAgentModelPolicy;
}

const toFauxPart = (part: ScriptedPiPart) => {
  switch (part.type) {
    case 'text':
      return fauxText(part.text);
    case 'thinking':
      return fauxThinking(part.text);
    case 'tool-call':
      return fauxToolCall(part.name, part.arguments);
  }
};

export const createScriptedPiAgentEngine = ({
  responses,
  tokensPerSecond,
  policy,
}: ScriptedPiEngineOptions): ScriptedPiAgentEngine => {
  const faux = fauxProvider(tokensPerSecond === undefined ? {} : { tokensPerSecond });
  faux.setResponses(responses.map((response) => fauxAssistantMessage(
    response.parts.map(toFauxPart),
    { stopReason: response.stopReason },
  )));

  const models = createModels();
  models.setProvider(faux.provider);
  const model = faux.getModel();

  return composePiAgentRuntime({
    models,
    model,
    systemPrompt: 'Use authorized HVAC Tools and finish through investigation.complete.',
    thinkingLevel: policy?.thinkingLevel ?? 'off',
    timeoutMs: policy?.timeoutMs ?? 30_000,
    maxOutputTokens: policy?.maxOutputTokens ?? 2_048,
  });
};

import {
  createModels,
  fauxAssistantMessage,
  fauxProvider,
  fauxText,
  fauxThinking,
  fauxToolCall,
  type Message,
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
  | Readonly<{ type: 'tool-call'; name: string; arguments: Record<string, unknown>; id?: string }>;

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

export interface ScriptedPiRequestMessage {
  readonly role: string;
  readonly text: string;
}

export interface ScriptedPiAgentEngine {
  readonly engine: AgentEngine;
  readonly modelRef: AgentModelRef;
  readonly policy: PiAgentModelPolicy;
  readonly requests: readonly (readonly ScriptedPiRequestMessage[])[];
}

const toFauxPart = (part: ScriptedPiPart) => {
  switch (part.type) {
    case 'text':
      return fauxText(part.text);
    case 'thinking':
      return fauxThinking(part.text);
    case 'tool-call':
      return fauxToolCall(part.name, part.arguments, part.id === undefined ? {} : { id: part.id });
  }
};

const visibleMessageText = (message: Message): string => {
  if (typeof message.content === 'string') return message.content;
  return message.content
    .filter((part) => part.type === 'text')
    .map((part) => part.text)
    .join('\n');
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
  const requests: ScriptedPiRequestMessage[][] = [];
  const observedStreamSimple: typeof models.streamSimple = (requestModel, context, options) => {
    requests.push(context.messages.map((message) => Object.freeze({
      role: message.role,
      text: visibleMessageText(message),
    })));
    return models.streamSimple(requestModel, context, options);
  };
  const observedModels = new Proxy(models, {
    get(target, property, receiver) {
      if (property === 'streamSimple') return observedStreamSimple;
      const value = Reflect.get(target, property, receiver);
      return typeof value === 'function' ? value.bind(target) : value;
    },
  });
  const runtime = composePiAgentRuntime({
    models: observedModels,
    model,
    thinkingLevel: policy?.thinkingLevel ?? 'off',
    timeoutMs: policy?.timeoutMs ?? 30_000,
    maxOutputTokens: policy?.maxOutputTokens ?? 2_048,
  });

  return Object.freeze({ ...runtime, requests });
};

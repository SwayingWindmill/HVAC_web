import type { StreamFn } from '@earendil-works/pi-agent-core';
import {
  createModels,
  getSupportedThinkingLevels,
  type Model,
  type ModelThinkingLevel,
  type Models,
} from '@earendil-works/pi-ai';
import { openaiProvider } from '@earendil-works/pi-ai/providers/openai';

import type { AgentEngine, AgentModelRef } from '../../agent/index.js';
import { createPiAgentEngine } from './pi-runtime.js';
import { OPERATIONS_INVESTIGATION_SYSTEM_POLICY } from './system-policy.js';

export const AGENT_MODEL_PROVIDER_ENV = 'AGENT_MODEL_PROVIDER' as const;
export const AGENT_MODEL_ID_ENV = 'AGENT_MODEL_ID' as const;
export const AGENT_MODEL_ALLOWLIST_ENV = 'AGENT_MODEL_ALLOWLIST' as const;
export const AGENT_MODEL_THINKING_LEVEL_ENV = 'AGENT_MODEL_THINKING_LEVEL' as const;
export const AGENT_MODEL_TIMEOUT_MS_ENV = 'AGENT_MODEL_TIMEOUT_MS' as const;
export const AGENT_MODEL_MAX_OUTPUT_TOKENS_ENV = ['AGENT_MODEL_MAX_OUTPUT', 'TOKENS'].join('_');

const PRODUCTION_PROVIDER_ID = 'openai' as const;
const PRODUCTION_ACCESS_ENV = ['OPENAI', 'API', 'KEY'].join('_');
const DEFAULT_THINKING_LEVEL: ModelThinkingLevel = 'off';
const DEFAULT_TIMEOUT_MS = 30_000;
const DEFAULT_MAX_OUTPUT_TOKENS = 2_048;
const MIN_TIMEOUT_MS = 100;
const MAX_TIMEOUT_MS = 120_000;
const MIN_OUTPUT_TOKENS = 64;

export type PiModelConfigurationErrorCode =
  | 'MODEL_PROVIDER_REQUIRED'
  | 'MODEL_PROVIDER_UNSUPPORTED'
  | 'MODEL_ID_REQUIRED'
  | 'MODEL_ALLOWLIST_REQUIRED'
  | 'MODEL_NOT_ALLOWLISTED'
  | 'MODEL_NOT_REGISTERED'
  | 'MODEL_THINKING_INVALID'
  | 'MODEL_THINKING_UNSUPPORTED'
  | 'MODEL_TIMEOUT_INVALID'
  | 'MODEL_OUTPUT_LIMIT_INVALID'
  | 'MODEL_PROVIDER_UNAVAILABLE';

export class PiModelConfigurationError extends Error {
  readonly code: PiModelConfigurationErrorCode;

  constructor(code: PiModelConfigurationErrorCode, message: string) {
    super(message);
    this.name = 'PiModelConfigurationError';
    this.code = code;
  }
}

export type PiModelEnvironment = Readonly<Record<string, string | undefined>>;
export type AgentModelThinkingLevel =
  | 'off'
  | 'minimal'
  | 'low'
  | 'medium'
  | 'high'
  | 'xhigh'
  | 'max';

export interface PiAgentModelPolicy {
  readonly modelRef: AgentModelRef;
  readonly thinkingLevel: AgentModelThinkingLevel;
  readonly timeoutMs: number;
  readonly maxOutputTokens: number;
}

export interface PiAgentRuntime {
  readonly engine: AgentEngine;
  readonly modelRef: AgentModelRef;
  readonly policy: PiAgentModelPolicy;
}

export interface ProductionPiAgentRuntimeOptions {
  readonly environment: PiModelEnvironment;
}

interface ParsedModelConfiguration {
  readonly provider: typeof PRODUCTION_PROVIDER_ID;
  readonly modelId: string;
  readonly thinkingLevel: ModelThinkingLevel;
  readonly timeoutMs: number;
  readonly maxOutputTokens: number;
}

interface ComposePiAgentRuntimeOptions {
  readonly models: Models;
  readonly model: Model<any>;
  readonly thinkingLevel: ModelThinkingLevel;
  readonly timeoutMs: number;
  readonly maxOutputTokens: number;
}

const required = (
  environment: PiModelEnvironment,
  name: string,
  code: PiModelConfigurationErrorCode,
): string => {
  const value = environment[name]?.trim();
  if (!value) throw new PiModelConfigurationError(code, `${name} is required.`);
  return value;
};

const parseBoundedInteger = (
  value: string | undefined,
  fallback: number,
  minimum: number,
  maximum: number,
  code: PiModelConfigurationErrorCode,
  name: string,
): number => {
  if (value === undefined || value.trim() === '') return fallback;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < minimum || parsed > maximum) {
    throw new PiModelConfigurationError(
      code,
      `${name} must be an integer between ${minimum} and ${maximum}.`,
    );
  }
  return parsed;
};

const parseThinkingLevel = (value: string | undefined): ModelThinkingLevel => {
  if (value === undefined || value.trim() === '') return DEFAULT_THINKING_LEVEL;
  const normalized = value.trim();
  const supported: readonly ModelThinkingLevel[] = [
    'off',
    'minimal',
    'low',
    'medium',
    'high',
    'xhigh',
    'max',
  ];
  if (!supported.includes(normalized as ModelThinkingLevel)) {
    throw new PiModelConfigurationError(
      'MODEL_THINKING_INVALID',
      `${AGENT_MODEL_THINKING_LEVEL_ENV} is invalid.`,
    );
  }
  return normalized as ModelThinkingLevel;
};

const parseConfiguration = (environment: PiModelEnvironment): ParsedModelConfiguration => {
  const provider = required(environment, AGENT_MODEL_PROVIDER_ENV, 'MODEL_PROVIDER_REQUIRED');
  if (provider !== PRODUCTION_PROVIDER_ID) {
    throw new PiModelConfigurationError(
      'MODEL_PROVIDER_UNSUPPORTED',
      `Only ${PRODUCTION_PROVIDER_ID} is enabled for the first production Pi runtime.`,
    );
  }

  const modelId = required(environment, AGENT_MODEL_ID_ENV, 'MODEL_ID_REQUIRED');
  const allowlistValue = required(
    environment,
    AGENT_MODEL_ALLOWLIST_ENV,
    'MODEL_ALLOWLIST_REQUIRED',
  );
  const allowlist = new Set(
    allowlistValue
      .split(',')
      .map((entry) => entry.trim())
      .filter((entry) => entry.length > 0),
  );
  if (!allowlist.has(modelId)) {
    throw new PiModelConfigurationError(
      'MODEL_NOT_ALLOWLISTED',
      `${modelId} is not present in ${AGENT_MODEL_ALLOWLIST_ENV}.`,
    );
  }

  return Object.freeze({
    provider,
    modelId,
    thinkingLevel: parseThinkingLevel(environment[AGENT_MODEL_THINKING_LEVEL_ENV]),
    timeoutMs: parseBoundedInteger(
      environment[AGENT_MODEL_TIMEOUT_MS_ENV],
      DEFAULT_TIMEOUT_MS,
      MIN_TIMEOUT_MS,
      MAX_TIMEOUT_MS,
      'MODEL_TIMEOUT_INVALID',
      AGENT_MODEL_TIMEOUT_MS_ENV,
    ),
    maxOutputTokens: parseBoundedInteger(
      environment[AGENT_MODEL_MAX_OUTPUT_TOKENS_ENV],
      DEFAULT_MAX_OUTPUT_TOKENS,
      MIN_OUTPUT_TOKENS,
      Number.MAX_SAFE_INTEGER,
      'MODEL_OUTPUT_LIMIT_INVALID',
      AGENT_MODEL_MAX_OUTPUT_TOKENS_ENV,
    ),
  });
};

const validateModelPolicy = (
  model: Model<any>,
  configuration: ParsedModelConfiguration,
): void => {
  if (configuration.maxOutputTokens > model.maxTokens) {
    throw new PiModelConfigurationError(
      'MODEL_OUTPUT_LIMIT_INVALID',
      `${AGENT_MODEL_MAX_OUTPUT_TOKENS_ENV} exceeds the selected model output limit.`,
    );
  }

  if (!getSupportedThinkingLevels(model).includes(configuration.thinkingLevel)) {
    throw new PiModelConfigurationError(
      'MODEL_THINKING_UNSUPPORTED',
      `${configuration.modelId} does not support thinking level ${configuration.thinkingLevel}.`,
    );
  }
};

const MAX_OUTPUT_OPTION = ['max', 'Tokens'].join('') as 'maxTokens';

const createPolicyStreamFn = (
  models: Models,
  policy: Pick<PiAgentModelPolicy, 'thinkingLevel' | 'timeoutMs' | 'maxOutputTokens'>,
): StreamFn => (model, context, options = {}) => models.streamSimple(model, context, {
  ...options,
  timeoutMs: policy.timeoutMs,
  [MAX_OUTPUT_OPTION]: policy.maxOutputTokens,
  ...(policy.thinkingLevel === 'off' ? {} : { reasoning: policy.thinkingLevel }),
});

export const composePiAgentRuntime = ({
  models,
  model,
  thinkingLevel,
  timeoutMs,
  maxOutputTokens,
}: ComposePiAgentRuntimeOptions): PiAgentRuntime => {
  const modelRef = Object.freeze({ provider: model.provider, model: model.id });
  const policy = Object.freeze({ modelRef, thinkingLevel, timeoutMs, maxOutputTokens });

  return Object.freeze({
    engine: createPiAgentEngine({
      model,
      streamFn: createPolicyStreamFn(models, policy),
      systemPrompt: OPERATIONS_INVESTIGATION_SYSTEM_POLICY,
      thinkingLevel,
    }),
    modelRef,
    policy,
  });
};

const createConfiguredModels = (environment: PiModelEnvironment) => {
  const models = createModels({
    authContext: {
      env: async (name) => name === PRODUCTION_ACCESS_ENV
        ? environment[PRODUCTION_ACCESS_ENV]
        : undefined,
      fileExists: async () => false,
    },
  });
  models.setProvider(openaiProvider());
  return models;
};

export const createProductionPiAgentRuntimeFromEnvironment = async ({
  environment,
}: ProductionPiAgentRuntimeOptions): Promise<PiAgentRuntime> => {
  const configuration = parseConfiguration(environment);
  const models = createConfiguredModels(environment);
  const model = models.getModel(configuration.provider, configuration.modelId);
  if (model === undefined) {
    throw new PiModelConfigurationError(
      'MODEL_NOT_REGISTERED',
      'Configured model is not registered in the Pi catalog.',
    );
  }
  validateModelPolicy(model, configuration);
  const availableModels = await models.getAvailable(configuration.provider);
  if (!availableModels.some((candidate) => candidate.id === configuration.modelId)) {
    throw new PiModelConfigurationError(
      'MODEL_PROVIDER_UNAVAILABLE',
      'Configured production model is unavailable to the server runtime.',
    );
  }
  const { thinkingLevel, timeoutMs, maxOutputTokens } = configuration;
  return composePiAgentRuntime({
    models,
    model,
    thinkingLevel,
    timeoutMs,
    maxOutputTokens,
  });
};

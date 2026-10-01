export const runtimePiModule = Object.freeze({
  name: 'runtime-pi',
  layer: 'adapter',
  dependencies: ['agent'],
} as const);

export type RuntimePiModule = typeof runtimePiModule;

export {
  AGENT_MODEL_ALLOWLIST_ENV,
  AGENT_MODEL_ID_ENV,
  AGENT_MODEL_MAX_OUTPUT_TOKENS_ENV,
  AGENT_MODEL_PROVIDER_ENV,
  AGENT_MODEL_THINKING_LEVEL_ENV,
  AGENT_MODEL_TIMEOUT_MS_ENV,
  PiModelConfigurationError,
  createProductionPiAgentRuntimeFromEnvironment,
} from './internal/model-runtime.js';
export type {
  AgentModelThinkingLevel,
  PiAgentModelPolicy,
  PiAgentRuntime,
  PiModelConfigurationErrorCode,
  PiModelEnvironment,
  ProductionPiAgentRuntimeOptions,
} from './internal/model-runtime.js';

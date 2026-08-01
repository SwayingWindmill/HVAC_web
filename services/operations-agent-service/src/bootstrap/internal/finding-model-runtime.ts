import {
  createSiteNightEnergyInvestigationCoordinator,
  type FindingSynthesizer,
  type SiteNightEnergyInvestigationCoordinator,
  type SiteNightEnergyInvestigationCoordinatorPorts,
  type SiteNightEnergyInvestigationPolicy,
} from '../../application/index.js';
import {
  createOpenAiFindingSynthesizer,
  type OpenAiFindingSynthesizerOptions,
} from '../../model/index.js';

export const OPERATIONS_AGENT_FINDING_MODEL_PROVIDER_ENV = 'OPERATIONS_AGENT_FINDING_MODEL_PROVIDER';
export const OPERATIONS_AGENT_FINDING_MODEL_ENV = 'OPERATIONS_AGENT_FINDING_MODEL';
export const OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV = 'OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST';
export const OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV = 'OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS';
export const OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV = [
  'OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT',
  'TOKENS',
].join('_');
const standardServerAccessVariable = ['OPENAI', 'API', 'KEY'].join('_');

const defaultTimeoutMs = 5_000;
const defaultMaximumOutputTokens = 512;
const modelPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/u;

export type OperationsAgentEnvironment = Readonly<Record<string, string | undefined>>;

export type OperationsAgentFindingModelRuntime =
  | {
    readonly status: 'DISABLED';
    readonly provider: null;
    readonly model: null;
    readonly findingSynthesisTimeoutMs: null;
    readonly maximumOutputTokens: null;
    readonly findingSynthesizer: null;
  }
  | {
    readonly status: 'ENABLED';
    readonly provider: 'openai';
    readonly model: string;
    readonly findingSynthesisTimeoutMs: number;
    readonly maximumOutputTokens: number;
    readonly findingSynthesizer: FindingSynthesizer;
  };

export interface OperationsAgentFindingModelRuntimeOptions {
  readonly environment?: OperationsAgentEnvironment;
  readonly createOpenAiSynthesizer?: (
    options: OpenAiFindingSynthesizerOptions,
  ) => FindingSynthesizer;
}

export interface EnvironmentConfiguredSiteNightEnergyCoordinatorOptions
  extends OperationsAgentFindingModelRuntimeOptions {
  readonly policy?: Partial<SiteNightEnergyInvestigationPolicy>;
}

export class OperationsAgentFindingModelConfigurationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'OperationsAgentFindingModelConfigurationError';
  }
}

const configuredValue = (environment: OperationsAgentEnvironment, name: string): string | null => {
  const value = environment[name];
  if (value === undefined || value.trim().length === 0) return null;
  return value.trim();
};

const requireModel = (value: string | null, label: string): string => {
  if (value === null || !modelPattern.test(value)) {
    throw new OperationsAgentFindingModelConfigurationError(`${label} is missing or invalid.`);
  }
  return value;
};

const parseInteger = (
  value: string | null,
  fallback: number,
  minimum: number,
  maximum: number,
  label: string,
): number => {
  if (value === null) return fallback;
  if (!/^[0-9]+$/u.test(value)) {
    throw new OperationsAgentFindingModelConfigurationError(`${label} must be an integer.`);
  }
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < minimum || parsed > maximum) {
    throw new OperationsAgentFindingModelConfigurationError(`${label} is outside the supported bound.`);
  }
  return parsed;
};

const parseAllowlist = (value: string | null): readonly string[] => {
  if (value === null) {
    throw new OperationsAgentFindingModelConfigurationError(
      `${OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV} is required when the Provider is enabled.`,
    );
  }
  const models = value.split(',').map((item) => item.trim()).filter((item) => item.length > 0);
  if (models.length === 0 || models.length > 32 || new Set(models).size !== models.length
    || models.some((model) => !modelPattern.test(model))) {
    throw new OperationsAgentFindingModelConfigurationError('Finding model allowlist is invalid.');
  }
  return Object.freeze(models);
};

const hasDisabledProviderSettings = (environment: OperationsAgentEnvironment): boolean => [
  OPERATIONS_AGENT_FINDING_MODEL_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV,
].some((name) => configuredValue(environment, name) !== null);

export const createOperationsAgentFindingModelRuntimeFromEnvironment = (
  options: OperationsAgentFindingModelRuntimeOptions = {},
): OperationsAgentFindingModelRuntime => {
  const environment = options.environment ?? process.env;
  const provider = configuredValue(environment, OPERATIONS_AGENT_FINDING_MODEL_PROVIDER_ENV);
  if (provider === null || provider === 'disabled') {
    if (hasDisabledProviderSettings(environment)) {
      throw new OperationsAgentFindingModelConfigurationError(
        'Finding model settings require an explicit enabled Provider.',
      );
    }
    return Object.freeze({
      status: 'DISABLED',
      provider: null,
      model: null,
      findingSynthesisTimeoutMs: null,
      maximumOutputTokens: null,
      findingSynthesizer: null,
    });
  }
  if (provider !== 'openai') {
    throw new OperationsAgentFindingModelConfigurationError('Finding model Provider is unsupported.');
  }
  if (configuredValue(environment, standardServerAccessVariable) === null) {
    throw new OperationsAgentFindingModelConfigurationError(
      'The standard OpenAI server access environment variable is required.',
    );
  }
  const model = requireModel(
    configuredValue(environment, OPERATIONS_AGENT_FINDING_MODEL_ENV),
    OPERATIONS_AGENT_FINDING_MODEL_ENV,
  );
  const allowlist = parseAllowlist(
    configuredValue(environment, OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV),
  );
  if (!allowlist.includes(model)) {
    throw new OperationsAgentFindingModelConfigurationError(
      'The configured Finding model is not present in the exact allowlist.',
    );
  }
  const findingSynthesisTimeoutMs = parseInteger(
    configuredValue(environment, OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV),
    defaultTimeoutMs,
    100,
    30_000,
    OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV,
  );
  const maximumOutputTokens = parseInteger(
    configuredValue(environment, OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV),
    defaultMaximumOutputTokens,
    64,
    2_048,
    OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV,
  );
  if (options.environment !== undefined && options.createOpenAiSynthesizer === undefined) {
    throw new OperationsAgentFindingModelConfigurationError(
      'A custom Environment requires an explicit Provider factory.',
    );
  }
  const factory = options.createOpenAiSynthesizer ?? createOpenAiFindingSynthesizer;
  const findingSynthesizer = factory({
    model,
    requestTimeoutMs: findingSynthesisTimeoutMs,
    maxOutputTokens: maximumOutputTokens,
  });
  return Object.freeze({
    status: 'ENABLED',
    provider: 'openai',
    model,
    findingSynthesisTimeoutMs,
    maximumOutputTokens,
    findingSynthesizer,
  });
};

export const createEnvironmentConfiguredSiteNightEnergyInvestigationCoordinator = (
  ports: SiteNightEnergyInvestigationCoordinatorPorts,
  options: EnvironmentConfiguredSiteNightEnergyCoordinatorOptions = {},
): SiteNightEnergyInvestigationCoordinator => {
  const runtime = createOperationsAgentFindingModelRuntimeFromEnvironment(options);
  if (runtime.status === 'DISABLED') {
    return createSiteNightEnergyInvestigationCoordinator(ports, options.policy);
  }
  if (ports.findingSynthesizer !== undefined) {
    throw new OperationsAgentFindingModelConfigurationError(
      'Finding Synthesizer was configured both explicitly and through the environment.',
    );
  }
  const configuredTimeout = options.policy?.findingSynthesisTimeoutMs;
  if (configuredTimeout !== undefined && configuredTimeout !== runtime.findingSynthesisTimeoutMs) {
    throw new OperationsAgentFindingModelConfigurationError(
      'Finding synthesis timeout conflicts with the environment configuration.',
    );
  }
  return createSiteNightEnergyInvestigationCoordinator(
    { ...ports, findingSynthesizer: runtime.findingSynthesizer },
    { ...options.policy, findingSynthesisTimeoutMs: runtime.findingSynthesisTimeoutMs },
  );
};

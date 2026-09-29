import { randomUUID } from 'node:crypto';

import type { AgentRunBudget } from '../../agent/index.js';
import {
  createAgentSessionLifecycle,
  createAgentSessionService,
  type AgentSessionService,
} from '../../application/index.js';
import {
  createPostgresOperationsAgentPersistence,
  type PostgresOperationsAgentPersistence,
  type PostgresOperationsAgentPersistenceOptions,
} from '../../persistence/index.js';
import {
  createProductionPiAgentRuntimeFromEnvironment,
  type PiAgentModelPolicy,
  type PiModelEnvironment,
} from '../../runtime-pi/index.js';
import {
  createEnergyAnalyticsOwnerReader,
  createGatewayToolAuthorizationReader,
  createHvacReadTools,
  createRegistryOwnerReader,
  type EnergyAnalyticsOwnerReaderConfig,
  type GatewayToolAuthorizationReaderConfig,
  type RegistryOwnerReaderConfig,
} from '../../tools/index.js';
import {
  createAgentSessionEventStreamResponse,
  createAgentSessionHttpHandler,
  type AgentSessionHttpAuthorizer,
  type AgentSessionHttpHandler,
} from '../../transport-http/index.js';

export interface ProductionAgentSessionOwnerConfig {
  readonly registry: RegistryOwnerReaderConfig;
  readonly energyAnalytics: EnergyAnalyticsOwnerReaderConfig;
  readonly gatewayToolAuthorization: GatewayToolAuthorizationReaderConfig;
}

export interface ProductionAgentSessionRuntimeOptions {
  readonly environment: PiModelEnvironment;
  readonly persistence: PostgresOperationsAgentPersistenceOptions;
  readonly owners: ProductionAgentSessionOwnerConfig;
  readonly authorizer: AgentSessionHttpAuthorizer;
  readonly budget: AgentRunBudget;
  readonly now?: () => number;
  readonly nextId?: (kind: 'session' | 'run' | 'message' | 'artifact') => string;
}

export interface ProductionAgentSessionRuntime {
  readonly service: AgentSessionService;
  readonly handler: AgentSessionHttpHandler;
  readonly modelPolicy: PiAgentModelPolicy;
  readonly persistence: PostgresOperationsAgentPersistence;
  close(): Promise<void>;
}

const requireBudget = (budget: AgentRunBudget): AgentRunBudget => {
  for (const [name, value] of Object.entries(budget)) {
    if (!Number.isSafeInteger(value) || value <= 0) {
      throw new Error(`Agent Run budget ${name} must be a positive safe integer.`);
    }
  }
  return Object.freeze({ ...budget });
};

export const createProductionAgentSessionRuntime = async (
  options: ProductionAgentSessionRuntimeOptions,
): Promise<ProductionAgentSessionRuntime> => {
  const modelRuntime = await createProductionPiAgentRuntimeFromEnvironment({
    environment: options.environment,
  });
  const persistence = createPostgresOperationsAgentPersistence(options.persistence);
  try {
    const registryReader = createRegistryOwnerReader(options.owners.registry);
    const energyAnalyticsReader = createEnergyAnalyticsOwnerReader(options.owners.energyAnalytics);
    const toolAuthorizationReader = createGatewayToolAuthorizationReader(
      options.owners.gatewayToolAuthorization,
    );
    const lifecycle = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });
    const service = createAgentSessionService({
      lifecycle,
      engine: modelRuntime.engine,
      modelRef: modelRuntime.modelRef,
      budget: requireBudget(options.budget),
      now: options.now ?? Date.now,
      nextId: options.nextId ?? (() => randomUUID()),
      createTools: (context) => createHvacReadTools({
        capabilities: context.capabilities,
        authorization: context.authorization,
        toolAuthorizationReader,
        registryReader,
        energyAnalyticsReader,
      }),
    });
    const handler = createAgentSessionHttpHandler({
      authorizer: options.authorizer,
      service,
      createEventStreamResponse: createAgentSessionEventStreamResponse,
    });
    return Object.freeze({
      service,
      handler,
      modelPolicy: modelRuntime.policy,
      persistence,
      close: () => persistence.close(),
    });
  } catch (error) {
    await persistence.close();
    throw error;
  }
};

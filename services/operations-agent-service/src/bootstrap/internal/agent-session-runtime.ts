import { randomUUID } from 'node:crypto';

import type { AgentRunBudget } from '../../agent/index.js';
import {
  createAgentSessionLifecycle,
  createAgentSessionService,
  type AgentSessionService,
} from '../../application/index.js';
import {
  createPostgresAgentSessionPersistence,
  type PostgresAgentSessionPersistence,
  type PostgresAgentSessionPersistenceOptions,
} from '../../persistence/index.js';
import {
  createProductionPiAgentRuntimeFromEnvironment,
  type PiAgentModelPolicy,
  type PiModelEnvironment,
} from '../../runtime-pi/index.js';
import {
  createAlarmOwnerReader,
  createFddOwnerReader,
  createWorkOrderOwnerReader,
  createDeviceTelemetryOwnerReader,
  createEnergyAnalyticsOwnerReader,
  createSemanticQueryOwnerReader,
  createTelemetryHistoryOwnerReader,
  createGatewayToolAuthorizationReader,
  createHvacReadTools,
  createSiteDescriber,
  createRegistryOwnerReader,
  type AlarmOwnerReaderConfig,
  type FddOwnerReaderConfig,
  type WorkOrderOwnerReaderConfig,
  type DeviceTelemetryOwnerReaderConfig,
  type EnergyAnalyticsOwnerReaderConfig,
  type GatewayToolAuthorizationReaderConfig,
  type RegistryOwnerReaderConfig,
} from '../../tools/index.js';
import { createAgentSessionEventStreamResponse } from '../../transport-events/index.js';
import {
  createAgentSessionHttpHandler,
  type AgentSessionHttpAuthorizer,
  type AgentSessionHttpHandler,
} from '../../transport-http/index.js';

export type { AgentSessionHttpAuthorizationInput, AgentSessionHttpAuthorizer } from '../../transport-http/index.js';

export interface ProductionAgentSessionOwnerConfig {
  readonly registry: RegistryOwnerReaderConfig;
  readonly deviceTelemetry: DeviceTelemetryOwnerReaderConfig;
  /** Energy Series and device history, both served by the Telemetry Query Service. */
  readonly telemetryQuery: EnergyAnalyticsOwnerReaderConfig;
  readonly gatewayToolAuthorization: GatewayToolAuthorizationReaderConfig;
  readonly alarm: AlarmOwnerReaderConfig;
  readonly fdd: FddOwnerReaderConfig;
  readonly workOrder: WorkOrderOwnerReaderConfig;
}

export interface ProductionAgentSessionRuntimeOptions {
  readonly environment: PiModelEnvironment;
  readonly persistence: PostgresAgentSessionPersistenceOptions;
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
  readonly persistence: PostgresAgentSessionPersistence;
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
  const persistence = createPostgresAgentSessionPersistence(options.persistence);
  try {
    const registryReader = createRegistryOwnerReader(options.owners.registry);
    const deviceTelemetryReader = createDeviceTelemetryOwnerReader(options.owners.deviceTelemetry);
    const energyAnalyticsReader = createEnergyAnalyticsOwnerReader(options.owners.telemetryQuery);
    const semanticQueryReader = createSemanticQueryOwnerReader(options.owners.telemetryQuery);
    const alarmReader = createAlarmOwnerReader(options.owners.alarm);
    const fddReader = createFddOwnerReader(options.owners.fdd);
    const workOrderReader = createWorkOrderOwnerReader(options.owners.workOrder);
    const telemetryHistoryReader = createTelemetryHistoryOwnerReader(options.owners.telemetryQuery);
    const toolAuthorizationReader = createGatewayToolAuthorizationReader(
      options.owners.gatewayToolAuthorization,
    );
    const budget = requireBudget(options.budget);
    const lifecycle = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });
    const service = createAgentSessionService({
      lifecycle,
      engine: modelRuntime.engine,
      modelRef: modelRuntime.modelRef,
      budget,
      now: options.now ?? Date.now,
      nextId: options.nextId ?? (() => randomUUID()),
      createTools: (context) => createHvacReadTools({
        capabilities: context.capabilities,
        authorization: context.authorization,
        toolAuthorizationReader,
        registryReader,
        deviceTelemetryReader,
        telemetryHistoryReader,
        energyAnalyticsReader,
        semanticQueryReader,
        alarmReader,
        fddReader,
        workOrderReader,
        limits: { maxEnergyRangeMs: budget.maxQueryRangeMs, maxResultRecords: budget.maxToolResultRecords },
      }),
      describeSite: (context, run) => createSiteDescriber({
        authorization: context.authorization,
        toolAuthorizationReader,
        registryReader,
      })(run),
    });
    const handler = createAgentSessionHttpHandler({
      usage: persistence.agentUsageReader,
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

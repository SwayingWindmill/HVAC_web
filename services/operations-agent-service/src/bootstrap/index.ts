import { applicationModule } from '../application/index.js';
import { domainModule } from '../domain/index.js';
import { modelModule } from '../model/index.js';
import {
  createMemoryOperationsTelemetryExporter,
  createOperationsOtlpHttpExporter,
  createOperationsTelemetryRuntime,
  hashOperationsTelemetryIdentity,
  observabilityModule,
  type OperationsOtlpHttpExporterOptions,
  type OperationsTelemetryExporter,
  type OperationsTelemetryMetricPoint,
  type OperationsTelemetryRuntime,
  type OperationsTelemetryRuntimeDiagnostics,
  type OperationsTelemetryRuntimeOptions,
  type OperationsTelemetrySpanData,
} from '../observability/index.js';
import { persistenceModule } from '../persistence/index.js';
import { runtimeLanggraphModule } from '../runtime-langgraph/index.js';
import { schedulingModule } from '../scheduling/index.js';
import { toolsModule } from '../tools/index.js';
import {
  createOperationsAgUiEventStreamResponse,
  transportAgUiModule,
} from '../transport-ag-ui/index.js';
import {
  createAgentSessionEventStreamResponse,
  createAgentSessionHttpHandler as createAgentSessionHttpTransportHandler,
  createOperationsAgentHttpHandler as createOperationsAgentHttpTransportHandler,
  transportHttpModule,
  type AgentSessionHttpAuthorizationInput,
  type AgentSessionHttpAuthorizer,
  type AgentSessionHttpHandler,
  type AgentSessionHttpOptions,
  type OperationsAgentHttpAuthorizationInput,
  type OperationsAgentHttpAuthorizer,
  type OperationsAgentHttpCoordinatorContext,
  type OperationsAgentHttpHandler,
  type OperationsAgentHttpOptions,
} from '../transport-http/index.js';

export {
  createMemoryOperationsTelemetryExporter,
  createOperationsOtlpHttpExporter,
  createOperationsTelemetryRuntime,
  hashOperationsTelemetryIdentity,
};

export type {
  OperationsOtlpHttpExporterOptions,
  OperationsTelemetryExporter,
  OperationsTelemetryMetricPoint,
  OperationsTelemetryRuntime,
  OperationsTelemetryRuntimeDiagnostics,
  OperationsTelemetryRuntimeOptions,
  OperationsTelemetrySpanData,
};

export type {
  AgentSessionHttpAuthorizationInput,
  AgentSessionHttpAuthorizer,
  AgentSessionHttpHandler,
  AgentSessionHttpOptions,
  OperationsAgentHttpAuthorizationInput,
  OperationsAgentHttpAuthorizer,
  OperationsAgentHttpCoordinatorContext,
  OperationsAgentHttpHandler,
  OperationsAgentHttpOptions,
};

export const createAgentSessionHttpHandler = (
  options: Omit<AgentSessionHttpOptions, 'createEventStreamResponse'>,
): AgentSessionHttpHandler => createAgentSessionHttpTransportHandler({
  ...options,
  createEventStreamResponse: createAgentSessionEventStreamResponse,
});

export const createOperationsAgentHttpHandler = (
  options: OperationsAgentHttpOptions,
): OperationsAgentHttpHandler => createOperationsAgentHttpTransportHandler({
  ...options,
  createAgUiEventStreamResponse: createOperationsAgUiEventStreamResponse,
});

export {
  createProductionAgentSessionRuntime,
  type ProductionAgentSessionOwnerConfig,
  type ProductionAgentSessionRuntime,
  type ProductionAgentSessionRuntimeOptions,
} from './internal/agent-session-runtime.js';

export {
  OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_PROVIDER_ENV,
  OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS_ENV,
  OperationsAgentFindingModelConfigurationError,
  createEnvironmentConfiguredSiteNightEnergyInvestigationCoordinator,
  createOperationsAgentFindingModelRuntimeFromEnvironment,
  type EnvironmentConfiguredSiteNightEnergyCoordinatorOptions,
  type OperationsAgentEnvironment,
  type OperationsAgentFindingModelRuntime,
  type OperationsAgentFindingModelRuntimeOptions,
} from './internal/finding-model-runtime.js';

export const bootstrapModule = Object.freeze({
  name: 'bootstrap',
  layer: 'composition',
  dependencies: [
    domainModule.name,
    applicationModule.name,
    runtimeLanggraphModule.name,
    modelModule.name,
    toolsModule.name,
    persistenceModule.name,
    transportHttpModule.name,
    transportAgUiModule.name,
    schedulingModule.name,
    observabilityModule.name,
  ],
} as const);

export const operationsAgentServiceModules = Object.freeze([
  domainModule,
  applicationModule,
  runtimeLanggraphModule,
  modelModule,
  toolsModule,
  persistenceModule,
  transportHttpModule,
  transportAgUiModule,
  schedulingModule,
  observabilityModule,
  bootstrapModule,
] as const);

export type OperationsAgentServiceModules = typeof operationsAgentServiceModules;

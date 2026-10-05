import {
  createMemoryOperationsTelemetryExporter,
  createOperationsOtlpHttpExporter,
  createOperationsTelemetryRuntime,
  hashOperationsTelemetryIdentity,
  type OperationsOtlpHttpExporterOptions,
  type OperationsTelemetryExporter,
  type OperationsTelemetryMetricPoint,
  type OperationsTelemetryRuntime,
  type OperationsTelemetryRuntimeDiagnostics,
  type OperationsTelemetryRuntimeOptions,
  type OperationsTelemetrySpanData,
} from '../observability/index.js';
import {
  createAgentSessionEventStreamResponse,
  createOperationsAgentEventStreamResponse,
} from '../transport-events/index.js';
import {
  createAgentSessionHttpHandler as createAgentSessionHttpTransportHandler,
  createOperationsAgentHttpHandler as createOperationsAgentHttpTransportHandler,
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
  createAgentEventStreamResponse: createOperationsAgentEventStreamResponse,
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

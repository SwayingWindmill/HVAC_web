import { applicationModule } from '../application/index.js';

export {
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
} from './internal/operations-telemetry-runtime.js';

export const observabilityModule = Object.freeze({
  name: 'observability',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type ObservabilityModule = typeof observabilityModule;

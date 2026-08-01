import { applicationModule } from '../application/index.js';

export {
  createOperationsAgUiEventStreamResponse,
  encodeOperationsAgUiEventStream,
  projectOperationsInvestigationToAgUiEventBatch,
  projectOperationsInvestigationToAgUiEvents,
  type OperationsAgUiEvent,
  type OperationsAgUiEventBatch,
  type OperationsAgUiEventFrame,
  type OperationsInvestigationStateSnapshot,
  type OperationsStreamRecoveryMode,
  type OperationsStreamRecoveryReason,
  type OperationsPlanStepStatus,
  type OperationsPlanStepView,
  type OperationsPlanView,
  type OperationsToolActivityView,
} from './internal/operations-investigation-events.js';

export const transportAgUiModule = Object.freeze({
  name: 'transport-ag-ui',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type TransportAgUiModule = typeof transportAgUiModule;

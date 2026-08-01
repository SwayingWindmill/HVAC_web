import { applicationModule } from '../application/index.js';

export {
  createOperationsAgUiEventStreamResponse,
  encodeOperationsAgUiEventStream,
  projectOperationsInvestigationToAgUiEvents,
  type OperationsAgUiEvent,
  type OperationsInvestigationStateSnapshot,
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

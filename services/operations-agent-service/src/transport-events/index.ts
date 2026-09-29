import { applicationModule } from '../application/index.js';

export { createAgentSessionEventStreamResponse } from './internal/agent-session-events.js';

export {
  createOperationsAgentEventStreamResponse,
  encodeOperationsAgentEventStream,
  projectOperationsInvestigationToAgentEventBatch,
  projectOperationsInvestigationToAgentEvents,
  type OperationsAgentEvent,
  type OperationsAgentEventBatch,
  type OperationsAgentEventFrame,
  type OperationsInvestigationStateSnapshot,
  type OperationsStreamRecoveryMode,
  type OperationsStreamRecoveryReason,
  type OperationsPlanStepStatus,
  type OperationsPlanStepView,
  type OperationsPlanView,
  type OperationsToolActivityView,
} from './internal/operations-investigation-events.js';

export const transportEventsModule = Object.freeze({
  name: 'transport-events',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type TransportEventsModule = typeof transportEventsModule;

import { applicationModule } from '../application/index.js';

export {
  createOperationsAgentHttpHandler,
  type OperationsAgentHttpAuthorizationInput,
  type OperationsAgentHttpAuthorizer,
  type OperationsAgentHttpCoordinatorContext,
  type OperationsAgentHttpHandler,
  type OperationsAgentHttpOptions,
} from './internal/operations-agent-http.js';

export const transportHttpModule = Object.freeze({
  name: 'transport-http',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type TransportHttpModule = typeof transportHttpModule;

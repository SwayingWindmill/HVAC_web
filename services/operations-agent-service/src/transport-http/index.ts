import { applicationModule } from '../application/index.js';

export { createAgentSessionEventStreamResponse } from './internal/agent-session-events.js';

export {
  createAgentSessionHttpHandler,
  type AgentSessionHttpAuthorizationInput,
  type AgentSessionHttpAuthorizer,
  type AgentSessionHttpHandler,
  type AgentSessionHttpOptions,
} from './internal/agent-session-http.js';

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

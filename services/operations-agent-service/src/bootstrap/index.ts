import { applicationModule } from '../application/index.js';
import { domainModule } from '../domain/index.js';
import { modelModule } from '../model/index.js';
import { observabilityModule } from '../observability/index.js';
import { persistenceModule } from '../persistence/index.js';
import { runtimeLanggraphModule } from '../runtime-langgraph/index.js';
import { schedulingModule } from '../scheduling/index.js';
import { toolsModule } from '../tools/index.js';
import { transportAgUiModule } from '../transport-ag-ui/index.js';
import { transportHttpModule } from '../transport-http/index.js';

export {
  createOperationsAgentHttpHandler,
  type OperationsAgentHttpAuthorizationInput,
  type OperationsAgentHttpAuthorizer,
  type OperationsAgentHttpCoordinatorContext,
  type OperationsAgentHttpHandler,
  type OperationsAgentHttpOptions,
} from '../transport-http/index.js';

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

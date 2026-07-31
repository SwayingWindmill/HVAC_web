import { applicationModule } from '../application/index.js';

export {
  createLangGraphAgentExecutionRuntime,
} from './internal/langgraph-agent-execution-runtime.js';
export {
  LangGraphRuntimeError,
  type LangGraphReadStep,
  type LangGraphRuntimeErrorCode,
  type LangGraphRuntimeProgram,
} from './internal/runtime-state.js';

export const runtimeLanggraphModule = Object.freeze({
  name: 'runtime-langgraph',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type RuntimeLanggraphModule = typeof runtimeLanggraphModule;

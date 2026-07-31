import { applicationModule } from '../application/index.js';

export const runtimeLanggraphModule = Object.freeze({
  name: 'runtime-langgraph',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type RuntimeLanggraphModule = typeof runtimeLanggraphModule;

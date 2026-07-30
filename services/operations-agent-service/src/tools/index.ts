import { applicationModule } from '../application/index.js';

export const toolsModule = Object.freeze({
  name: 'tools',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type ToolsModule = typeof toolsModule;

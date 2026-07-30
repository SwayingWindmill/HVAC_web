import { applicationModule } from '../application/index.js';

export const observabilityModule = Object.freeze({
  name: 'observability',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type ObservabilityModule = typeof observabilityModule;

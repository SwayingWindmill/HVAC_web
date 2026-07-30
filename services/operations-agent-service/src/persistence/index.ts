import { applicationModule } from '../application/index.js';

export const persistenceModule = Object.freeze({
  name: 'persistence',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type PersistenceModule = typeof persistenceModule;

import { domainModule } from '../domain/index.js';

export const applicationModule = Object.freeze({
  name: 'application',
  layer: 'application',
  dependencies: [domainModule.name],
} as const);

export type ApplicationModule = typeof applicationModule;

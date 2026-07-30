import { applicationModule } from '../application/index.js';

export const transportAgUiModule = Object.freeze({
  name: 'transport-ag-ui',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type TransportAgUiModule = typeof transportAgUiModule;

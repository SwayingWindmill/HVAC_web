import { applicationModule } from '../application/index.js';

export const schedulingModule = Object.freeze({
  name: 'scheduling',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type SchedulingModule = typeof schedulingModule;

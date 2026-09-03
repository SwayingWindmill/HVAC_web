export const runtimePiModule = Object.freeze({
  name: 'runtime-pi',
  layer: 'adapter',
  dependencies: ['agent'],
} as const);

export type RuntimePiModule = typeof runtimePiModule;

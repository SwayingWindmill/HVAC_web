export const domainModule = Object.freeze({
  name: 'domain',
  layer: 'domain',
  dependencies: [],
} as const);

export type DomainModule = typeof domainModule;

import { applicationModule } from '../application/index.js';

export {
  createPostgresOperationsAgentPersistence,
  type PostgresCheckpointRepository,
  type PostgresOperationsAgentPersistence,
  type PostgresOperationsAgentPersistenceOptions,
} from './internal/postgres-persistence.js';

export const persistenceModule = Object.freeze({
  name: 'persistence',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type PersistenceModule = typeof persistenceModule;

import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(process.cwd(), process.cwd().endsWith('operations-agent-service') ? '../..' : '.');
const target = process.argv[2];
const targets = Object.freeze({
  operations: {
    environmentName: 'OPERATIONS_AGENT_OPERATIONS_MIGRATOR_DATABASE_URL',
    migrationPath: resolve(
      root,
      'services/operations-agent-service/migrations/operations/001_agent_operations.sql',
    ),
  },
  checkpoints: {
    environmentName: 'OPERATIONS_AGENT_CHECKPOINTS_MIGRATOR_DATABASE_URL',
    migrationPath: resolve(
      root,
      'services/operations-agent-service/migrations/checkpoints/001_agent_checkpoints.sql',
    ),
  },
});

if (target !== 'operations' && target !== 'checkpoints') {
  throw new Error('Migration target must be operations or checkpoints.');
}

const configuration = targets[target];
const databaseUrl = process.env[configuration.environmentName];
if (!databaseUrl) {
  throw new Error(`${configuration.environmentName} is required.`);
}

const result = spawnSync('psql', [
  databaseUrl,
  '-v',
  'ON_ERROR_STOP=1',
  '-f',
  configuration.migrationPath,
], {
  cwd: root,
  encoding: 'utf8',
  windowsHide: true,
  stdio: 'inherit',
});

if (result.error || result.status !== 0) {
  throw new Error(`Operations Agent ${target} migration failed.`);
}

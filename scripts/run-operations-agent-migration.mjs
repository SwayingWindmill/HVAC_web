import { spawnSync } from 'node:child_process';
import { readdirSync } from 'node:fs';
import { resolve } from 'node:path';

// Applies the Operations Agent's Session schema with the migrator role.
const root = resolve(process.cwd(), process.cwd().endsWith('operations-agent-service') ? '../..' : '.');
const directory = resolve(root, 'services/operations-agent-service/migrations/operations');
const environmentName = 'OPERATIONS_AGENT_OPERATIONS_MIGRATOR_DATABASE_URL';

if (process.argv[2] !== 'operations') {
  throw new Error('Migration target must be operations.');
}
const databaseUrl = process.env[environmentName];
if (!databaseUrl) {
  throw new Error(`${environmentName} is required.`);
}

for (const name of readdirSync(directory).filter((file) => file.endsWith('.sql')).sort()) {
  const migrationPath = resolve(directory, name);
  const result = spawnSync('psql', [databaseUrl, '-v', 'ON_ERROR_STOP=1', '-f', migrationPath], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    stdio: 'inherit',
  });
  if (result.error || result.status !== 0) {
    throw new Error(`Operations Agent migration failed at ${migrationPath}.`);
  }
}

// identity-service's PostgreSQL tests on a throwaway database: the production database and role
// bootstrap, then every identity migration in manifest order, then the Go tests with the
// runtime, admin and schema-owner logins they need (services/identity-service/internal/identity).
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const container = `hvac-identity-pg-test-${process.pid}`;
const postgresImage = 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412';
const goBinary = process.env.GO_BINARY ?? 'go';
const adminPassword = `identity-test-${randomBytes(18).toString('hex')}`;
const rolePassword = `identity-role-${randomBytes(18).toString('hex')}`;
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function run(command, args) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args[0]} failed: ${result.error?.message ?? result.stderr?.trim() ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

async function freePort() {
  const server = createServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const { port } = server.address();
  await new Promise((resolveClose) => server.close(resolveClose));
  return port;
}

const psqlFile = (database, path) => {
  run('docker', ['cp', resolve(root, path), `${container}:/tmp/migration.sql`]);
  run('docker', ['exec', container, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-q', '-f', '/tmp/migration.sql']);
};
const psql = (database, sql) => run('docker', ['exec', container, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);

async function waitForPostgres() {
  // The image initializes on a server listening on no address, then restarts; wait for the restarted one.
  for (let attempt = 0; attempt < 240; attempt += 1) {
    try {
      if (psql('postgres', "SELECT current_setting('listen_addresses') <> ''") === 't') return;
    } catch {}
    await pause(250);
  }
  throw new Error('identity PostgreSQL did not become ready');
}

const port = await freePort();
try {
  run('docker', ['run', '-d', '--name', container, '-p', `127.0.0.1:${port}:5432`, '-e', `POSTGRES_PASSWORD=${adminPassword}`, postgresImage]);
  await waitForPostgres();
  psqlFile('postgres', 'deploy/platform/phase1/postgres/init/001-create-phase1-databases.sql');
  psqlFile('postgres', 'deploy/platform/phase1/migrations/bootstrap-roles.sql');
  const migrations = (await readFile(resolve(root, 'deploy/platform/phase1/migrations/migration-list.tsv'), 'utf8'))
    .split('\n').map((line) => line.split('|')).filter(([database]) => database === 'hvac_identity');
  for (const [, path] of migrations) psqlFile('hvac_identity', path.trim());
  psql('postgres', `ALTER ROLE identity_runtime WITH PASSWORD '${rolePassword}'; ALTER ROLE identity_admin WITH PASSWORD '${rolePassword}';`);

  const database = (role) => `postgres://${role}:${rolePassword}@127.0.0.1:${port}/hvac_identity?sslmode=disable`;
  const child = spawn(goBinary, ['test', '-count=1', ...process.argv.slice(2), './services/identity-service/internal/identity/...'], {
    cwd: root,
    stdio: 'inherit',
    env: {
      ...process.env,
      GOCACHE: process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache'),
      IDENTITY_TEST_ADMIN_DATABASE_URL: database('identity_admin'),
      IDENTITY_TEST_RUNTIME_DATABASE_URL: database('identity_runtime'),
      // The rollback test takes a grant away from identity_admin and gives it back, as the schema owner would.
      IDENTITY_TEST_MIGRATOR_DATABASE_URL: `postgres://postgres:${adminPassword}@127.0.0.1:${port}/hvac_identity?sslmode=disable`,
    },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal || code !== 0) throw new Error(`identity PostgreSQL tests failed: ${signal ?? code}`);
  console.log('identity PostgreSQL tests passed.');
} finally {
  try { run('docker', ['rm', '-f', container]); } catch {}
}

// A throwaway PostgreSQL with the production database and role bootstrap and every Phase 1
// migration of the named databases, in manifest order, for an owner's Go tests. Logins get
// random passwords that live only as long as the container.
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const postgresImage = 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412';
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

/**
 * Runs `go test` on `packages` against a fresh database. `databases` are the Phase 1 databases
 * whose migrations to apply; `env(url)` names the test variables, where `url(role, database)`
 * is a connection string for a login role (`postgres` is the superuser).
 */
export async function runGoTestsOnPhase1Postgres({ name, databases, logins, packages, env, goArgs = [] }) {
  const container = `hvac-${name}-pg-test-${process.pid}`;
  const superuserPassword = randomBytes(18).toString('hex');
  const loginPassword = randomBytes(18).toString('hex');
  const psql = (database, sql) => run('docker', ['exec', container, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
  const psqlFile = (database, path) => {
    run('docker', ['cp', resolve(root, path), `${container}:/tmp/migration.sql`]);
    run('docker', ['exec', container, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-q', '-f', '/tmp/migration.sql']);
  };
  const port = await freePort();
  try {
    run('docker', ['run', '-d', '--name', container, '-p', `127.0.0.1:${port}:5432`, '-e', `POSTGRES_PASSWORD=${superuserPassword}`, postgresImage]);
    // The image initializes on a server listening on no address, then restarts; wait for the restarted one.
    let ready = false;
    for (let attempt = 0; attempt < 240 && !ready; attempt += 1) {
      try { ready = psql('postgres', "SELECT current_setting('listen_addresses') <> ''") === 't'; } catch {}
      if (!ready) await pause(250);
    }
    if (!ready) throw new Error(`${name} PostgreSQL did not become ready`);
    psqlFile('postgres', 'deploy/platform/phase1/postgres/init/001-create-phase1-databases.sql');
    psqlFile('postgres', 'deploy/platform/phase1/migrations/bootstrap-roles.sql');
    const migrations = (await readFile(resolve(root, 'deploy/platform/phase1/migrations/migration-list.tsv'), 'utf8'))
      .split('\n').map((line) => line.split('|')).filter(([database]) => databases.includes(database));
    for (const [database, path] of migrations) psqlFile(database, path.trim());
    psql('postgres', logins.map((role) => `ALTER ROLE ${role} WITH PASSWORD '${loginPassword}';`).join(' '));

    const url = (role, database) => `postgres://${role}:${role === 'postgres' ? superuserPassword : loginPassword}@127.0.0.1:${port}/${database}?sslmode=disable`;
    const child = spawn(process.env.GO_BINARY ?? 'go', ['test', '-count=1', ...goArgs, ...packages], {
      cwd: root,
      stdio: 'inherit',
      env: { ...process.env, GOCACHE: process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache'), ...env(url) },
    });
    const [code, signal] = await once(child, 'exit');
    if (signal || code !== 0) throw new Error(`${name} PostgreSQL tests failed: ${signal ?? code}`);
    console.log(`${name} PostgreSQL tests passed.`);
  } finally {
    try { run('docker', ['rm', '-f', container]); } catch {}
  }
}

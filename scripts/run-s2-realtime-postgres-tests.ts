import { spawnSync, type SpawnSyncOptions } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.ts';
import { findAvailablePort } from './lib/postgres-compose-harness.ts';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/telemetry/compose.yaml');
const projectName = `hvac-s2-runtime-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
// The snapshot read Redis falls back to runs on the same temporary database.
const reportPath = resolve(root, process.env.S2_RUNTIME_REPORT_PATH ?? 'out/s2-realtime-backend/realtime-postgres.json');
const testPattern = process.env.S2_RUNTIME_TEST_PATTERN ?? 'TestPostgresRealtime|TestPostgresLatestSnapshot';
const ticketNumber = Number(process.env.S2_RUNTIME_TICKET_NUMBER ?? '65');
const pause = (milliseconds: number): Promise<void> => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

const postgresHostPort = await findAvailablePort('S2 Telemetry Runtime PostgreSQL');
const composeEnvironment = { ...process.env, S2_POSTGRES_HOST_PORT: String(postgresHostPort) };
const runtimeURL = `postgres://s2_telemetry_service:s2-telemetry-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const adminURL = `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;

function run(command: string, args: string[], options: SpawnSyncOptions = {}): string {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || String(result.stderr ?? '').trim() || String(result.stdout ?? '').trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args: string[]): string {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function psql(sql: string): string {
  return run('docker', ['exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}

async function waitForPostgres(): Promise<void> {
  let stableStart = '';
  let stableChecks = 0;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = psql("SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('telemetry_runtime.presence_signals') IS NOT NULL)::text || '|' || EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='telemetry_runtime' AND table_name='device_observation_snapshots' AND column_name='state_sha256')::text");
      const [startedAt, signalsReady, digestReady] = state.split('|');
      if (signalsReady === 'true' && digestReady === 'true') {
        if (startedAt === stableStart) stableChecks += 1;
        else {
          stableStart = startedAt;
          stableChecks = 1;
        }
        if (stableChecks >= 4) return;
      } else stableChecks = 0;
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`S2 Telemetry Runtime PostgreSQL fixture did not initialize\n${logs}`);
}

const report: {
  schemaVersion: number;
  ticket: number;
  status: string;
  startedAt: string;
  finishedAt?: string;
  error?: string;
  postgresImage: string;
  assertions: Record<string, unknown>;
} = {
  schemaVersion: 1,
  ticket: ticketNumber,
  status: 'failed',
  startedAt: new Date().toISOString(),
  postgresImage: 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412',
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'postgres']);
  await waitForPostgres();

  const testOutput = run(process.execPath, [
    '--experimental-strip-types',
    'scripts/run-isolated-go.ts',
    '--module=modules/telemetry',
    'test', '-count=1', '-run', testPattern, '-v', './pkg/telemetry/...',
  ], {
    env: {
      ...process.env,
      S2_TELEMETRY_TEST_DATABASE_URL: runtimeURL,
      S2_TELEMETRY_ADMIN_DATABASE_URL: adminURL,
    },
  });
  report.assertions.goIntegration = testOutput;
  report.assertions.runtimeIdentity = psql(`
    SELECT rolname || '|' || rolcanlogin::text || '|' || rolinherit::text || '|' || rolbypassrls::text
    FROM pg_roles WHERE rolname = 's2_telemetry_service'
  `);
  if (report.assertions.runtimeIdentity !== 's2_telemetry_service|true|false|false') {
    throw new Error(`runtime identity drifted: ${report.assertions.runtimeIdentity}`);
  }

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S2 Telemetry Runtime PostgreSQL evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}

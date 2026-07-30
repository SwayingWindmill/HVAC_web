import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s2-telemetry/compose.yaml');
const projectName = `hvac-s2-runtime-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(root, process.env.S2_RUNTIME_REPORT_PATH ?? 'out/s2-telemetry-runtime-snapshot/telemetry-runtime-postgres.json');
const testPattern = process.env.S2_RUNTIME_TEST_PATTERN ?? 'TestPostgresSnapshot';
const ticketNumber = Number(process.env.S2_RUNTIME_TICKET_NUMBER ?? '62');
const realtimeMode = testPattern.includes('Realtime');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const composeInvocation = (() => {
  const plugin = spawnSync('docker', ['compose', 'version'], { stdio: 'ignore', windowsHide: true });
  if (!plugin.error && plugin.status === 0) return { command: 'docker', prefix: ['compose'] };
  return { command: 'docker-compose', prefix: [] };
})();

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('S2 Telemetry Runtime PostgreSQL port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const postgresHostPort = await findAvailablePort();
const composeEnvironment = { ...process.env, S2_POSTGRES_HOST_PORT: String(postgresHostPort) };
const runtimeURL = `postgres://s2_telemetry_service:s2-telemetry-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const adminURL = `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return run(composeInvocation.command, [...composeInvocation.prefix, '-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function psql(sql) {
  return run('docker', ['exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}

async function waitForPostgres() {
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

const report = {
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
    'scripts/run-isolated-go.mjs',
    '--module=services/telemetry-runtime-service',
    'test', '-count=1', '-run', testPattern, '-v', './internal/telemetry/...',
  ], {
    env: {
      ...process.env,
      S2_TELEMETRY_TEST_DATABASE_URL: runtimeURL,
      S2_TELEMETRY_ADMIN_DATABASE_URL: adminURL,
    },
  });
  report.assertions.goIntegration = testOutput;
  if (realtimeMode) {
    report.assertions.realtimeOwnerState = psql(`
      SELECT s.status || '|' || (s.revoked_at IS NOT NULL)::text || '|'
        || (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o
            WHERE o.device_id = s.device_id AND o.delivery_state = 'PUBLISHED')::text
      FROM telemetry_runtime.telemetry_subscriptions s
      WHERE s.client_subscription_id = 'postgres-zone'
    `);
    if (report.assertions.realtimeOwnerState !== 'REVOKED|true|1') {
      throw new Error(`unexpected realtime owner state ${report.assertions.realtimeOwnerState}`);
    }
    report.assertions.currentScopeRecheck = psql(`
      SELECT count(*)::text FROM telemetry_runtime.iam_scope_projections
      WHERE principal_id = '018f2e00-2000-7000-8000-000000000001'
        AND device_id = '018f2e00-3000-7000-8000-000000000001'
        AND action = 'SUBSCRIBE' AND revoked_at IS NOT NULL
    `);
    if (report.assertions.currentScopeRecheck !== '1') {
      throw new Error(`realtime IAM revocation evidence drifted: ${report.assertions.currentScopeRecheck}`);
    }
  } else {
    report.assertions.currentTransaction = psql(`
      SELECT s.business_revision::text || '|' || p.business_revision::text || '|'
        || s.evaluation_availability || '|'
        || (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o WHERE o.device_id = s.device_id AND o.subscription_id IS NULL)::text
      FROM telemetry_runtime.device_observation_snapshots s
      JOIN telemetry_runtime.device_presence p USING (device_id)
      WHERE s.device_id = '018f2e00-3000-7000-8000-000000000001'
    `);
    if (report.assertions.currentTransaction !== '4|4|AVAILABLE|4') {
      throw new Error(`unexpected committed transaction state ${report.assertions.currentTransaction}`);
    }
    report.assertions.twoOrganizationIsolation = psql(`
      SELECT (snapshot ->> 'owningOrganizationId') || '|' || evaluation_availability || '|'
        || (snapshot #>> '{values,0,missingReason}')
      FROM telemetry_runtime.device_observation_snapshots
      WHERE device_id = '018f2e00-3000-7000-8000-000000000003'
    `);
    if (report.assertions.twoOrganizationIsolation !== '018f2e00-0000-7000-8000-000000000002|UNAVAILABLE|NEVER_OBSERVED') {
      throw new Error(`unexpected Organization B state ${report.assertions.twoOrganizationIsolation}`);
    }
  }
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

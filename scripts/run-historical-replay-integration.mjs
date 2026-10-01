import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/telemetry/compose.yaml');
const projectName = `hvac-historical-replay-${process.pid}`;
const reportPath = resolve(root, process.env.HISTORICAL_REPLAY_REPORT_PATH ?? 'out/historical-replay/integration.json');
const postgresImage = 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412';
const clickHouseImage = 'clickhouse/clickhouse-server:26.3.12.3@sha256:1f7cd090d5c4e2b8bfe0ea5d8ae6125937e1d932c6371b4d25fbd6088829dc9c';
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('Historical Replay port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const [postgresHostPort, clickHouseHostPort] = await Promise.all([findAvailablePort(), findAvailablePort()]);
const composeEnvironment = {
  ...process.env,
  S2_POSTGRES_HOST_PORT: String(postgresHostPort),
  S2_CLICKHOUSE_HTTP_HOST_PORT: String(clickHouseHostPort),
};
const runtimeURL = `postgres://s2_telemetry_service:s2-telemetry-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const historyURL = `postgres://s2_telemetry_history_service:s2-telemetry-history-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const adminURL = `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const clickHouseURL = `http://127.0.0.1:${clickHouseHostPort}`;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function container(service) {
  return compose(['ps', '-q', service]);
}

function psql(sql) {
  return run('docker', ['exec', container('postgres'), 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}

function clickHouse(sql) {
  return run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'telemetry_history', '--query', sql]);
}

async function waitForServices() {
  let stableChecks = 0;
  for (let attempt = 0; attempt < 360; attempt += 1) {
    try {
      const postgresReady = psql(`SELECT (to_regclass('telemetry_runtime.telemetry_history_outbox') IS NOT NULL)::text || '|' || (position('HISTORY_REPLAY' in pg_get_constraintdef(oid)) > 0)::text FROM pg_constraint WHERE conname='source_observations_source_path_check'`);
      const clickHouseReady = clickHouse(`SELECT count() FROM system.tables WHERE database = 'telemetry_history' AND name IN ('observations', 'numeric_hourly_states', 'numeric_hourly')`);
      if (postgresReady === 'true|true' && clickHouseReady === '3') {
        stableChecks += 1;
        if (stableChecks >= 3) return;
      } else stableChecks = 0;
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres', 'clickhouse']); } catch (error) { logs = String(error); }
  throw new Error(`Historical Replay integration services did not initialize\n${logs}`);
}

function runTelemetryTest(name) {
  return run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=modules/telemetry',
    'test', '-count=1', '-run', `^${name}$`, '-v', './pkg/telemetry',
  ], {
    env: {
      ...process.env,
      S2_TELEMETRY_TEST_DATABASE_URL: runtimeURL,
      S2_TELEMETRY_ADMIN_DATABASE_URL: adminURL,
      S2_TELEMETRY_HISTORY_DATABASE_URL: historyURL,
      S2_CLICKHOUSE_HTTP_URL: clickHouseURL,
      S2_CLICKHOUSE_USERNAME: 'telemetry_history',
      S2_CLICKHOUSE_PASSWORD: '',
    },
  });
}

const report = {
  schemaVersion: 1,
  ticket: 343,
  capability: 'historical-replay',
  status: 'failed',
  startedAt: new Date().toISOString(),
  postgresImage,
  clickHouseImage,
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', '--pull=never', 'postgres', 'clickhouse']);
  await waitForServices();

  report.assertions.postgresCurrentInvariant = runTelemetryTest('TestPostgresHistoricalReplayPreservesCurrentTruth');
  report.assertions.clickHouseHistoryInvariant = runTelemetryTest('TestPostgresHistoricalReplayProjectsClickHouseWithoutCurrentMutation');

  report.assertions.authoritativeHistory = psql(`
    SELECT count(*)::text || '|'
      || count(*) FILTER (WHERE acceptance_status='ACCEPTED' AND quality='GOOD')::text || '|'
      || count(*) FILTER (WHERE source_path='HISTORY_REPLAY')::text
    FROM telemetry_runtime.source_observations
    WHERE source_partition='tb-history-replay-integration'
  `);
  if (report.assertions.authoritativeHistory !== '1|1|1') {
    throw new Error(`unexpected Historical Replay Postgres evidence ${report.assertions.authoritativeHistory}`);
  }

  report.assertions.historyOutbox = psql(`
    SELECT delivery_state || '|' || attempts::text || '|' || (published_at IS NOT NULL)::text
    FROM telemetry_runtime.telemetry_history_outbox
    WHERE payload ->> 'source_partition'='tb-history-replay-integration'
  `);
  if (report.assertions.historyOutbox !== 'PUBLISHED|1|true') {
    throw new Error(`unexpected Historical Replay outbox state ${report.assertions.historyOutbox}`);
  }

  report.assertions.clickHouse = clickHouse(`
    SELECT count()::String || '|'
      || countIf(acceptance_status='ACCEPTED' AND quality='GOOD')::String || '|'
      || any(source_path) || '|'
      || toString(any(value_number))
    FROM telemetry_history.observations
    WHERE source_partition='tb-history-replay-integration'
  `);
  if (report.assertions.clickHouse !== '1|1|HISTORY_REPLAY|21.5') {
    throw new Error(`unexpected Historical Replay ClickHouse state ${report.assertions.clickHouse}`);
  }

  report.assertions.currentTruth = psql(`
    SELECT l.value::text || '|' || l.business_revision::text || '|'
      || s.business_revision::text || '|'
      || (SELECT count(*) FROM telemetry_runtime.presence_signals p WHERE p.device_id=l.device_id)::text
    FROM telemetry_runtime.latest_accepted_telemetry l
    JOIN telemetry_runtime.device_observation_snapshots s USING (device_id)
    WHERE l.device_id='018f2e00-3000-7000-8000-000000000001'::uuid
      AND l.telemetry_key='zone.temperature'
  `);
  if (report.assertions.currentTruth !== '23.5|1|1|1') {
    throw new Error(`Historical Replay changed Current truth ${report.assertions.currentTruth}`);
  }

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Historical Replay integration evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}

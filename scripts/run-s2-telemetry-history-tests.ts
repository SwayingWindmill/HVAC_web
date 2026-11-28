import { spawnSync, type SpawnSyncOptions } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { pullDockerImageWithRetry, runDockerCompose } from './lib/docker-cli.ts';
import { findAvailablePort } from './lib/postgres-compose-harness.ts';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/telemetry/compose.yaml');
const projectName = `hvac-s2-history-${process.pid}`;
const reportPath = resolve(root, process.env.S2_HISTORY_REPORT_PATH ?? 'out/s2-history/clickhouse-integration.json');
const postgresImage = 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412';
const clickHouseImage = 'clickhouse/clickhouse-server:26.3.39.7@sha256:3a91276f066905da0edbbd622d3fc2a2df632c87ea7fe32ef4e74fdf8c9567b0';
const pause = (milliseconds: number): Promise<void> => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

const [postgresHostPort, clickHouseHostPort, clickHouseNativePort] = await Promise.all([
  findAvailablePort('S2 telemetry history Postgres'),
  findAvailablePort('S2 telemetry history ClickHouse HTTP'),
  findAvailablePort('S2 telemetry history ClickHouse native'),
]);
const composeEnvironment = {
  ...process.env,
  S2_POSTGRES_HOST_PORT: String(postgresHostPort),
  S2_CLICKHOUSE_HTTP_HOST_PORT: String(clickHouseHostPort),
  S2_CLICKHOUSE_NATIVE_HOST_PORT: String(clickHouseNativePort),
};
const runtimeURL = `postgres://s2_telemetry_service:s2-telemetry-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const historyURL = `postgres://s2_telemetry_history_service:s2-telemetry-history-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const adminURL = `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const clickHouseURL = `http://127.0.0.1:${clickHouseHostPort}`;
const clickHouseNativeAddr = `127.0.0.1:${clickHouseNativePort}`;

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

function container(service: string): string {
  return compose(['ps', '-q', service]);
}

function psql(sql: string): string {
  return run('docker', ['exec', container('postgres'), 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}

function clickHouse(sql: string): string {
  return run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'telemetry_history', '--query', sql]);
}

async function waitForServices(): Promise<void> {
  let stableChecks = 0;
  for (let attempt = 0; attempt < 360; attempt += 1) {
    try {
      const postgresReady = psql(`SELECT (to_regclass('telemetry_runtime.telemetry_history_outbox') IS NOT NULL)::text`);
      const ch = await fetch(`${clickHouseURL}/?user=telemetry_history&query=SELECT+count()+FROM+telemetry_history.numeric_gauge_15min_states`);
      if (postgresReady === 'true' && ch.ok) {
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
  throw new Error(`S2 telemetry history fixtures did not initialize\n${logs}`);
}

const report: {
  schemaVersion: number;
  capability: string;
  status: string;
  startedAt: string;
  finishedAt?: string;
  error?: string;
  postgresImage: string;
  clickHouseImage: string;
  assertions: Record<string, string>;
} = {
  schemaVersion: 1,
  capability: 's2-clickhouse-history',
  status: 'failed',
  startedAt: new Date().toISOString(),
  postgresImage,
  clickHouseImage,
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  await pullDockerImageWithRetry(postgresImage, { cwd: root, env: composeEnvironment });
  await pullDockerImageWithRetry(clickHouseImage, { cwd: root, env: composeEnvironment });
  compose(['up', '-d', '--pull=never', 'postgres', 'clickhouse']);
  await waitForServices();

  report.assertions.goIntegration = run(process.execPath, [
    '--experimental-strip-types',
    'scripts/run-isolated-go.ts',
    '--module=modules/telemetry',
    'test', '-count=1', '-run', 'TestPostgresOutboxProjectsClickHouseHistoryDeduplicatesRetry|TestPostgresOutboxProjectsClickHouseNativePrepareBatchDeduplicatesRetry|TestPostgresOutboxDeduplicatesRetransmittedTimestampBeforeClickHouseMV|TestHistoryMicrobatch', '-v', './pkg/telemetry/...',
  ], {
    env: {
      ...process.env,
      S2_TELEMETRY_TEST_DATABASE_URL: runtimeURL,
      S2_TELEMETRY_ADMIN_DATABASE_URL: adminURL,
      S2_TELEMETRY_HISTORY_DATABASE_URL: historyURL,
      S2_CLICKHOUSE_HTTP_URL: clickHouseURL,
      S2_CLICKHOUSE_NATIVE_ADDR: clickHouseNativeAddr,
      S2_CLICKHOUSE_USERNAME: 'telemetry_history',
      S2_CLICKHOUSE_PASSWORD: '',
    },
  });
  if (!report.assertions.goIntegration.includes('--- PASS: TestPostgresOutboxProjectsClickHouseHistoryDeduplicatesRetry') ||
      !report.assertions.goIntegration.includes('--- PASS: TestPostgresOutboxProjectsClickHouseNativePrepareBatchDeduplicatesRetry') ||
      report.assertions.goIntegration.includes('--- SKIP:')) {
    throw new Error('history integration tests must execute, not skip');
  }
  report.assertions.queryClient = run(process.execPath, [
    '--experimental-strip-types',
    'scripts/run-isolated-go.ts',
    '--module=modules/telemetry',
    'test', '-count=1', '-run', 'TestClickHouseHistoryClient', '-v', './internal/history/...',
  ], {
    env: {
      ...process.env,
      HISTORY_QUERY_CLICKHOUSE_TEST_URL: clickHouseURL,
      HISTORY_QUERY_CLICKHOUSE_TEST_USERNAME: 'telemetry_query_history_reader',
    },
  });
  if (!report.assertions.queryClient.includes('--- PASS: TestClickHouseHistoryClientAggregatesHourly') ||
      report.assertions.queryClient.includes('--- SKIP:')) {
    throw new Error('history query client integration tests must execute, not skip');
  }
  report.assertions.outbox = psql(`
    SELECT delivery_state || '|' || attempts::text || '|' || (published_at IS NOT NULL)::text
    FROM telemetry_runtime.telemetry_history_outbox
    WHERE payload ->> 'source_partition' = 'tb-history-clickhouse-integration' AND payload ->> 'source_offset' = '1'
  `);
  if (report.assertions.outbox !== 'PUBLISHED|2|true') {
    throw new Error(`unexpected history outbox state ${report.assertions.outbox}`);
  }
  report.assertions.clickHouse = clickHouse(`
    SELECT count()::String || '|'
      || countIf(acceptance_status = 'ACCEPTED')::String || '|'
      || toString(any(value_number))
    FROM telemetry_history.observations
    WHERE source_partition = 'tb-history-clickhouse-integration' AND source_offset = 1
  `);
  if (report.assertions.clickHouse !== '1|1|24.75') {
    throw new Error(`unexpected ClickHouse history state ${report.assertions.clickHouse}`);
  }
  report.assertions.hourly = clickHouse(`
    SELECT sample_count::String || '|' || toString(average_value)
    FROM telemetry_history.numeric_hourly
    WHERE telemetry_key = 'zone.temperature'
      AND device_id = toUUID('018f2e00-3000-7000-8000-000000000001')
      AND hour = toDateTime('2026-07-29 08:00:00', 'UTC')
  `);
  if (report.assertions.hourly !== '1|24.75') {
    throw new Error(`unexpected ClickHouse hourly state ${report.assertions.hourly}`);
  }

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S2 ClickHouse history evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}

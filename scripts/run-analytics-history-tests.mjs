import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s2-telemetry/compose.yaml');
const projectName = `hvac-analytics-history-${process.pid}`;
const reportPath = resolve(root, process.env.ANALYTICS_HISTORY_REPORT_PATH ?? 'out/analytics-history/clickhouse-integration.json');
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
  if (!address || typeof address === 'string') throw new Error('analytics history port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const clickHouseHostPort = await findAvailablePort();
const composeEnvironment = {
  ...process.env,
  S2_CLICKHOUSE_HTTP_HOST_PORT: String(clickHouseHostPort),
};
const clickHouseURL = `http://127.0.0.1:${clickHouseHostPort}`;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function runExpectFailure(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (!result.error && result.status === 0) {
    throw new Error(`${command} ${args.join(' ')} unexpectedly succeeded`);
  }
  return String(result.stderr ?? result.stdout ?? '').trim().slice(-2000);
}

function compose(args) {
  return run(composeInvocation.command, [...composeInvocation.prefix, '-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function container(service) {
  return compose(['ps', '-q', service]);
}

function clickHouse(sql) {
  return run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'telemetry_history', '--query', sql]);
}

function clickHouseMustFail(sql, user) {
  return runExpectFailure('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', user, '--query', sql]);
}

async function waitForClickHouse() {
  let stableChecks = 0;
  for (let attempt = 0; attempt < 360; attempt += 1) {
    try {
      const tableReady = clickHouse(`SELECT count() FROM system.tables WHERE database = 'analytics' AND name = 'energy_interval_facts'`);
      const userCount = clickHouse(`SELECT count() FROM system.users WHERE name IN ('analytics_projector_reader', 'analytics_projector_writer', 'cube_analytics_reader', 'telemetry_query_history_reader')`);
      if (tableReady === '1' && userCount === '4') {
        stableChecks += 1;
        if (stableChecks >= 3) return;
      } else stableChecks = 0;
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'clickhouse']); } catch (error) { logs = String(error); }
  throw new Error(`analytics ClickHouse model did not initialize\n${logs}`);
}

const report = {
  schemaVersion: 1,
  capability: 'analytics-energy-interval-read-model',
  status: 'failed',
  startedAt: new Date().toISOString(),
  clickHouseImage: 'clickhouse/clickhouse-server:26.3.12.3@sha256:1f7cd090d5c4e2b8bfe0ea5d8ae6125937e1d932c6371b4d25fbd6088829dc9c',
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'clickhouse']);
  await waitForClickHouse();
  report.assertions.goIntegration = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/analytics-read-model-projector',
    'test', '-count=1', '-run', 'TestCumulativeMeterProjectsAdditiveEnergyFactsIdempotently', '-v', './internal/clickhouse/...',
  ], {
    env: {
      ...process.env,
      ANALYTICS_CLICKHOUSE_TEST_URL: clickHouseURL,
      ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME: 'telemetry_history',
    },
  });
  report.assertions.deviceHistoryQuery = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/telemetry-query-service',
    'test', '-count=1', '-run', 'TestClickHouseHistoryClientQueriesBoundedRealProjection', '-v', './internal/history/...',
  ], {
    env: {
      ...process.env,
      HISTORY_QUERY_CLICKHOUSE_TEST_URL: clickHouseURL,
      HISTORY_QUERY_CLICKHOUSE_TEST_USERNAME: 'telemetry_query_history_reader',
    },
  });
  report.assertions.factCount = clickHouse(`SELECT count() FROM analytics.energy_interval_facts`);
  if (report.assertions.factCount !== '2') throw new Error(`unexpected energy interval fact count ${report.assertions.factCount}`);

  const rollupPointId = '01990000-1000-7000-8000-000000000001';
  clickHouse(`INSERT INTO telemetry_history.observations (
    observation_id, tenant_id, owning_organization_id, site_id, device_id, point_id,
    integration_instance_id, source_event_id, source_partition, source_offset, source_path,
    telemetry_key, value_type, unit, value_number, sampled_at, received_at,
    acceptance_status, quality, quality_reasons, payload_sha256
  ) VALUES
    (toUUID('01990000-2000-7000-8000-000000000001'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-4000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000001'), 'rollup-fixture', 1, 'PUSH', 'active_power', 'NUMBER', 'kW', 10, toDateTime64('2026-08-11 10:00:05', 3, 'UTC'), toDateTime64('2026-08-11 10:00:05.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('a', 64)),
    (toUUID('01990000-2000-7000-8000-000000000002'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-4000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000002'), 'rollup-fixture', 2, 'PUSH', 'active_power', 'NUMBER', 'kW', 20, toDateTime64('2026-08-11 10:00:35', 3, 'UTC'), toDateTime64('2026-08-11 10:00:35.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('b', 64)),
    (toUUID('01990000-2000-7000-8000-000000000003'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-4000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000003'), 'rollup-fixture', 3, 'PUSH', 'active_power', 'NUMBER', 'kW', 30, toDateTime64('2026-08-11 10:01:10', 3, 'UTC'), toDateTime64('2026-08-11 10:01:10.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('c', 64)),
    (toUUID('01990000-2000-7000-8000-000000000004'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-4000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000004'), 'rollup-fixture', 4, 'PUSH', 'active_power', 'NUMBER', 'kW', 40, toDateTime64('2026-08-11 10:16:00', 3, 'UTC'), toDateTime64('2026-08-11 10:16:00.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('d', 64))`);

  report.assertions.rollup1Min = clickHouse(`SELECT toString(bucket) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_1min WHERE point_id = toUUID('${rollupPointId}') ORDER BY bucket FORMAT TSVRaw`);
  if (report.assertions.rollup1Min !== '2026-08-11 10:00:00|2|15|10|20\n2026-08-11 10:01:00|1|30|30|30\n2026-08-11 10:16:00|1|40|40|40') throw new Error(`unexpected 1 minute rollup ${report.assertions.rollup1Min}`);
  report.assertions.rollup15Min = clickHouse(`SELECT toString(bucket) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_15min WHERE point_id = toUUID('${rollupPointId}') ORDER BY bucket FORMAT TSVRaw`);
  if (report.assertions.rollup15Min !== '2026-08-11 10:00:00|3|20|10|30\n2026-08-11 10:15:00|1|40|40|40') throw new Error(`unexpected 15 minute rollup ${report.assertions.rollup15Min}`);
  report.assertions.rollupHourly = clickHouse(`SELECT toString(hour) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_hourly WHERE point_id = toUUID('${rollupPointId}') FORMAT TSVRaw`);
  if (report.assertions.rollupHourly !== '2026-08-11 10:00:00|4|25|10|40') throw new Error(`unexpected hourly rollup ${report.assertions.rollupHourly}`);
  report.assertions.rollupDaily = clickHouse(`SELECT toString(bucket) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_daily WHERE point_id = toUUID('${rollupPointId}') FORMAT TSVRaw`);
  if (report.assertions.rollupDaily !== '2026-08-11 00:00:00|4|25|10|40') throw new Error(`unexpected daily rollup ${report.assertions.rollupDaily}`);

  report.assertions.readerCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'analytics_projector_reader', '--query', 'SELECT count() FROM telemetry_history.observations']);
  report.assertions.historyQueryCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'telemetry_query_history_reader', '--query', 'SELECT count() FROM telemetry_history.observations']);
  report.assertions.cubeCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'cube_analytics_reader', '--query', 'SELECT count() FROM analytics.energy_interval_facts']);
  report.assertions.readerCannotInsert = clickHouseMustFail('INSERT INTO analytics.energy_interval_facts (fact_id) VALUES (generateUUIDv4())', 'analytics_projector_reader');
  report.assertions.historyQueryCannotInsert = clickHouseMustFail('INSERT INTO telemetry_history.observations (observation_id) VALUES (generateUUIDv4())', 'telemetry_query_history_reader');
  report.assertions.historyQueryCannotSelectAnalytics = clickHouseMustFail('SELECT count() FROM analytics.energy_interval_facts', 'telemetry_query_history_reader');
  report.assertions.writerCannotSelect = clickHouseMustFail('SELECT count() FROM analytics.energy_interval_facts', 'analytics_projector_writer');
  report.assertions.cubeCannotInsert = clickHouseMustFail('INSERT INTO analytics.energy_interval_facts (fact_id) VALUES (generateUUIDv4())', 'cube_analytics_reader');

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Analytics history evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}

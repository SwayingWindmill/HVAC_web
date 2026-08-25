import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';
import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s2-telemetry/compose.yaml');
const projectName = `hvac-analytics-history-${process.pid}`;
const reportPath = resolve(root, process.env.ANALYTICS_HISTORY_REPORT_PATH ?? 'out/analytics-history/clickhouse-integration.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
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
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
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
    'test', '-count=1', '-run', 'TestCanonicalCounterDeltaProjectsEnergyFactsIdempotently', '-v', './internal/clickhouse/...',
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
  report.assertions.forecastGoIntegration = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/forecast-service',
    'test', '-count=1', '-run', 'TestClickHouseForecastSinkPersistsTraceableBaseline', '-v', './internal/forecast/...',
  ], {
    env: {
      ...process.env,
      FORECAST_CLICKHOUSE_TEST_URL: clickHouseURL,
    },
  });
  report.assertions.factCount = clickHouse(`SELECT count() FROM analytics.energy_interval_facts`);
  if (report.assertions.factCount !== '3') throw new Error(`unexpected energy interval fact count ${report.assertions.factCount}`);

  const rollupPointId = '01990000-1000-7000-8000-000000000001';
  clickHouse(`INSERT INTO telemetry_history.observations (
    observation_id, tenant_id, site_id, device_id, point_id,
    integration_instance_id, source_event_id, source_partition, source_offset, source_path,
    telemetry_key, point_type, point_revision, value_type, unit, value_number, sampled_at, received_at,
    acceptance_status, quality, quality_reasons, payload_sha256
  ) VALUES
    (toUUID('01990000-2000-7000-8000-000000000001'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000001'), 'rollup-fixture', 1, 'PUSH', 'active_power', 'TELEMETRY', 1, 'NUMBER', 'kW', 10, toDateTime64('2026-08-11 10:00:05', 3, 'UTC'), toDateTime64('2026-08-11 10:00:05.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('a', 64)),
    (toUUID('01990000-2000-7000-8000-000000000002'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000002'), 'rollup-fixture', 2, 'PUSH', 'active_power', 'TELEMETRY', 1, 'NUMBER', 'kW', 20, toDateTime64('2026-08-11 10:00:35', 3, 'UTC'), toDateTime64('2026-08-11 10:00:35.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('b', 64)),
    (toUUID('01990000-2000-7000-8000-000000000003'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000003'), 'rollup-fixture', 3, 'PUSH', 'active_power', 'TELEMETRY', 1, 'NUMBER', 'kW', 30, toDateTime64('2026-08-11 10:01:10', 3, 'UTC'), toDateTime64('2026-08-11 10:01:10.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('c', 64)),
    (toUUID('01990000-2000-7000-8000-000000000004'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rollupPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000004'), 'rollup-fixture', 4, 'PUSH', 'active_power', 'TELEMETRY', 1, 'NUMBER', 'kW', 40, toDateTime64('2026-08-11 10:16:00', 3, 'UTC'), toDateTime64('2026-08-11 10:16:00.100', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('d', 64))`);

  report.assertions.rollup1Min = clickHouse(`SELECT toString(bucket) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_1min WHERE point_id = toUUID('${rollupPointId}') ORDER BY bucket FORMAT TSVRaw`);
  if (report.assertions.rollup1Min !== '2026-08-11 10:00:00|2|15|10|20\n2026-08-11 10:01:00|1|30|30|30\n2026-08-11 10:16:00|1|40|40|40') throw new Error(`unexpected 1 minute rollup ${report.assertions.rollup1Min}`);
  report.assertions.rollup15Min = clickHouse(`SELECT toString(bucket) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_15min WHERE point_id = toUUID('${rollupPointId}') ORDER BY bucket FORMAT TSVRaw`);
  if (report.assertions.rollup15Min !== '2026-08-11 10:00:00|3|20|10|30\n2026-08-11 10:15:00|1|40|40|40') throw new Error(`unexpected 15 minute rollup ${report.assertions.rollup15Min}`);
  report.assertions.rollupHourly = clickHouse(`SELECT toString(hour) || '|' || toString(sample_count) || '|' || toString(average_value) || '|' || toString(minimum_value) || '|' || toString(maximum_value) FROM telemetry_history.numeric_hourly WHERE point_id = toUUID('${rollupPointId}') FORMAT TSVRaw`);
  if (report.assertions.rollupHourly !== '2026-08-11 10:00:00|4|25|10|40') throw new Error(`unexpected hourly rollup ${report.assertions.rollupHourly}`);

  const resetCounterPointId = '01990000-1000-7000-8000-000000000010';
  const rolloverCounterPointId = '01990000-1000-7000-8000-000000000020';
  const invalidCounterPointId = '01990000-1000-7000-8000-000000000030';
  const revisionCounterPointId = '01990000-1000-7000-8000-000000000040';
  clickHouse(`INSERT INTO telemetry_history.observations (
    observation_id, tenant_id, site_id, device_id, point_id,
    integration_instance_id, source_event_id, source_partition, source_offset, source_path,
    telemetry_key, point_type, point_revision, counter_decrease_mode, counter_rollover_modulus,
    value_type, unit, value_number, sampled_at, received_at,
    acceptance_status, quality, quality_reasons, payload_sha256
  ) VALUES
    (toUUID('01990000-2000-7000-8000-000000000101'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${resetCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000101'), 'counter-reset', 1, 'PUSH', 'energy_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'kWh', 100, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('1', 64)),
    (toUUID('01990000-2000-7000-8000-000000000103'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${resetCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000103'), 'counter-reset', 3, 'PUSH', 'energy_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'kWh', 5, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('3', 64)),
    (toUUID('01990000-2000-7000-8000-000000000102'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${resetCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000102'), 'counter-reset', 2, 'PUSH', 'energy_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'kWh', 125, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:03:00', 3, 'UTC'), 'OUT_OF_ORDER', 'GOOD', ['OUT_OF_ORDER'], repeat('2', 64)),
    (toUUID('01990000-2000-7000-8000-000000000201'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rolloverCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000201'), 'counter-rollover', 1, 'PUSH', 'water_total', 'COUNTER', 1, 'ROLLOVER', 1000, 'NUMBER', 'm3', 990, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('4', 64)),
    (toUUID('01990000-2000-7000-8000-000000000202'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rolloverCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000202'), 'counter-rollover', 2, 'PUSH', 'water_total', 'COUNTER', 1, 'ROLLOVER', 1000, 'NUMBER', 'm3', 5, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:01:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('5', 64)),
    (toUUID('01990000-2000-7000-8000-000000000203'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${rolloverCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000203'), 'counter-rollover', 3, 'PUSH', 'water_total', 'COUNTER', 1, 'ROLLOVER', 1000, 'NUMBER', 'm3', 20, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('6', 64)),
    (toUUID('01990000-2000-7000-8000-000000000301'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${invalidCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000301'), 'counter-invalid', 1, 'PUSH', 'runtime_total', 'COUNTER', 1, 'INVALID', NULL, 'NUMBER', 'h', 50, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('7', 64)),
    (toUUID('01990000-2000-7000-8000-000000000302'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${invalidCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000302'), 'counter-invalid', 2, 'PUSH', 'runtime_total', 'COUNTER', 1, 'INVALID', NULL, 'NUMBER', 'h', 40, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:01:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('8', 64)),
    (toUUID('01990000-2000-7000-8000-000000000303'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${invalidCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000303'), 'counter-invalid', 3, 'PUSH', 'runtime_total', 'COUNTER', 1, 'INVALID', NULL, 'NUMBER', 'h', 45, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('9', 64)),
    (toUUID('01990000-2000-7000-8000-000000000304'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${invalidCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000304'), 'counter-invalid', 4, 'PUSH', 'runtime_total', 'COUNTER', 1, 'INVALID', NULL, 'NUMBER', 'h', 55, toDateTime64('2026-08-11 11:03:00', 3, 'UTC'), toDateTime64('2026-08-11 11:03:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('a', 64)),
    (toUUID('01990000-2000-7000-8000-000000000401'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${revisionCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000401'), 'counter-revision', 1, 'PUSH', 'cycle_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'count', 10, toDateTime64('2026-08-11 11:00:00', 3, 'UTC'), toDateTime64('2026-08-11 11:00:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('b', 64)),
    (toUUID('01990000-2000-7000-8000-000000000402'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${revisionCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000402'), 'counter-revision', 2, 'PUSH', 'cycle_total', 'COUNTER', 1, 'RESET_TO_ZERO', NULL, 'NUMBER', 'count', 20, toDateTime64('2026-08-11 11:01:00', 3, 'UTC'), toDateTime64('2026-08-11 11:01:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('c', 64)),
    (toUUID('01990000-2000-7000-8000-000000000403'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${revisionCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000403'), 'counter-revision', 3, 'PUSH', 'cycle_total', 'COUNTER', 2, 'RESET_TO_ZERO', NULL, 'NUMBER', 'count', 25, toDateTime64('2026-08-11 11:02:00', 3, 'UTC'), toDateTime64('2026-08-11 11:02:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('d', 64)),
    (toUUID('01990000-2000-7000-8000-000000000404'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), toUUID('01990000-6000-7000-8000-000000000001'), toUUID('${revisionCounterPointId}'), toUUID('01990000-7000-7000-8000-000000000001'), toUUID('01990000-8000-7000-8000-000000000404'), 'counter-revision', 4, 'PUSH', 'cycle_total', 'COUNTER', 2, 'RESET_TO_ZERO', NULL, 'NUMBER', 'count', 30, toDateTime64('2026-08-11 11:03:00', 3, 'UTC'), toDateTime64('2026-08-11 11:03:01', 3, 'UTC'), 'ACCEPTED', 'GOOD', [], repeat('e', 64))`);

  report.assertions.counterResetTransitions = clickHouse(`SELECT transition_type || ':' || ifNull(toString(delta_value), 'NULL') FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${resetCounterPointId}') ORDER BY sampled_at FORMAT TSVRaw`);
  if (report.assertions.counterResetTransitions !== 'INITIAL:NULL\nINCREASE:25\nRESET:5') throw new Error(`unexpected reset Counter transitions ${report.assertions.counterResetTransitions}`);
  report.assertions.counterRolloverTransitions = clickHouse(`SELECT transition_type || ':' || ifNull(toString(delta_value), 'NULL') FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${rolloverCounterPointId}') ORDER BY sampled_at FORMAT TSVRaw`);
  if (report.assertions.counterRolloverTransitions !== 'INITIAL:NULL\nROLLOVER:15\nINCREASE:15') throw new Error(`unexpected rollover Counter transitions ${report.assertions.counterRolloverTransitions}`);
  report.assertions.counterInvalidTransitions = clickHouse(`SELECT transition_type || ':' || ifNull(toString(delta_value), 'NULL') FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${invalidCounterPointId}') ORDER BY sampled_at FORMAT TSVRaw`);
  if (report.assertions.counterInvalidTransitions !== 'INITIAL:NULL\nINVALID_DECREASE:NULL\nINVALID_DECREASE:NULL\nRECOVERY:5') throw new Error(`unexpected invalid Counter transitions ${report.assertions.counterInvalidTransitions}`);
  report.assertions.counterRevisionTransitions = clickHouse(`SELECT transition_type || ':' || ifNull(toString(delta_value), 'NULL') FROM telemetry_history.counter_deltas WHERE point_id = toUUID('${revisionCounterPointId}') ORDER BY sampled_at FORMAT TSVRaw`);
  if (report.assertions.counterRevisionTransitions !== 'INITIAL:NULL\nINCREASE:10\nREVISION_BOUNDARY:NULL\nINCREASE:5') throw new Error(`unexpected Counter revision transitions ${report.assertions.counterRevisionTransitions}`);

  report.assertions.counterHourly = clickHouse(`SELECT toString(point_id) || '|' || toString(delta_sum) || '|' || toString(delta_count) || '|' || toString(reset_count) || '|' || toString(rollover_count) || '|' || toString(excluded_transition_count) FROM telemetry_history.counter_hourly WHERE point_id IN (toUUID('${resetCounterPointId}'), toUUID('${rolloverCounterPointId}'), toUUID('${invalidCounterPointId}'), toUUID('${revisionCounterPointId}')) ORDER BY point_id FORMAT TSVRaw`);
  const expectedCounterHourly = [
    `${resetCounterPointId}|30|2|1|0|0`,
    `${rolloverCounterPointId}|30|2|0|1|0`,
    `${invalidCounterPointId}|5|1|0|0|2`,
    `${revisionCounterPointId}|15|2|0|0|1`,
  ].join('\n');
  if (report.assertions.counterHourly !== expectedCounterHourly) throw new Error(`unexpected Counter hourly rollup ${report.assertions.counterHourly}`);
  report.assertions.counterExcludedFromGenericRollup = clickHouse(`SELECT count() FROM telemetry_history.numeric_hourly WHERE point_id IN (toUUID('${resetCounterPointId}'), toUUID('${rolloverCounterPointId}'), toUUID('${invalidCounterPointId}'), toUUID('${revisionCounterPointId}'))`);
  if (report.assertions.counterExcludedFromGenericRollup !== '0') throw new Error(`Counter leaked into generic numeric rollup: ${report.assertions.counterExcludedFromGenericRollup}`);

  const metricId = '01990000-1500-7000-8000-000000000001';
  const metricVersionId = '01990000-1510-7000-8000-000000000001';
  const metricBindingId = '01990000-1530-7000-8000-000000000001';
  const metricCalculationRunId = '01990000-1540-7000-8000-000000000001';
  clickHouse(`INSERT INTO analytics.metric_result_facts (
    result_id, tenant_id, site_id, subject_type, subject_id,
    metric_id, metric_version_id, metric_code, metric_version,
    metric_binding_id, binding_version, period_start, period_end, calculated_at,
    granularity, value_type, value_json, value_number, value_string, value_boolean,
    unit, quality, completeness, calculation_run_id, revision, provenance
  ) VALUES
    (toUUID('01990000-1550-7000-8000-000000000001'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), 'SITE', toUUID('01990000-5000-7000-8000-000000000001'),
     toUUID('${metricId}'), toUUID('${metricVersionId}'), 'daily_energy', 1,
     toUUID('${metricBindingId}'), 1, toDateTime64('2026-08-10 16:00:00', 3, 'UTC'), toDateTime64('2026-08-11 16:00:00', 3, 'UTC'), toDateTime64('2026-08-11 16:02:00', 3, 'UTC'),
     'DAY', 'NUMBER', '1000', 1000, NULL, NULL, 'kWh', 'PARTIAL', 0.99, toUUID('${metricCalculationRunId}'), 1, '{"reason":"scheduled","source":"counter_deltas"}'),
    (toUUID('01990000-1550-7000-8000-000000000002'), toUUID('01990000-3000-7000-8000-000000000001'), toUUID('01990000-5000-7000-8000-000000000001'), 'SITE', toUUID('01990000-5000-7000-8000-000000000001'),
     toUUID('${metricId}'), toUUID('${metricVersionId}'), 'daily_energy', 1,
     toUUID('${metricBindingId}'), 1, toDateTime64('2026-08-10 16:00:00', 3, 'UTC'), toDateTime64('2026-08-11 16:00:00', 3, 'UTC'), toDateTime64('2026-08-12 02:00:00', 3, 'UTC'),
     'DAY', 'NUMBER', '1012', 1012, NULL, NULL, 'kWh', 'GOOD', 1.0, toUUID('01990000-1540-7000-8000-000000000002'), 2, '{"reason":"late_data_recalculation","previousRevision":1}')`);
  report.assertions.metricResultFactsEngine = clickHouse(`SELECT engine FROM system.tables WHERE database = 'analytics' AND name = 'metric_result_facts'`);
  if (report.assertions.metricResultFactsEngine !== 'MergeTree') throw new Error(`unexpected metric_result_facts engine ${report.assertions.metricResultFactsEngine}`);
  report.assertions.metricResultFactHistory = clickHouse(`SELECT toString(count()) || '|' || arrayStringConcat(arrayMap(value -> toString(value), arraySort(groupArray(revision))), ',') FROM analytics.metric_result_facts WHERE metric_code = 'daily_energy' AND subject_id = toUUID('01990000-5000-7000-8000-000000000001') AND period_start = toDateTime64('2026-08-10 16:00:00', 3, 'UTC') FORMAT TSVRaw`);
  if (report.assertions.metricResultFactHistory !== '2|1,2') throw new Error(`Metric historical recalculation did not preserve both revisions: ${report.assertions.metricResultFactHistory}`);
  report.assertions.metricResultRevisionTwoPayload = clickHouse(`SELECT toString(revision) || '|' || toString(value_number) || '|' || quality || '|' || toString(completeness) || '|' || toString(metric_version) || '|' || toString(binding_version) FROM analytics.metric_result_facts WHERE metric_code = 'daily_energy' AND subject_id = toUUID('01990000-5000-7000-8000-000000000001') AND period_start = toDateTime64('2026-08-10 16:00:00', 3, 'UTC') ORDER BY revision DESC LIMIT 1 FORMAT TSVRaw`);
  if (report.assertions.metricResultRevisionTwoPayload !== '2|1012|GOOD|1|1|1') throw new Error(`Metric revision 2 payload is incorrect: ${report.assertions.metricResultRevisionTwoPayload}`);

  const forecastJobId = '01990000-1700-7000-8000-000000000001';
  report.assertions.forecastSeriesTraceability = clickHouse(`SELECT
    toString(count()) || '|' || toString(min(horizon_minutes)) || '|' || toString(max(horizon_minutes)) || '|'
    || any(quality) || '|' || toString(any(model_version)) || '|' || toString(any(feature_set_version)) || '|'
    || toString(any(deployment_id)) || '|' || toString(any(model_version_id)) || '|' || toString(any(input_snapshot_id)) || '|'
    || toString(any(topology_version_id)) || '|' || toString(countIf(forecast_for > forecast_origin))
    FROM analytics.forecast_series WHERE forecast_job_id = toUUID('${forecastJobId}') FORMAT TSVRaw`);
  const expectedForecastTraceability = '4|15|60|VALID|3|7|01990000-1720-7000-8000-000000000001|01990000-1740-7000-8000-000000000001|01990000-1760-7000-8000-000000000001|01990000-1770-7000-8000-000000000001|4';
  if (report.assertions.forecastSeriesTraceability !== expectedForecastTraceability) throw new Error(`unexpected Forecast traceability ${report.assertions.forecastSeriesTraceability}`);

  report.assertions.readerCanSelectCanonical = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'analytics_projector_reader', '--query', 'SELECT count() FROM telemetry_history.counter_deltas']);
  report.assertions.readerCannotSelectRaw = clickHouseMustFail('SELECT count() FROM telemetry_history.observations', 'analytics_projector_reader');
  report.assertions.historyQueryCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'telemetry_query_history_reader', '--query', 'SELECT count() FROM telemetry_history.observations']);
  report.assertions.cubeCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'cube_analytics_reader', '--query', 'SELECT count() FROM analytics.energy_interval_facts']);
  report.assertions.metricReaderCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'metric_engine_reader', '--query', 'SELECT count() FROM analytics.metric_result_facts']);
  report.assertions.cubeCanSelectMetric = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'cube_analytics_reader', '--query', 'SELECT count() FROM analytics.metric_result_facts']);
  report.assertions.forecastReaderCanSelect = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'forecast_service_reader', '--query', 'SELECT count() FROM analytics.forecast_series']);
  report.assertions.cubeCanSelectForecast = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'cube_analytics_reader', '--query', 'SELECT count() FROM analytics.forecast_series']);
  report.assertions.readerCannotInsert = clickHouseMustFail('INSERT INTO analytics.energy_interval_facts (fact_id) VALUES (generateUUIDv4())', 'analytics_projector_reader');
  report.assertions.metricReaderCannotInsert = clickHouseMustFail("INSERT INTO analytics.metric_result_facts (result_id) VALUES (generateUUIDv4())", 'metric_engine_reader');
  report.assertions.metricWriterCannotSelect = clickHouseMustFail('SELECT count() FROM analytics.metric_result_facts', 'metric_engine_writer');
  report.assertions.metricWriterCanDelete = run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'metric_engine_writer', '--query', "ALTER TABLE analytics.metric_result_facts DELETE WHERE result_id = toUUID('00000000-0000-0000-0000-000000000000') SETTINGS mutations_sync=1"]);
  report.assertions.forecastReaderCannotInsert = clickHouseMustFail('INSERT INTO analytics.forecast_series (forecast_id) VALUES (generateUUIDv4())', 'forecast_service_reader');
  report.assertions.forecastWriterCannotSelect = clickHouseMustFail('SELECT count() FROM analytics.forecast_series', 'forecast_service_writer');
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

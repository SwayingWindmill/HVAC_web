import { createHmac, randomBytes } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';
import { pullDockerImageWithRetry, runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const s2ComposePath = resolve(root, 'infra/s2-telemetry/compose.yaml');
const cubeComposePath = resolve(root, 'semantic/cube/compose.yaml');
const s2ProjectName = `hvac-analytics-cube-source-${process.pid}`;
const cubeProjectName = `hvac-analytics-cube-${process.pid}`;
const reportPath = resolve(root, process.env.ANALYTICS_CUBE_REPORT_PATH ?? 'out/analytics-history/cube-integration.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const cubeImage = 'cubejs/cube:v1.6.51@sha256:bc8c3f27aa588e0bf9c9937ca5bbb37192bc14d78bd18241299c94b9d2ca20e5';
const cubeKey = randomBytes(32).toString('base64url');
const tenantId = '018f4f00-0100-7000-8000-000000000001';
const siteId = '018f4f00-1000-7000-8000-000000000001';
async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('analytics Cube port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const [clickHouseHostPort, cubeHostPort] = await Promise.all([findAvailablePort(), findAvailablePort()]);
const composeEnvironment = {
  ...process.env,
  S2_CLICKHOUSE_HTTP_HOST_PORT: String(clickHouseHostPort),
  CUBE_HOST_PORT: String(cubeHostPort),
  CUBEJS_DB_HOST: 'clickhouse',
  CUBEJS_DB_PORT: '8123',
  CUBEJS_DB_NAME: 'analytics',
  CUBEJS_DB_USER: 'cube_analytics_reader',
  CUBEJS_DB_PASS: '',
  CUBEJS_API_SECRET: cubeKey,
};
const clickHouseURL = `http://127.0.0.1:${clickHouseHostPort}`;
const cubeURL = `http://127.0.0.1:${cubeHostPort}`;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(composePath, projectName, args) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

const s2Compose = (args) => compose(s2ComposePath, s2ProjectName, args);
const cubeCompose = (args) => compose(cubeComposePath, cubeProjectName, args);

function container(service) {
  return s2Compose(['ps', '-q', service]);
}

function connectCubeToClickHouse() {
  const clickHouseContainer = container('clickhouse');
  const cubeContainer = cubeCompose(['ps', '-q', 'cube']);
  const networks = JSON.parse(run('docker', ['inspect', '--format', '{{json .NetworkSettings.Networks}}', clickHouseContainer]));
  const sourceNetwork = Object.keys(networks)[0];
  if (!sourceNetwork) throw new Error('analytics ClickHouse source network was not found');
  run('docker', ['network', 'connect', sourceNetwork, cubeContainer]);
}

function clickHouse(sql) {
  return run('docker', ['exec', container('clickhouse'), 'clickhouse-client', '--user', 'telemetry_history', '--query', sql]);
}

async function waitForClickHouse() {
  for (let attempt = 0; attempt < 360; attempt += 1) {
    try {
      if (clickHouse(`SELECT count() FROM system.tables WHERE database = 'analytics' AND name = 'energy_interval_facts'`) === '1') return;
    } catch {}
    await pause(250);
  }
  throw new Error(`analytics ClickHouse source did not initialize\n${s2Compose(['logs', '--no-color', 'clickhouse'])}`);
}

async function waitForClickHouseHTTP() {
  let lastError;
  for (let attempt = 0; attempt < 120; attempt += 1) {
    try {
      const response = await fetch(`${clickHouseURL}/?query=SELECT%201`, {
        headers: { Authorization: `Basic ${Buffer.from('telemetry_history:').toString('base64')}` },
        signal: AbortSignal.timeout(1000),
      });
      const body = await response.text();
      if (response.ok && body.trim() === '1') return;
      lastError = new Error(`HTTP ${response.status}: ${body.trim()}`);
    } catch (error) {
      lastError = error;
    }
    await pause(250);
  }
  throw new Error(`analytics ClickHouse HTTP endpoint did not become ready: ${lastError}`);
}

const encode = (value) => Buffer.from(JSON.stringify(value)).toString('base64url');

function cubeToken(overrides = {}) {
  const now = Math.floor(Date.now() / 1000);
  const header = encode({ alg: 'HS256', typ: 'JWT' });
  const payload = encode({
    sub: 'analytics-integration-user',
    iat: now,
    exp: now + 300,
    tenantId,
    siteId,
    siteIds: [siteId],
    groups: ['analytics_reader'],
    principalId: 'analytics-integration-user',
    policyRevision: 'analytics-integration:1',
    ...overrides,
  });
  const unsigned = `${header}.${payload}`;
  const signature = createHmac('sha256', cubeKey).update(unsigned).digest('base64url');
  return `${unsigned}.${signature}`;
}

function energyQuery() {
  return {
    measures: [
      'energy_usage.energy_valid_kwh',
      'energy_usage.valid_count',
      'energy_usage.suspect_count',
      'energy_usage.invalid_count',
      'energy_usage.max_data_watermark',
      'energy_usage.max_dataset_revision',
    ],
    filters: [
      { member: 'energy_usage.tenant_id', operator: 'equals', values: [tenantId] },
      { member: 'energy_usage.site_id', operator: 'equals', values: [siteId] },
      { member: 'energy_usage.energy_type', operator: 'equals', values: ['electricity'] },
    ],
    timeDimensions: [{
      dimension: 'energy_usage.period_end',
      dateRange: ['2026-07-29T12:00:00.000Z', '2026-07-29T14:00:00.000Z'],
      granularity: 'hour',
    }],
    order: { 'energy_usage.period_end.hour': 'asc' },
    timezone: 'UTC',
    limit: 100,
  };
}

async function queryCube(token) {
  const response = await fetch(`${cubeURL}/cubejs-api/v1/load`, {
    method: 'POST',
    headers: { Accept: 'application/json', Authorization: token, 'Content-Type': 'application/json' },
    body: JSON.stringify({ query: energyQuery() }),
  });
  const text = await response.text();
  let decoded;
  try { decoded = JSON.parse(text); } catch { decoded = { error: text }; }
  if (!response.ok || decoded.error) throw new Error(`Cube query returned ${response.status}: ${decoded.error ?? text}`);
  return decoded;
}

async function waitForCube() {
  let lastError;
  for (let attempt = 0; attempt < 60; attempt += 1) {
    try { return await queryCube(cubeToken()); } catch (error) { lastError = error; }
    await pause(500);
  }
  let logs = '';
  try {
    const cubeContainer = cubeCompose(['ps', '-q', 'cube']);
    logs = run('docker', ['logs', '--tail', '80', cubeContainer]).slice(-8000);
  } catch (error) {
    logs = String(error).slice(-2000);
  }
  throw new Error(`Cube semantic model did not become queryable: ${lastError}\n${logs}`);
}

const report = {
  schemaVersion: 1,
  capability: 'analytics-cube-energy-query',
  status: 'failed',
  startedAt: new Date().toISOString(),
  cubeImage,
  assertions: {},
};

try {
  try { cubeCompose(['down', '--volumes', '--remove-orphans']); } catch {}
  try { s2Compose(['down', '--volumes', '--remove-orphans']); } catch {}
  s2Compose(['up', '-d', 'clickhouse']);
  await waitForClickHouse();
  await waitForClickHouseHTTP();
  report.assertions.sourceProjection = run(process.execPath, [
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

  await pullDockerImageWithRetry(cubeImage, { cwd: root, env: composeEnvironment, attempts: 3, retryBaseMs: 1000, timeoutMs: 40000 });
  cubeCompose(['up', '-d', 'cube']);
  connectCubeToClickHouse();
  const response = await waitForCube();
  const rows = response.data ?? [];
  if (rows.length !== 2) throw new Error(`unexpected Cube row count ${rows.length}`);
  const [firstHour, secondHour] = rows;
  if (Number(firstHour['energy_usage.energy_valid_kwh']) !== 2 || Number(secondHour['energy_usage.energy_valid_kwh']) !== 2) {
    throw new Error(`unexpected Cube valid energy ${JSON.stringify(rows)}`);
  }
  if (Number(firstHour['energy_usage.valid_count']) !== 1 || Number(secondHour['energy_usage.valid_count']) !== 2 ||
      Number(firstHour['energy_usage.suspect_count']) !== 0 || Number(secondHour['energy_usage.suspect_count']) !== 0 ||
      Number(firstHour['energy_usage.invalid_count']) !== 0 || Number(secondHour['energy_usage.invalid_count']) !== 0) {
    throw new Error(`unexpected Cube quality counts ${JSON.stringify(rows)}`);
  }
  if (Number(firstHour['energy_usage.max_dataset_revision']) !== 1722257880000 || Number(secondHour['energy_usage.max_dataset_revision']) !== 1722258300000) {
    throw new Error(`unexpected Cube revision ${JSON.stringify(rows)}`);
  }
  const firstWatermark = String(firstHour['energy_usage.max_data_watermark']);
  const secondWatermark = String(secondHour['energy_usage.max_data_watermark']);
  const firstWatermarkUTC = firstWatermark.endsWith('Z') ? firstWatermark : `${firstWatermark}Z`;
  const secondWatermarkUTC = secondWatermark.endsWith('Z') ? secondWatermark : `${secondWatermark}Z`;
  if (new Date(firstWatermarkUTC).toISOString() !== '2026-07-29T12:58:00.000Z' ||
      new Date(secondWatermarkUTC).toISOString() !== '2026-07-29T13:05:00.000Z') {
    throw new Error(`unexpected Cube watermark ${JSON.stringify(rows)}`);
  }

  const denied = await queryCube(cubeToken({ siteId: '018f4f00-1000-7000-8000-000000000099', siteIds: ['018f4f00-1000-7000-8000-000000000099'] }));
  if ((denied.data ?? []).length !== 0) throw new Error('Cube row-level policy leaked another Site');
  report.assertions.energyQuery = rows;
  report.assertions.deniedSiteRows = (denied.data ?? []).length;
  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Analytics Cube evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { cubeCompose(['down', '--volumes', '--remove-orphans']); } catch {}
  try { s2Compose(['down', '--volumes', '--remove-orphans']); } catch {}
}

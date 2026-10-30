import { createHmac, randomBytes } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';
import { pullDockerImageWithRetry, runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const s2ComposePath = resolve(root, 'infra/telemetry/compose.yaml');
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

// The Query Service reads the public views only: site_energy is the Site's total, equipment_energy its sub-meters.
function energyQuery({ timezone = 'UTC', dateRange = ['2026-07-29T12:00:00.000Z', '2026-07-29T14:00:00.000Z'], view = 'site_energy' } = {}) {
  return {
    measures: [
      `${view}.energy_valid_kwh`,
      `${view}.valid_count`,
      `${view}.suspect_count`,
      `${view}.invalid_count`,
      `${view}.max_data_watermark`,
      `${view}.max_dataset_revision`,
    ],
    filters: [
      { member: `${view}.site_id`, operator: 'equals', values: [siteId] },
      { member: `${view}.energy_type`, operator: 'equals', values: ['electricity'] },
    ],
    timeDimensions: [{
      dimension: `${view}.period_end`,
      dateRange,
      granularity: 'hour',
    }],
    order: { [`${view}.period_end.hour`]: 'asc' },
    timezone,
    limit: 100,
  };
}

async function cubeMeta(token) {
  const response = await fetch(`${cubeURL}/cubejs-api/v1/meta`, { headers: { Accept: 'application/json', Authorization: token } });
  if (!response.ok) throw new Error(`Cube meta returned ${response.status}: ${await response.text()}`);
  return response.json();
}

// The error Cube answers a query with, or null when it returns data.
async function cubeRefusal(token, query) {
  const response = await fetch(`${cubeURL}/cubejs-api/v1/load`, {
    method: 'POST',
    headers: { Accept: 'application/json', Authorization: token, 'Content-Type': 'application/json' },
    body: JSON.stringify({ query }),
  });
  const decoded = await response.json();
  return decoded.error ?? null;
}

async function queryCube(token, query = energyQuery(), cache = undefined) {
  const response = await fetch(`${cubeURL}/cubejs-api/v1/load`, {
    method: 'POST',
    headers: { Accept: 'application/json', Authorization: token, 'Content-Type': 'application/json' },
    body: JSON.stringify({ query, cache }),
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
    '--module=modules/energy',
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
  if (Number(firstHour['site_energy.energy_valid_kwh']) !== 2 || Number(secondHour['site_energy.energy_valid_kwh']) !== 2) {
    throw new Error(`unexpected Cube valid energy ${JSON.stringify(rows)}`);
  }
  if (Number(firstHour['site_energy.valid_count']) !== 1 || Number(secondHour['site_energy.valid_count']) !== 2 ||
      Number(firstHour['site_energy.suspect_count']) !== 0 || Number(secondHour['site_energy.suspect_count']) !== 0 ||
      Number(firstHour['site_energy.invalid_count']) !== 0 || Number(secondHour['site_energy.invalid_count']) !== 0) {
    throw new Error(`unexpected Cube quality counts ${JSON.stringify(rows)}`);
  }
  if (Number(firstHour['site_energy.max_dataset_revision']) !== 1722257880000 || Number(secondHour['site_energy.max_dataset_revision']) !== 1722258300000) {
    throw new Error(`unexpected Cube revision ${JSON.stringify(rows)}`);
  }
  const firstWatermark = String(firstHour['site_energy.max_data_watermark']);
  const secondWatermark = String(secondHour['site_energy.max_data_watermark']);
  const firstWatermarkUTC = firstWatermark.endsWith('Z') ? firstWatermark : `${firstWatermark}Z`;
  const secondWatermarkUTC = secondWatermark.endsWith('Z') ? secondWatermark : `${secondWatermark}Z`;
  if (new Date(firstWatermarkUTC).toISOString() !== '2026-07-29T12:58:00.000Z' ||
      new Date(secondWatermarkUTC).toISOString() !== '2026-07-29T13:05:00.000Z') {
    throw new Error(`unexpected Cube watermark ${JSON.stringify(rows)}`);
  }

  // Cube reads dateRange and returns buckets as wall time in the query timezone (Telemetry Query Service relies on both).
  const local = (await queryCube(cubeToken(), energyQuery({ timezone: 'Asia/Shanghai', dateRange: ['2026-07-29T20:00:00.000', '2026-07-29T21:59:59.999'] }))).data ?? [];
  if (local.map((row) => `${row['site_energy.period_end.hour']}=${Number(row['site_energy.energy_valid_kwh'])}`).join(',') !==
      '2026-07-29T20:00:00.000=2,2026-07-29T21:00:00.000=2') {
    throw new Error(`unexpected Asia/Shanghai Cube buckets ${JSON.stringify(local)}`);
  }
  if (local.map((row) => String(row['site_energy.max_data_watermark'])).join(',') !== rows.map((row) => String(row['site_energy.max_data_watermark'])).join(',')) {
    throw new Error(`Asia/Shanghai Cube watermark differs from UTC ${JSON.stringify(local)}`);
  }

  // The projected meter is the Site's total, so it has no equipment sub-meter share.
  const branch = (await queryCube(cubeToken(), energyQuery({ view: 'equipment_energy' }))).data ?? [];
  if (branch.length !== 0) throw new Error(`Site meter facts were counted as sub-meters ${JSON.stringify(branch)}`);

  // Only the views are public, and every member carries a Chinese title and description for operators and the Agent.
  const meta = await cubeMeta(cubeToken());
  const published = (meta.cubes ?? []).map((cube) => cube.name).sort();
  if (published.join(',') !== 'equipment_energy,site_energy,site_energy_cost') throw new Error(`Cube publishes ${published.join(',')}`);
  for (const view of meta.cubes) {
    for (const member of [...view.measures, ...view.dimensions]) {
      if (!/[\u4e00-\u9fff]/u.test(member.title ?? '') || !/[\u4e00-\u9fff]/u.test(member.description ?? '')) {
        throw new Error(`${member.name} lacks a Chinese title or description`);
      }
      if (/tenant_id|point_id|sensor_id|accounting_scope|period_start/u.test(member.name)) throw new Error(`${member.name} is not a consumer member`);
    }
  }
  for (const internal of ['site_energy_facts.energy_valid_kwh', 'equipment_energy_facts.energy_valid_kwh', 'site_energy_cost_facts.cost_valid_cny']) {
    const refusal = await cubeRefusal(cubeToken(), { measures: [internal], limit: 1 });
    // Cube 1.6 reports a hidden member with this message (as a 500), so the message is what is checked.
    if (!refusal?.includes(`You requested hidden member: '${internal}'`)) throw new Error(`Cube did not refuse ${internal} as hidden: ${refusal}`);
  }

  // The Query Service prices the Site's electricity through site_energy_cost (#575).
  report.assertions.energyCost = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=modules/telemetry',
    'test', '-count=1', '-run', 'TestCubePricesTheSiteElectricityAtItsPriceWindows', '-v', './internal/cube/...',
  ], {
    env: {
      ...process.env,
      ANALYTICS_CUBE_TEST_URL: cubeURL,
      ANALYTICS_CUBE_TEST_KEY: cubeKey,
      ANALYTICS_CLICKHOUSE_TEST_URL: clickHouseURL,
    },
  });

  // Sub-meters: copies of the Site meter's facts as one machine's BRANCH facts, read per equipment and with suspect data.
  const machine = '018f4f00-4000-7000-8000-000000000011';
  clickHouse(`INSERT INTO analytics.energy_interval_facts SELECT * REPLACE (generateUUIDv4() AS fact_id, generateUUIDv4() AS meter_binding_id, generateUUIDv4() AS source_current_observation_id, 'BRANCH' AS accounting_scope, toUUID('${machine}') AS load_device_id) FROM (SELECT * FROM analytics.energy_interval_facts WHERE site_id = '${siteId}' AND energy_type = 'electricity' AND accounting_scope = 'SITE')`);
  const equipment = (await queryCube(cubeToken(), {
    measures: ['equipment_energy.energy_valid_kwh', 'equipment_energy.energy_valid_and_suspect_kwh'],
    dimensions: ['equipment_energy.equipment_id'],
    filters: [{ member: 'equipment_energy.site_id', operator: 'equals', values: [siteId] }],
    timezone: 'UTC',
    limit: 10,
  })).data ?? [];
  if (equipment.map((row) => `${row['equipment_energy.equipment_id']}=${Number(row['equipment_energy.energy_valid_kwh'])}/${Number(row['equipment_energy.energy_valid_and_suspect_kwh'])}`).join(',') !== `${machine}=4/4`) {
    throw new Error(`unexpected equipment energy ${JSON.stringify(equipment)}`);
  }

  // COP is cooling over electricity in the same bucket: cooling facts at four times the electricity read 4.
  clickHouse(`INSERT INTO analytics.energy_interval_facts SELECT * REPLACE (generateUUIDv4() AS fact_id, generateUUIDv4() AS meter_binding_id, generateUUIDv4() AS source_current_observation_id, 'cooling' AS energy_type, energy_kwh * 4 AS energy_kwh) FROM (SELECT * FROM analytics.energy_interval_facts WHERE site_id = '${siteId}' AND energy_type = 'electricity')`);
  const cop = (await queryCube(cubeToken(), {
    measures: ['site_energy.cop', 'site_energy.cooling_valid_kwh', 'site_energy.electricity_valid_kwh'],
    filters: [{ member: 'site_energy.site_id', operator: 'equals', values: [siteId] }],
    timeDimensions: [{ dimension: 'site_energy.period_end', dateRange: ['2026-07-29T12:00:00.000Z', '2026-07-29T14:00:00.000Z'], granularity: 'hour' }],
    order: { 'site_energy.period_end.hour': 'asc' },
    timezone: 'UTC',
    limit: 10,
  })).data ?? [];
  if (cop.map((row) => `${Number(row['site_energy.electricity_valid_kwh'])}/${Number(row['site_energy.cooling_valid_kwh'])}=${Number(row['site_energy.cop'])}`).join(',') !== '2/8=4,2/8=4') {
    throw new Error(`unexpected Cube COP ${JSON.stringify(cop)}`);
  }

  // site_energy and equipment_energy read whole hours from the hourly rollup and single facts only for
  // an hour the range cuts into (015-energy-hourly-rollup.sql); either way they must equal the facts.
  const factTotals = (from, to, scope = 'SITE') => clickHouse(`SELECT concat(toString(round(sumIf(energy_kwh, quality = 'VALID'), 6)), '/', toString(countIf(quality = 'VALID')), '/', toString(countIf(quality = 'SUSPECT'))) FROM analytics.energy_interval_facts_visible WHERE site_id = '${siteId}' AND accounting_scope = '${scope}' AND energy_type = 'electricity' AND period_end >= parseDateTime64BestEffort('${from}', 3) AND period_end <= parseDateTime64BestEffort('${to}', 3)`);
  const cubeTotals = async (from, to, view = 'site_energy') => {
    const [row = {}] = (await queryCube(cubeToken(), {
      measures: [`${view}.energy_valid_kwh`, `${view}.valid_count`, `${view}.suspect_count`],
      filters: [{ member: `${view}.site_id`, operator: 'equals', values: [siteId] }, { member: `${view}.energy_type`, operator: 'equals', values: ['electricity'] }],
      timeDimensions: [{ dimension: `${view}.period_end`, dateRange: [from, to] }],
      timezone: 'UTC',
      limit: 1,
    // Cube serves a repeated query from its result cache while the refresh key is unchanged (the
    // default key ticks every 10 s), and renewQuery only re-reads that key. The corrected stage repeats
    // the first stage's queries right after an insert, so it must skip the cache altogether.
    }, 'no-cache')).data ?? [];
    return `${Math.round(Number(row[`${view}.energy_valid_kwh`] ?? 0) * 1e6) / 1e6}/${Number(row[`${view}.valid_count`])}/${Number(row[`${view}.suspect_count`])}`;
  };
  const ranges = [
    ['2026-07-29T12:00:00.000Z', '2026-07-29T13:59:59.999Z'],
    ['2026-07-29T12:30:00.000Z', '2026-07-29T13:29:59.999Z'],
    ['2026-07-29T12:58:00.000Z', '2026-07-29T13:05:00.000Z'],
  ];
  const checkRollup = async (stage) => {
    for (const [from, to] of ranges) {
      for (const [view, scope] of [['site_energy', 'SITE'], ['equipment_energy', 'BRANCH']]) {
        const expected = factTotals(from, to, scope);
        const actual = await cubeTotals(from, to, view);
        if (actual !== expected) {
          const hourly = clickHouse(`SELECT concat(toString(round(sumIf(energy_kwh, quality = 'VALID'), 6)), '/', toString(sumIf(interval_count, quality = 'VALID')), '/', toString(sumIf(interval_count, quality = 'SUSPECT'))) FROM analytics.energy_hourly WHERE site_id = '${siteId}' AND accounting_scope = '${scope}' AND energy_type = 'electricity' AND hour >= toStartOfHour(parseDateTime64BestEffort('${from}', 3)) AND hour <= parseDateTime64BestEffort('${to}', 3)`);
          throw new Error(`${stage}: ${view} ${from}..${to} read ${actual}, facts hold ${expected}, the rollup's touched hours hold ${hourly}`);
        }
      }
    }
  };
  await checkRollup('rollup');
  // A correction replaces its predecessor in the rollup too: the next revision of one Site fact turns it suspect.
  clickHouse(`INSERT INTO analytics.energy_interval_facts SELECT * REPLACE (generateUUIDv4() AS fact_id, fact_revision + 1 AS fact_revision, 'SUSPECT' AS quality, energy_kwh + 1 AS energy_kwh, now64(3) AS projected_at) FROM (SELECT * FROM analytics.energy_interval_facts WHERE site_id = '${siteId}' AND accounting_scope = 'SITE' AND energy_type = 'electricity' AND fact_revision = 0 ORDER BY period_end LIMIT 1)`);
  await checkRollup('corrected rollup');
  report.assertions.hourlyRollup = ranges.map(([from, to]) => `${from}..${to}=${factTotals(from, to)}`);

  // The Operations Agent's semantic query (#573): several measures of one view, cut to the queried Site.
  report.assertions.semanticQuery = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=modules/telemetry',
    'test', '-count=1', '-run', 'TestCubeAnswersAMultiMeasureSemanticQueryForTheQueriedSite', '-v', './internal/cube/...',
  ], {
    env: { ...process.env, ANALYTICS_CUBE_TEST_URL: cubeURL, ANALYTICS_CUBE_TEST_KEY: cubeKey },
  });

  // Both views see only the caller's Tenant and Site, though neither exposes the Tenant.
  const otherSite = { siteId: '018f4f00-1000-7000-8000-000000000099', siteIds: ['018f4f00-1000-7000-8000-000000000099'] };
  const otherTenant = { tenantId: '018f4f00-0100-7000-8000-000000000099' };
  const denied = await queryCube(cubeToken(otherSite));
  for (const [view, context] of [['site_energy', otherSite], ['site_energy', otherTenant], ['equipment_energy', otherSite], ['equipment_energy', otherTenant]]) {
    const leaked = (await queryCube(cubeToken(context), { ...energyQuery({ view }), filters: [] })).data ?? [];
    if (leaked.length !== 0) throw new Error(`${view} leaked rows outside the caller's scope ${JSON.stringify(context)}`);
  }
  for (const context of [otherSite, otherTenant]) {
    const leaked = (await queryCube(cubeToken(context), { measures: ['site_energy_cost.cost_valid_cny', 'site_energy_cost.unpriced_valid_kwh'], dimensions: ['site_energy_cost.tariff_period'], timezone: 'UTC', limit: 10 })).data ?? [];
    if (leaked.some((row) => row['site_energy_cost.cost_valid_cny'] !== null || row['site_energy_cost.unpriced_valid_kwh'] !== null)) {
      throw new Error(`site_energy_cost leaked rows outside the caller's scope ${JSON.stringify(context)}: ${JSON.stringify(leaked)}`);
    }
  }
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

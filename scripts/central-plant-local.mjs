import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import WebSocket from 'ws';
import { centralPlantDevices, centralPlantIdentity } from './central-plant-local-contract.mjs';
import { startCentralPlantLocalTopology } from './central-plant-local-topology.mjs';

const root = resolve(process.cwd());
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('browser port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function dockerQuery(container, sql) {
  const result = spawnSync('docker', ['exec', container, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-Atqc', sql], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  if (result.error || result.status !== 0) throw new Error(result.error?.message ?? result.stderr?.trim() ?? 'S2 query failed');
  return String(result.stdout).trim();
}

function queryPersistedSummary(topology) {
  const integration = centralPlantIdentity.integrationInstanceId;
  const sql = `SELECT json_build_object(
    'acceptedObservations', (SELECT count(*) FROM telemetry_runtime.source_observations WHERE integration_instance_id='${integration}'::uuid AND acceptance_status='ACCEPTED'),
    'deviceCount', (SELECT count(DISTINCT device_id) FROM telemetry_runtime.source_observations WHERE integration_instance_id='${integration}'::uuid AND acceptance_status='ACCEPTED'),
    'latestPointCount', (SELECT count(*) FROM telemetry_runtime.latest_accepted_telemetry l JOIN telemetry_runtime.registry_device_bindings b USING (device_id) WHERE b.integration_instance_id='${integration}'::uuid),
    'snapshotCount', (SELECT count(*) FROM telemetry_runtime.device_observation_snapshots s JOIN telemetry_runtime.registry_device_bindings b USING (device_id) WHERE b.integration_instance_id='${integration}'::uuid),
    'publishedPublicationCount', (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o JOIN telemetry_runtime.registry_device_bindings b USING (device_id) WHERE b.integration_instance_id='${integration}'::uuid AND o.delivery_state='PUBLISHED'),
    'pendingPublicationCount', (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o JOIN telemetry_runtime.registry_device_bindings b USING (device_id) WHERE b.integration_instance_id='${integration}'::uuid AND o.delivery_state='PENDING'),
    'quarantineCount', (SELECT count(*) FROM telemetry_runtime.ingest_quarantine WHERE integration_instance_id='${integration}'::uuid AND resolved_at IS NULL)
  )::text;`;
  return JSON.parse(dockerQuery(topology.database.s2Container, sql));
}

async function waitForPersistedLoop(topology) {
  let last = null;
  for (let attempt = 0; attempt < 120; attempt += 1) {
    last = queryPersistedSummary(topology);
    if (
      Number(last.acceptedObservations) >= topology.pointCount
      && Number(last.deviceCount) === topology.deviceCount
      && Number(last.latestPointCount) === topology.pointCount
      && Number(last.snapshotCount) === topology.deviceCount
      && Number(last.publishedPublicationCount) > 0
      && Number(last.pendingPublicationCount) === 0
      && Number(last.quarantineCount) === 0
    ) return last;
    await pause(1000);
  }
  throw new Error(`central plant telemetry did not converge: ${JSON.stringify(last)}`);
}

function createCDPClient(webSocketURL) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketURL);
    const pending = new Map();
    let nextID = 0;
    socket.once('open', () => resolveClient({
      send(method, params = {}) {
        const id = ++nextID;
        socket.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
      close() { socket.close(); },
    }));
    socket.once('error', rejectClient);
    socket.on('message', (raw) => {
      const message = JSON.parse(String(raw));
      if (!message.id) return;
      const command = pending.get(message.id);
      if (!command) return;
      pending.delete(message.id);
      if (message.error) command.rejectCommand(new Error(message.error.message));
      else command.resolveCommand(message.result);
    });
  });
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text);
  return response.result.value;
}

async function waitForCondition(client, expression, label) {
  let last;
  for (let attempt = 0; attempt < 600; attempt += 1) {
    try {
      last = await evaluate(client, expression);
      if (last) return last;
    } catch {}
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({ url: location.href, text: document.body?.innerText?.slice(0, 5000) ?? '' })`).catch((error) => ({ error: String(error) }));
  throw new Error(`${label} did not become ready: ${JSON.stringify({ last, diagnostic })}`);
}

async function browserAudit(topology) {
  const browserCandidates = [
    process.env.BROWSER_BINARY,
    process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
    process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
    'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
    'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
    '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium',
  ].filter(Boolean);
  const browserPath = browserCandidates.find((candidate) => existsSync(candidate));
  if (!browserPath) throw new Error('A CDP-compatible browser is required for the central plant smoke test');
  const profileDirectory = join(tmpdir(), `central-plant-local-${process.pid}`);
  await mkdir(profileDirectory, { recursive: true });
  const debugPort = await findAvailablePort();
  const browser = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--no-sandbox', '--no-first-run', '--no-default-browser-check',
    '--ignore-certificate-errors', `--remote-debugging-port=${debugPort}`, `--user-data-dir=${profileDirectory}`, 'about:blank',
  ], { stdio: 'ignore', windowsHide: true });
  let client;
  try {
    for (let attempt = 0; attempt < 300; attempt += 1) {
      try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
      if (attempt === 299) throw new Error('browser debugger did not become ready');
      await pause(100);
    }
    const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
    const page = pages.find((candidate) => candidate.type === 'page');
    assert(page?.webSocketDebuggerUrl, 'browser page is unavailable');
    client = await createCDPClient(page.webSocketDebuggerUrl);
    await client.send('Runtime.enable');
    await client.send('Page.enable');
    const sitePath = `/sites/${centralPlantIdentity.siteId}/assets`;
    await client.send('Page.navigate', {
      url: `${topology.webURL}/api/v1/auth/login?returnTo=${encodeURIComponent(sitePath)}`,
    });
    await waitForCondition(client, `
      location.pathname === ${JSON.stringify(sitePath)}
      && Boolean(document.querySelector('[data-testid="real-site-route-assets"]'))
      && document.body.innerText.includes('中央机房')
    `, 'authenticated central plant Site Assets shell');

    const chiller = centralPlantDevices.find((device) => device.name === 'CHILLER-01');
    assert(chiller, 'CHILLER-01 contract is unavailable');
    const requestedKeys = ['chiller.cop', 'chiller.power', 'chiller.cooling_capacity'];
    const authority = await waitForCondition(client, `(async () => {
      try {
        const devicesResponse = await fetch(
          ${JSON.stringify(`/api/v1/sites/${centralPlantIdentity.siteId}/devices?limit=100`)},
          { credentials: 'include', headers: { Accept: 'application/json, application/problem+json' } },
        );
        const devicesBody = await devicesResponse.json();
        const devices = Array.isArray(devicesBody?.items) ? devicesBody.items : [];
        if (!devicesResponse.ok || devices.length !== 6) return false;
        const chiller = devices.find((device) => device.id === ${JSON.stringify(chiller.platformDeviceId)} && device.displayName === 'CHILLER-01');
        if (!chiller) return false;
        const query = new URLSearchParams({ keys: ${JSON.stringify(requestedKeys.join(','))} });
        const snapshotResponse = await fetch(
          '/api/v1/devices/' + encodeURIComponent(chiller.id) + '/observation-snapshot?' + query.toString(),
          { credentials: 'include', headers: { Accept: 'application/json, application/problem+json' } },
        );
        const snapshot = await snapshotResponse.json();
        if (!snapshotResponse.ok || snapshot?.deviceId !== chiller.id || !Array.isArray(snapshot?.values)) return false;
        const values = Object.fromEntries(snapshot.values.map((value) => [value.key, value]));
        if (!${JSON.stringify(requestedKeys)}.every((key) => values[key]?.state === 'PRESENT')) return false;
        return {
          sitePath: location.pathname,
          deviceCount: devices.length,
          deviceNames: devices.map((device) => device.displayName).sort(),
          snapshot: {
            deviceId: snapshot.deviceId,
            businessRevision: snapshot.businessRevision,
            displayState: snapshot.displayState,
            telemetryReadiness: snapshot.telemetryReadiness,
            values: ${JSON.stringify(requestedKeys)}.map((key) => ({
              key,
              value: values[key].value,
              unit: values[key].unit ?? null,
              freshness: values[key].freshness,
              quality: values[key].quality,
            })),
          },
        };
      } catch {
        return false;
      }
    })()`, 'Gateway Registry and S2 authority');
    assert.equal(authority.deviceCount, 6);
    assert.deepEqual(authority.deviceNames, centralPlantDevices.map((device) => device.name).sort());
    assert.equal(authority.snapshot.deviceId, chiller.platformDeviceId);
    assert.equal(authority.snapshot.values.length, requestedKeys.length);
    return { browser: browserPath, ...authority };
  } finally {
    client?.close();
    if (browser.exitCode === null) {
      if (process.platform === 'win32') spawnSync('taskkill', ['/PID', String(browser.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
      else browser.kill('SIGTERM');
    }
    await rm(profileDirectory, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
  }
}

async function runSmoke() {
  const topology = await startCentralPlantLocalTopology({ quiet: true });
  try {
    const persisted = await waitForPersistedLoop(topology);
    const browser = await browserAudit(topology);
    const report = {
      schemaVersion: 1,
      status: 'passed',
      topology: 'EG8200 -> ThingsBoard -> Adapter -> S2 -> Gateway -> HVAC Web Real',
      webURL: topology.webURL,
      thingsBoardURL: topology.thingsBoardURL,
      persisted,
      browser,
      verifiedAt: new Date().toISOString(),
    };
    await writeFile(resolve(root, 'out/central-plant-local/smoke-report.json'), `${JSON.stringify(report, null, 2)}\n`, 'utf8');
    console.log(JSON.stringify(report, null, 2));
  } finally {
    await topology.stop();
  }
}

async function runUp() {
  const topology = await startCentralPlantLocalTopology({ quiet: false });
  console.log(JSON.stringify({ status: 'ready', webURL: topology.webURL, thingsBoardURL: topology.thingsBoardURL }, null, 2));
  await new Promise((resolveSignal) => {
    const shutdown = () => resolveSignal();
    process.once('SIGINT', shutdown);
    process.once('SIGTERM', shutdown);
  });
  await topology.stop();
}

const action = process.argv[2] ?? 'up';
if (action === 'up') await runUp();
else if (action === 'smoke') await runSmoke();
else throw new Error('usage: node scripts/central-plant-local.mjs {up|smoke}');

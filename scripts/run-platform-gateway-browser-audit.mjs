import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const gatewayPort = Number(process.env.PLATFORM_GATEWAY_AUDIT_PORT ?? 18080);
const webPort = Number(process.env.PLATFORM_GATEWAY_WEB_AUDIT_PORT ?? 5179);
const debugPort = Number(process.env.PLATFORM_GATEWAY_DEBUG_PORT ?? 9344);
const gatewayUrl = `http://127.0.0.1:${gatewayPort}`;
const webUrl = `http://127.0.0.1:${webPort}`;
const profileDir = join(tmpdir(), `platform-gateway-audit-${process.pid}`);
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

const edgeCandidates = [
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  join('C:\\Program Files (x86)', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
  join('C:\\Program Files', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
].filter(Boolean);
const edgePath = edgeCandidates.find((candidate) => existsSync(candidate));
if (!edgePath) throw new Error('Microsoft Edge executable not found');
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function waitForUrl(url, label, child) {
  for (let attempt = 0; attempt < 600; attempt += 1) {
    if (child && (child.exitCode !== null || child.signalCode !== null)) {
      throw new Error(`${label} exited before becoming ready: ${child.signalCode ?? child.exitCode}`);
    }
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`${label} did not become ready at ${url}`);
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
    return;
  }
  child.kill('SIGTERM');
  const stopped = await Promise.race([
    once(child, 'exit').then(() => true),
    pause(1500).then(() => false),
  ]);
  if (!stopped) child.kill('SIGKILL');
}

function createCdpClient(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    let nextId = 0;

    socket.addEventListener('open', () => {
      resolveClient({
        send(method, params = {}) {
          const id = ++nextId;
          socket.send(JSON.stringify({ id, method, params }));
          return new Promise((resolveCommand, rejectCommand) => {
            pending.set(id, { resolveCommand, rejectCommand });
          });
        },
        close() {
          socket.close();
        },
      });
    });
    socket.addEventListener('error', (event) => rejectClient(new Error(`CDP socket error: ${String(event)}`)));
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
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
  const result = await client.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text ?? 'Browser evaluation failed');
  }
  return result.result.value;
}

let gatewayProcess;
let webProcess;
let edgeProcess;
let cdpClient;

try {
  await mkdir(profileDir, { recursive: true });
  await mkdir(goCacheDir, { recursive: true });

  gatewayProcess = spawn(goBinary, ['run', './services/platform-gateway/cmd/platform-gateway'], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      GOCACHE: goCacheDir,
      PLATFORM_GATEWAY_ADDR: `127.0.0.1:${gatewayPort}`,
      ROUTE_OWNERSHIP_REGISTRY: resolve(root, 'contracts/ownership/route-ownership.v1.json'),
      S0_ALLOW_MEMORY_ROUTE_AUDIT: 'true',
    },
  });
  gatewayProcess.once('error', (error) => console.error('Gateway process error:', error));
  await waitForUrl(`${gatewayUrl}/api/v1/health?includeBuild=true`, 'platform-gateway', gatewayProcess);

  webProcess = spawn(process.execPath, [
    resolve(root, 'node_modules/vite/bin/vite.js'),
    'apps/hvac-web',
    '--config', 'apps/hvac-web/vite.config.ts',
    '--host', '127.0.0.1',
    '--port', String(webPort),
    '--strictPort',
  ], {
    cwd: root,
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      PLATFORM_GATEWAY_PROXY_TARGET: gatewayUrl,
      S0_GATEWAY_ONLY: 'true',
    },
  });
  webProcess.once('error', (error) => console.error('Vite process error:', error));
  await waitForUrl(`${webUrl}/system`, 'HVAC Web', webProcess);

  edgeProcess = spawn(edgePath, [
    '--headless=new',
    '--disable-gpu',
    '--no-first-run',
    '--no-default-browser-check',
    '--hide-scrollbars',
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profileDir}`,
    `${webUrl}/system`,
  ], { stdio: 'ignore' });

  await waitForUrl(`http://127.0.0.1:${debugPort}/json/version`, 'Edge debugger', edgeProcess);
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available for the audit');

  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');

  let platformState = null;
  for (let attempt = 0; attempt < 120; attempt += 1) {
    platformState = await evaluate(
      cdpClient,
      `document.querySelector('[data-testid="platform-gateway-status"]')?.getAttribute('data-platform-state') ?? null`,
    );
    if (platformState === 'online') break;
    await pause(100);
  }
  assert(platformState === 'online', `Platform status did not become online; final state: ${String(platformState)}`);

  const success = await evaluate(cdpClient, `
    fetch('/api/v1/health?includeBuild=true', {
      headers: { 'X-Request-ID': 'browser-audit-01' },
    }).then(async (response) => ({
      status: response.status,
      contentType: response.headers.get('content-type'),
      requestId: response.headers.get('x-request-id'),
      traceparent: response.headers.get('traceparent'),
      body: await response.json(),
    }))
  `);
  assert(success.status === 200, `Browser health status was ${success.status}`);
  assert(success.contentType === 'application/json', `Browser health content type was ${success.contentType}`);
  assert(success.requestId === 'browser-audit-01', `Browser request ID was ${success.requestId}`);
  assert(/^00-[a-f0-9]{32}-[a-f0-9]{16}-[a-f0-9]{2}$/.test(success.traceparent), `Invalid traceparent: ${success.traceparent}`);
  assert(success.body.status === 'ok' && success.body.service === 'platform-gateway', 'Browser health payload failed contract checks');
  assert(!('success' in success.body) && !('data' in success.body) && !('message' in success.body), 'Browser health response used a forbidden global envelope');

  const cases = [
    { label: 'method-not-allowed', path: '/api/v1/health', init: { method: 'POST' }, status: 405, code: 'METHOD_NOT_ALLOWED' },
    { label: 'unknown-route', path: '/api/v1/browser-audit-unknown', init: { method: 'GET' }, status: 404, code: 'ROUTE_NOT_FOUND' },
    { label: 'invalid-query', path: '/api/v1/health?includeBuild=yes', init: { method: 'GET' }, status: 400, code: 'INVALID_QUERY_PARAMETER' },
  ];

  for (const testCase of cases) {
    const result = await evaluate(cdpClient, `
      fetch(${JSON.stringify(testCase.path)}, ${JSON.stringify(testCase.init)}).then(async (response) => ({
        status: response.status,
        contentType: response.headers.get('content-type'),
        body: await response.json(),
      }))
    `);
    assert(result.status === testCase.status, `${testCase.label} status was ${result.status}`);
    assert(result.contentType === 'application/problem+json', `${testCase.label} content type was ${result.contentType}`);
    assert(result.body.code === testCase.code, `${testCase.label} code was ${result.body.code}`);
    assert(/^[a-f0-9]{32}$/.test(result.body.traceId), `${testCase.label} traceId was invalid`);
    assert(typeof result.body.retryable === 'boolean', `${testCase.label} retryable was missing`);
  }

  console.log('Platform Gateway browser audit passed.');
} finally {
  cdpClient?.close();
  await stopProcess(edgeProcess);
  await stopProcess(webProcess);
  await stopProcess(gatewayProcess);
  await rm(profileDir, { recursive: true, force: true });
}

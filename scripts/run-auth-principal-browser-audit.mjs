import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import WebSocket from 'ws';
import { startS0AuthTopology, stopProcess } from './s0-auth-topology.mjs';

const debugPort = Number(process.env.S0_AUTH_DEBUG_PORT ?? 9355);
const profileDir = join(tmpdir(), `s0-auth-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const edgeCandidates = [
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  join('C:\\Program Files (x86)', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
  join('C:\\Program Files', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
].filter(Boolean);
const edgePath = edgeCandidates.find((candidate) => existsSync(candidate));
if (!edgePath) throw new Error('Microsoft Edge executable not found');

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function waitForDebugger(child) {
  for (let attempt = 0; attempt < 600; attempt += 1) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error('Edge exited before debugger became ready');
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json/version`);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error('Edge debugger did not become ready');
}

function createCdpClient(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    let nextId = 0;
    socket.addEventListener('open', () => resolveClient({
      send(method, params = {}) {
        const id = ++nextId;
        socket.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
      close() { socket.close(); },
    }));
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
  const result = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text ?? 'Browser evaluation failed');
  }
  return result.result.value;
}

async function waitForPrincipalState(client, expected) {
  for (let attempt = 0; attempt < 600; attempt += 1) {
    try {
      const state = await evaluate(client, `document.querySelector('[data-testid="authenticated-principal-status"]')?.getAttribute('data-principal-state') ?? null`);
      if (state === expected) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`Principal UI did not reach ${expected}`);
}

let topology;
let edgeProcess;
let cdpClient;
try {
  await mkdir(profileDir, { recursive: true });
  topology = await startS0AuthTopology({ oidcPort: 19091, iamPort: 18445, gatewayPort: 18081, webPort: 5180 });
  edgeProcess = spawn(edgePath, [
    '--headless=new',
    '--disable-gpu',
    '--no-first-run',
    '--no-default-browser-check',
    '--hide-scrollbars',
    '--ignore-certificate-errors',
    '--allow-insecure-localhost',
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profileDir}`,
    `${topology.webURL}/system`,
  ], { stdio: 'ignore' });
  await waitForDebugger(edgeProcess);
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available for auth audit');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Network.enable');

  await waitForPrincipalState(cdpClient, 'anonymous');
  await evaluate(cdpClient, `window.location.assign('/api/v1/auth/login?returnTo=%2Fsystem')`);
  await waitForPrincipalState(cdpClient, 'authenticated');

  const principal = await evaluate(cdpClient, `
    fetch('/api/v1/principal').then(async (response) => ({ status: response.status, body: await response.json() }))
  `);
  assert(principal.status === 200, `Principal status was ${principal.status}`);
  assert(principal.body.principal.subject === 'fixture-user', 'Unexpected browser principal');
  assert(principal.body.context.executingServicePrincipal.spiffeId === 'spiffe://hvac.local/platform-gateway', 'Gateway workload identity was not preserved');
  assert(principal.body.context.audience === 'iam-service', 'Delegation audience was not IAM');
  assert(principal.body.context.actingOrganizationId === 'org-fixture-01', 'Organization context was missing');
  assert(principal.body.session.csrfToken && principal.body.session.id, 'Session view was incomplete');

  const browserState = await evaluate(cdpClient, `({
    url: window.location.href,
    cookie: document.cookie,
    localStorage: Object.fromEntries(Object.entries(localStorage)),
    sessionStorage: Object.fromEntries(Object.entries(sessionStorage)),
  })`);
  assert(!/[?&](code|state|access_token|refresh_token|id_token)=/i.test(browserState.url), `OIDC material remained in URL: ${browserState.url}`);
  assert(!browserState.cookie.includes('hvac_session'), 'HttpOnly BFF Session was visible to document.cookie');
  const storageText = JSON.stringify({ localStorage: browserState.localStorage, sessionStorage: browserState.sessionStorage });
  assert(!/(access_token|refresh_token|id_token|authorization_code|Bearer)/i.test(storageText), `OIDC material leaked into browser storage: ${storageText}`);

  const cookies = await cdpClient.send('Network.getAllCookies');
  const sessionCookies = cookies.cookies.filter((cookie) => cookie.name === '__Host-hvac_session');
  assert(sessionCookies.length === 1, `Expected one BFF Session cookie, found ${sessionCookies.length}`);
  assert(sessionCookies[0].httpOnly === true, 'BFF Session cookie was not HttpOnly');
  assert(sessionCookies[0].secure === true, 'BFF Session cookie was not Secure');
  assert(sessionCookies[0].sameSite === 'Lax', `BFF Session SameSite was ${sessionCookies[0].sameSite}`);
  assert(!sessionCookies[0].value.includes('.'), 'BFF Session cookie was not opaque');

  const forged = await evaluate(cdpClient, `
    fetch('/api/v1/principal', { headers: { 'X-Principal': 'forged-browser-user' } })
      .then(async (response) => ({ status: response.status, body: await response.json() }))
  `);
  assert(forged.status === 400 && forged.body.code === 'FORGED_IDENTITY_HEADER', 'Forged browser identity header was not rejected');

  const csrfRejected = await evaluate(cdpClient, `
    fetch('/api/v1/auth/logout', { method: 'POST', headers: { 'X-CSRF-Token': 'wrong-token' } })
      .then(async (response) => ({ status: response.status, body: await response.json() }))
  `);
  assert(csrfRejected.status === 403 && csrfRejected.body.code === 'CSRF_TOKEN_INVALID', 'Invalid CSRF token was not rejected');

  const directIAM = await evaluate(cdpClient, `
    fetch(${JSON.stringify(`${topology.iamURL}/internal/v1/principal/current`)}, { method: 'POST' })
      .then(() => ({ resolved: true }))
      .catch(() => ({ resolved: false }))
  `);
  assert(directIAM.resolved === false, 'Browser reached private IAM without a workload certificate');

  await evaluate(cdpClient, `document.querySelector('button[aria-label="退出平台会话"]')?.click()`);
  await waitForPrincipalState(cdpClient, 'anonymous');
  const afterLogout = await evaluate(cdpClient, `
    fetch('/api/v1/principal').then(async (response) => ({ status: response.status, body: await response.json() }))
  `);
  assert(afterLogout.status === 401 && ['AUTHENTICATION_REQUIRED', 'SESSION_INVALID'].includes(afterLogout.body.code), 'Logout did not invalidate the BFF Session');

  console.log('S0 authenticated principal browser audit passed.');
} finally {
  cdpClient?.close();
  await stopProcess(edgeProcess);
  await topology?.stop();
  await rm(profileDir, { recursive: true, force: true });
}

import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer as createHTTPServer } from 'node:http';
import { existsSync, readFileSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer as createViteServer } from 'vite';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/agent-session-workspace');
const outputRoot = resolve(root, 'out/agent-session-workspace-certification');
const linuxProfileDir = join(tmpdir(), `agent-session-workspace-browser-${process.pid}`);
const tenantId = '01910000-0000-7000-8000-000000000001';
const siteId = '01910000-0001-7000-8000-000000000001';
const sessionId = 'agent-session-browser-001';
const previousRunId = 'run-browser-001';
const continuationRunId = 'run-browser-002';
const requestArtifactId = 'input-request-browser-001';
const optionValue = 'night-adjust';
const csrfFixture = '[REDACTED_SECRET]';
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  assert(address && typeof address === 'object', 'port allocator did not expose an address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function json(response, value, status = 200) {
  response.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
  });
  response.end(JSON.stringify(value));
}

function principal() {
  const operator = {
    subject: 'agent-session-audit',
    issuer: 'https://identity.example.test',
    displayName: 'Agent Session Auditor',
    email: '',
    roles: ['operator'],
  };
  return {
    principal: operator,
    context: {
      initiatingPrincipal: operator,
      executingServicePrincipal: {
        service: 'platform-gateway',
        spiffeId: 'spiffe://hvac.local/platform-gateway',
      },
      tenantId,
      audience: 'iam-service',
      policyRevision: 'agent-session-policy-1',
      delegationExpiresAt: '2026-09-05T00:00:00.000Z',
    },
    authorization: {
      capabilitySetVersion: 11,
      policyRevision: 'agent-session-policy-1',
      capabilities: ['site.read'],
    },
    session: {
      id: 'agent-session-browser-audit',
      expiresAt: '2026-09-05T00:00:00.000Z',
      idleTimeoutMs: 1_800_000,
      csrfToken: csrfFixture,
      revocationObjectiveMs: 30_000,
      lastAuditMessageId: 'agent-session-browser-audit-message',
    },
  };
}

const zeroUsage = Object.freeze({ inputTokens: 0, outputTokens: 0, modelCalls: 0, toolCalls: 0 });
const modelRef = Object.freeze({ provider: 'openai', model: 'gpt-5-mini' });

function waitingSnapshot() {
  return {
    session: {
      id: sessionId,
      tenantId,
      siteId,
      agentDefinitionId: 'operations-investigation.v1',
      createdBy: 'agent-session-audit',
      revision: 2,
      createdAt: 1_000,
      updatedAt: 1_200,
      status: 'WAITING_FOR_INPUT',
      activeRunId: null,
    },
    runs: [{
      id: previousRunId,
      sessionId,
      modelRef,
      status: 'COMPLETED',
      startedAt: 1_050,
      finishedAt: 1_200,
      usage: { inputTokens: 32, outputTokens: 18, modelCalls: 1, toolCalls: 1 },
      failureCode: null,
    }],
    messages: [{
      id: 'message-browser-operator-001',
      sessionId,
      runId: null,
      role: 'OPERATOR',
      content: '请检查昨夜能耗异常，并在需要时询问我。',
      createdAt: 1_040,
    }, {
      id: 'message-browser-assistant-001',
      sessionId,
      runId: previousRunId,
      role: 'ASSISTANT',
      content: '需要确认是否把夜间策略调整纳入本次调查。',
      createdAt: 1_190,
    }],
    toolExecutions: [{
      id: 'tool-browser-001',
      sessionId,
      runId: previousRunId,
      toolName: 'energy.query_series',
      argumentsDigest: 'sha256:browser-energy-query',
      status: 'COMPLETED',
      startedAt: 1_080,
      finishedAt: 1_100,
      resultSummary: 'Energy evidence is available for the Site.',
      provenance: [],
      failureCode: null,
    }],
    artifacts: [{
      id: requestArtifactId,
      sessionId,
      runId: previousRunId,
      kind: 'INPUT_REQUEST',
      request: {
        prompt: '是否将夜间策略调整纳入调查？',
        response: {
          kind: 'SINGLE_SELECT',
          choices: [{ value: optionValue, label: '纳入夜间策略调整' }, { value: 'energy-only', label: '只分析能耗' }],
        },
      },
      createdAt: 1_195,
    }],
  };
}

function activeSnapshot() {
  const waiting = waitingSnapshot();
  return {
    session: {
      ...waiting.session,
      revision: 3,
      updatedAt: 1_300,
      status: 'ACTIVE',
      activeRunId: continuationRunId,
    },
    runs: [...waiting.runs, {
      id: continuationRunId,
      sessionId,
      modelRef,
      status: 'RUNNING',
      startedAt: 1_300,
      finishedAt: null,
      usage: zeroUsage,
      failureCode: null,
    }],
    messages: [...waiting.messages, {
      id: 'message-browser-operator-002',
      sessionId,
      runId: null,
      role: 'OPERATOR',
      content: '纳入夜间策略调整',
      createdAt: 1_300,
    }],
    toolExecutions: waiting.toolExecutions,
    artifacts: [...waiting.artifacts, {
      id: 'input-response-browser-001',
      sessionId,
      runId: continuationRunId,
      kind: 'INPUT_RESPONSE',
      requestArtifactId,
      value: optionValue,
      submittedBy: 'agent-session-audit',
      createdAt: 1_300,
    }],
  };
}

function agentEvent(type, runId, sequence, payload) {
  return {
    version: 'hvac.agent.event/v1',
    type,
    sessionId,
    runId,
    sequence,
    at: 1_300 + sequence,
    payload,
  };
}

function writeEvent(response, event) {
  response.write(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
}

function createGatewayFixture() {
  const requests = [];
  const streamFirstEvents = [];
  const submissions = [];
  let streamCount = 0;
  let abortedStreams = 0;
  const collectionPath = `/api/v1/sites/${siteId}/operations/agent-sessions`;
  const eventsPath = `${collectionPath}/${sessionId}/events`;
  const inputPath = `${collectionPath}/${sessionId}:submit-input`;

  const server = createHTTPServer(async (request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    requests.push({ method: request.method, path: url.pathname });
    if (request.method === 'GET' && url.pathname === '/api/v1/principal') {
      json(response, principal());
      return;
    }
    if (request.method === 'GET' && url.pathname === collectionPath) {
      json(response, [waitingSnapshot()]);
      return;
    }
    if (request.method === 'POST' && url.pathname === inputPath) {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      submissions.push({ body, csrf: request.headers['x-csrf-token'] ?? null });
      json(response, activeSnapshot(), 202);
      return;
    }
    if (request.method === 'GET' && url.pathname === eventsPath) {
      streamCount += 1;
      const number = streamCount;
      response.writeHead(200, {
        'content-type': 'text/event-stream; charset=utf-8',
        'cache-control': 'no-store, no-transform',
        connection: 'keep-alive',
      });
      const snapshot = number === 1 ? waitingSnapshot() : activeSnapshot();
      const first = agentEvent('session.snapshot', snapshot.session.activeRunId, 0, { snapshot });
      streamFirstEvents.push(first.type);
      writeEvent(response, first);
      if (number >= 2) {
        writeEvent(response, agentEvent('assistant.delta', continuationRunId, 1, {
          messageId: 'message-browser-assistant-streaming',
          delta: '正在检查夜间策略与能耗证据。',
        }));
      }
      request.once('close', () => {
        if (!response.writableEnded) abortedStreams += 1;
      });
      return;
    }
    json(response, { title: 'Not Found', detail: `${request.method} ${url.pathname}` }, 404);
  });

  return {
    server,
    requests,
    streamFirstEvents,
    submissions,
    streamCount: () => streamCount,
    abortedStreams: () => abortedStreams,
  };
}

function createCdpClient(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    const events = [];
    let nextId = 0;
    socket.on('open', () => resolveClient({
      events,
      send(method, params = {}) {
        const id = ++nextId;
        socket.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
      close() { socket.close(); },
    }));
    socket.on('error', rejectClient);
    socket.on('message', (raw) => {
      const message = JSON.parse(String(raw));
      if (!message.id) {
        events.push(message);
        return;
      }
      const command = pending.get(message.id);
      if (!command) return;
      pending.delete(message.id);
      if (message.error) command.rejectCommand(new Error(message.error.message));
      else command.resolveCommand(message.result);
    });
  });
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? 'Browser evaluation failed');
  }
  return response.result.value;
}

async function waitFor(client, expression, label) {
  let last;
  for (let attempt = 0; attempt < 160; attempt += 1) {
    last = await evaluate(client, expression).catch((error) => ({ error: String(error) }));
    if (last === true) return;
    await pause(50);
  }
  const diagnostics = await evaluate(client, `({
    href: location.href,
    readyState: document.readyState,
    body: document.body?.innerText?.slice(0, 2000) ?? '',
    viteError: document.querySelector('vite-error-overlay')?.shadowRoot?.textContent?.slice(0, 4000) ?? '',
  })`).catch((error) => ({ diagnosticsError: String(error) }));
  throw new Error(`Timed out waiting for ${label}; last=${JSON.stringify(last)} diagnostics=${JSON.stringify(diagnostics)} events=${JSON.stringify(client.events.slice(-20))}`);
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped && process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  } else if (!stopped) {
    child.kill('SIGKILL');
  }
}

const browserCandidates = [
  process.env.BROWSER_BINARY,
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
  '/mnt/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  '/mnt/c/Program Files/Google/Chrome/Application/chrome.exe',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium-browser',
  '/usr/bin/chromium',
].filter(Boolean);
const browserPath = browserCandidates.find((candidate) => existsSync(candidate));
if (!browserPath) throw new Error('A CDP-compatible browser was not found');
const windowsBrowser = browserPath.toLowerCase().endsWith('.exe');
const windowsInterop = windowsBrowser && process.platform !== 'win32';
const windowsTemp = windowsInterop
  ? String(spawnSync('cmd.exe', ['/c', 'echo', '%TEMP%'], { encoding: 'utf8' }).stdout ?? '').trim()
  : '';
const windowsProfileArgument = windowsInterop
  ? `${windowsTemp}\\agent-session-workspace-browser-${process.pid}`
  : '';
const windowsProfileDir = windowsInterop
  ? String(spawnSync('wslpath', ['-u', windowsProfileArgument], { encoding: 'utf8' }).stdout ?? '').trim()
  : '';
const profileDir = windowsInterop ? windowsProfileDir : linuxProfileDir;
const profileArgument = windowsInterop ? windowsProfileArgument : profileDir;
const windowsHost = windowsInterop
  ? readFileSync('/etc/resolv.conf', 'utf8').match(/^nameserver\s+(\S+)/mu)?.[1]
  : undefined;
const debuggerHost = windowsInterop ? windowsHost : '127.0.0.1';
if (!profileDir || !profileArgument || !debuggerHost) throw new Error('Browser runtime path/network resolution failed.');

const gatewayPort = await findAvailablePort();
const debugPort = await findAvailablePort();
const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
const fixture = createGatewayFixture();
let viteServer;
let browserProcess;
let cdpClient;
let passed = false;
const assertions = [];

try {
  await mkdir(profileDir, { recursive: true });
  await mkdir(outputRoot, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });
  viteServer = await createViteServer({
    root: fixtureRoot,
    configFile: false,
    logLevel: 'error',
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: {
      host: '127.0.0.1',
      port: 0,
      strictPort: false,
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
    },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Vite fixture server has no address');
  const webURL = `http://127.0.0.1:${viteAddress.port}/`;

  browserProcess = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--no-sandbox',
    '--no-first-run',
    '--no-default-browser-check',
    `--remote-debugging-port=${debugPort}`,
    '--remote-debugging-address=0.0.0.0',
    `--user-data-dir=${profileArgument}`,
    'about:blank',
  ], { stdio: 'ignore' });
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      if ((await fetch(`http://${debuggerHost}:${debugPort}/json/version`)).ok) break;
    } catch {}
    if (attempt === 299) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }
  const pages = await fetch(`http://${debuggerHost}:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Page.enable');
  await cdpClient.send('Page.navigate', { url: webURL });

  await waitFor(cdpClient, `document.body.innerText.includes('需要操作员输入') && document.body.innerText.includes('是否将夜间策略调整纳入调查？')`, 'typed input request');
  assert(fixture.streamCount() === 1, `initial Session should open exactly one SSE stream, saw ${fixture.streamCount()}`);
  assert(fixture.streamFirstEvents[0] === 'session.snapshot', 'initial Session stream did not begin with durable snapshot');
  assertions.push('initial-reconnect-begins-with-durable-snapshot');

  const inputA11y = await evaluate(cdpClient, `({
    group: document.querySelector('[aria-label="操作员选项"]')?.tagName ?? null,
    submit: Array.from(document.querySelectorAll('button')).find((button) => button.textContent?.includes('提交并继续调查'))?.textContent ?? null,
  })`);
  assert(inputA11y.group !== null && inputA11y.submit?.includes('提交并继续调查'), 'typed operator input is not discoverable through labelled controls');
  assertions.push('typed-input-controls-are-observable-and-labelled');

  await evaluate(cdpClient, `document.querySelector('input[type="radio"][value="${optionValue}"]')?.click()`);
  await evaluate(cdpClient, `Array.from(document.querySelectorAll('button')).find((button) => button.textContent?.includes('提交并继续调查'))?.click()`);
  await waitFor(cdpClient, `document.body.innerText.includes('正在检查夜间策略与能耗证据。') && document.body.innerText.includes('实时运行中')`, 'continuation streaming delta');
  assert(fixture.submissions.length === 1, 'typed input was not submitted exactly once');
  const submission = fixture.submissions[0];
  assert(submission.csrf === csrfFixture, 'typed input mutation omitted current BFF CSRF capability');
  assert(JSON.stringify(Object.keys(submission.body).sort()) === JSON.stringify(['expectedRevision', 'requestArtifactId', 'value']), 'browser submitted fields outside the generated typed input contract');
  assert(submission.body.expectedRevision === 2 && submission.body.requestArtifactId === requestArtifactId && submission.body.value === optionValue, 'typed input body did not preserve request identity and value');
  assert(!Object.hasOwn(submission.body, 'submittedBy'), 'browser forged authenticated principal attribution');
  assert(fixture.streamFirstEvents[1] === 'session.snapshot', 'continuation reconnect did not begin with durable snapshot');
  assertions.push('typed-input-continuation-reconnects-snapshot-first-and-streams-delta');

  const visibleSafety = await evaluate(cdpClient, `({
    reasoning: document.body.innerText.toLowerCase().includes('thinking'),
    providerEvent: document.body.innerText.includes('message_update'),
  })`);
  assert(!visibleSafety.reasoning && !visibleSafety.providerEvent, 'browser rendered Pi/provider internal event or reasoning vocabulary');
  assertions.push('hidden-reasoning-and-provider-events-not-rendered');

  await evaluate(cdpClient, `document.querySelector('#agent-session-audit-purge')?.click()`);
  await waitFor(cdpClient, `document.querySelector('#agent-session-audit-purge-count')?.textContent === '1' && document.body.innerText.includes('当前 Site 尚无调查。') && !document.body.innerText.includes('正在检查夜间策略与能耗证据。')`, 'protected Site purge');
  for (let attempt = 0; attempt < 60 && fixture.abortedStreams() < 2; attempt += 1) await pause(50);
  assert(fixture.abortedStreams() >= 2, `Site purge/continuation transition did not abort protected SSE resources; aborted=${fixture.abortedStreams()}`);
  const afterPurge = await evaluate(cdpClient, `({
    hasOldQuestion: document.body.innerText.includes('请检查昨夜能耗异常'),
    hasStreaming: document.body.innerText.includes('正在检查夜间策略与能耗证据。'),
    sessionItems: document.querySelectorAll('.agent-session-list-item').length,
  })`);
  assert(!afterPurge.hasOldQuestion && !afterPurge.hasStreaming && afterPurge.sessionItems === 0, 'Site protected purge retained Agent Session content');
  assertions.push('site-purge-aborts-live-stream-and-clears-protected-agent-state');

  passed = true;
  await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({
    schemaVersion: 1,
    passed: true,
    generatedAt: new Date().toISOString(),
    assertions,
    network: {
      requests: fixture.requests,
      streamCount: fixture.streamCount(),
      abortedStreams: fixture.abortedStreams(),
      streamFirstEvents: fixture.streamFirstEvents,
      submissions: fixture.submissions,
    },
  }, null, 2));
  console.log(`Agent Session workspace browser audit passed. Evidence: ${join(outputRoot, 'browser-evidence.json')}`);
} finally {
  cdpClient?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  await rm(profileDir, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 }).catch(() => undefined);
  if (!passed) {
    await mkdir(outputRoot, { recursive: true });
    await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({
      schemaVersion: 1,
      passed: false,
      generatedAt: new Date().toISOString(),
      assertions,
      network: {
        requests: fixture.requests,
        streamCount: fixture.streamCount(),
        abortedStreams: fixture.abortedStreams(),
        streamFirstEvents: fixture.streamFirstEvents,
        submissions: fixture.submissions,
      },
    }, null, 2));
  }
}

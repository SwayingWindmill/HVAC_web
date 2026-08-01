import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { existsSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

import WebSocket from 'ws';

const root = resolve(process.cwd());
const outputRoot = resolve(root, 'out/s5-work-order-create-assign');
const profileDir = join(tmpdir(), `s5-work-order-create-assign-${process.pid}`);
const organizationId = '01910000-0000-7000-8000-000000000001';
const siteId = '01910000-0001-7000-8000-000000000001';
const otherSiteId = '01910000-0002-7000-8000-000000000001';
const workOrderId = '01930000-5000-7000-8000-000000000001';
const sourceAlarmId = '01910000-4000-7000-8000-000000000001';
const assigneeId = '01910000-9000-7000-8000-000000000001';
const csrfToken = `s5-work-order-csrf-${process.pid}`;
const createKey = `create-work-order-${process.pid}`;
const assignKey = `assign-work-order-${process.pid}`;
const salt = 's5-work-order-write-canary-v1';
const group = 's5-work-order-write-v1';
const revision = 1;
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const assert = (condition, message) => { if (!condition) throw new Error(message); };

function cohortBucket(subject) {
  const digest = createHash('sha256')
    .update(`${salt}\0${group}\0${revision}\0${organizationId}\0${subject}`)
    .digest();
  return Number(digest.readBigUInt64BE(0) % 100n);
}

function findSubject(selected, label) {
  for (let index = 0; index < 100000; index += 1) {
    const subject = `work-order-write-${label}-${index}`;
    if ((cohortBucket(subject) < 1) === selected) return subject;
  }
  throw new Error('unable to resolve deterministic Work Order write cohort subject');
}

const selectedSubject = findSubject(true, 'selected');
const authorizationDeniedSubject = findSubject(true, 'authorization-denied');
const deniedSubject = findSubject(false, 'denied');
const requests = [];

const createRequest = {
  title: 'Inspect authoritative AHU fan vibration',
  description: 'Created through the governed Work Order mutation canary.',
  priority: 'HIGH',
  sourceReferences: [{ domain: 'ALARM', resourceId: sourceAlarmId, relationship: 'ORIGIN' }],
  assigneeId: null,
  teamId: null,
};

function initialWorkOrder() {
  return {
    schemaVersion: 1,
    workOrderId,
    organizationId,
    siteId,
    title: createRequest.title,
    description: createRequest.description,
    priority: createRequest.priority,
    status: 'OPEN',
    sourceReferences: createRequest.sourceReferences,
    tasks: { total: 0, completed: 0, blocked: 0 },
    noteCount: 0,
    attachmentCount: 0,
    completionEvidence: [],
    timeline: [{
      operation: 'CREATE',
      toStatus: 'OPEN',
      reason: 'WORK_ORDER_CREATED',
      actorType: 'PRINCIPAL',
      actorId: selectedSubject,
      occurredAt: '2026-08-01T00:00:00Z',
      version: 1,
    }],
    version: 1,
    createdAt: '2026-08-01T00:00:00Z',
    updatedAt: '2026-08-01T00:00:00Z',
  };
}

let authoritativeWorkOrder = null;
let committedCreateKey = '';
let committedCreateBody = '';

function problem(response, status, code, detail) {
  response.writeHead(status, {
    'content-type': 'application/problem+json',
    'cache-control': 'private, no-store',
  });
  response.end(JSON.stringify({
    type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`,
    title: code,
    status,
    detail,
    code,
    retryable: false,
  }));
}

function json(response, status, payload, headers = {}) {
  response.writeHead(status, {
    'content-type': 'application/json',
    'cache-control': 'private, no-store',
    ...headers,
  });
  response.end(JSON.stringify(payload));
}

function cookieValue(request, key) {
  const match = String(request.headers.cookie ?? '')
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${key}=`));
  return match ? decodeURIComponent(match.slice(key.length + 1)) : '';
}

async function readJSONBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const raw = Buffer.concat(chunks).toString('utf8');
  return { raw, value: JSON.parse(raw) };
}

function html() {
  return `<!doctype html><meta charset="utf-8"><title>Work Order create/assign canary audit</title>
  <main data-testid="work-order-mutation-audit" data-state="LOADING">
    <h1>Work Order create/assign canary</h1>
    <pre data-testid="result"></pre>
  </main>
  <script>
  const siteId=${JSON.stringify(siteId)}, otherSiteId=${JSON.stringify(otherSiteId)}, workOrderId=${JSON.stringify(workOrderId)};
  const selected=${JSON.stringify(selectedSubject)}, authorizationDenied=${JSON.stringify(authorizationDeniedSubject)}, denied=${JSON.stringify(deniedSubject)};
  const csrf=${JSON.stringify(csrfToken)}, createKey=${JSON.stringify(createKey)}, assignKey=${JSON.stringify(assignKey)};
  const createBody=${JSON.stringify(createRequest)}, assigneeId=${JSON.stringify(assigneeId)};
  const main=document.querySelector('[data-testid="work-order-mutation-audit"]'), result=document.querySelector('[data-testid="result"]');
  const setSubject=(value)=>{ document.cookie='subject='+encodeURIComponent(value)+'; path=/; SameSite=Lax'; };
  const clear=()=>{ result.textContent=''; main.dataset.state='DENIED'; };
  async function mutate(path, body, key) {
    const response=await fetch(path, {
      method:'POST',
      headers:{accept:'application/json','content-type':'application/json','x-csrf-token':csrf,'idempotency-key':key},
      body:JSON.stringify(body),
    });
    const payload=await response.json();
    if(!response.ok){ clear(); result.textContent=payload.code; }
    else { main.dataset.state='READY'; result.textContent=payload.title; }
    return {status:response.status,body:payload,replayed:response.headers.get('idempotency-replayed')};
  }
  async function create(targetSite=siteId, key=createKey) {
    return mutate('/api/v1/sites/'+targetSite+'/work-orders', createBody, key);
  }
  async function assign(expectedVersion=1, key=assignKey) {
    return mutate('/api/v1/sites/'+siteId+'/work-orders/'+workOrderId+':assign', {
      expectedVersion, assigneeId, teamId:null, reason:'Assign to authorized technician',
    }, key);
  }
  globalThis.__WORK_ORDER_MUTATION_AUDIT__={
    selected, authorizationDenied, denied,
    createSelected:async()=>{setSubject(selected);return create();},
    retryCreate:async()=>{setSubject(selected);return create();},
    assignSelected:async()=>{setSubject(selected);return assign();},
    staleAssign:async()=>{setSubject(selected);return assign(1,assignKey+'-stale');},
    denyAuthorization:async()=>{setSubject(authorizationDenied);return create(siteId,createKey+'-denied');},
    switchDenied:async()=>{setSubject(denied);return create(siteId,createKey+'-nonselected');},
    switchSite:async()=>{setSubject(selected);return create(otherSiteId,createKey+'-cross-site');},
    loseSession:async()=>{document.cookie='subject=; Max-Age=0; path=/';return create(siteId,createKey+'-session-loss');},
    unreviewedLifecycle:async()=>{setSubject(selected);return mutate('/api/v1/sites/'+siteId+'/work-orders/'+workOrderId+':complete',{expectedVersion:2,reason:'not reviewed'},'complete-'+createKey);},
    state:()=>main.dataset.state,
  };
  setSubject(selected);
  globalThis.__WORK_ORDER_MUTATION_AUDIT__.initial=globalThis.__WORK_ORDER_MUTATION_AUDIT__.createSelected();
  </script>`;
}

const forbiddenAuthorityHeaders = new Set([
  'x-work-order-write-context',
  'x-work-order-read-context',
  'x-organization-id',
  'x-site-id',
  'x-work-order-id',
  'x-delegation-grant',
  'x-principal-subject',
]);

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? '/', 'http://fixture');
  if (url.pathname === '/' || url.pathname === '/favicon.ico') {
    if (url.pathname === '/favicon.ico') {
      response.writeHead(204);
      response.end();
      return;
    }
    response.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' });
    response.end(html());
    return;
  }

  const subject = cookieValue(request, 'subject');
  const bucket = subject ? cohortBucket(subject) : null;
  const recordedHeaders = Object.fromEntries(
    Object.entries(request.headers)
      .filter(([name]) => name.startsWith('x-') || name === 'idempotency-key' || name === 'content-type'),
  );
  requests.push({
    method: request.method,
    path: url.pathname,
    subject,
    bucket,
    headers: recordedHeaders,
  });

  if (request.method !== 'POST') {
    problem(response, 405, 'METHOD_NOT_ALLOWED', 'Work Order create/assignment canary is POST-only.');
    return;
  }
  if (Object.keys(request.headers).some((name) => forbiddenAuthorityHeaders.has(name))) {
    problem(response, 400, 'FORGED_IDENTITY_HEADER', 'Browser authority headers are rejected.');
    return;
  }
  if (!subject) {
    problem(response, 401, 'SESSION_REQUIRED', 'The authenticated Session is required.');
    return;
  }
  if (bucket >= 1) {
    problem(response, 404, 'ROUTE_NOT_FOUND', 'The requested route is not available.');
    return;
  }
  if (subject === authorizationDeniedSubject) {
    problem(response, 403, 'WORK_ORDER_ACCESS_DENIED', 'The Work Order mutation is not authorized.');
    return;
  }
  if (request.headers['x-csrf-token'] !== csrfToken) {
    problem(response, 403, 'CSRF_INVALID', 'The request CSRF token is invalid.');
    return;
  }
  const idempotencyKey = String(request.headers['idempotency-key'] ?? '');
  if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$/.test(idempotencyKey)) {
    problem(response, 400, 'WORK_ORDER_MUTATION_INVALID', 'A bounded Idempotency-Key is required.');
    return;
  }

  const createMatch = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/work-orders$/);
  const assignMatch = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/work-orders\/([^/:]+):assign$/);
  if (createMatch) {
    if (createMatch[1] !== siteId) {
      problem(response, 404, 'RESOURCE_NOT_FOUND', 'The Work Order resource is not visible.');
      return;
    }
    let parsed;
    try {
      parsed = await readJSONBody(request);
    } catch {
      problem(response, 400, 'WORK_ORDER_MUTATION_INVALID', 'The Work Order create body is invalid.');
      return;
    }
    if (!authoritativeWorkOrder) {
      authoritativeWorkOrder = initialWorkOrder();
      committedCreateKey = idempotencyKey;
      committedCreateBody = parsed.raw;
      json(response, 201, authoritativeWorkOrder);
      return;
    }
    if (idempotencyKey === committedCreateKey && parsed.raw === committedCreateBody) {
      json(response, 200, authoritativeWorkOrder, { 'Idempotency-Replayed': 'true' });
      return;
    }
    if (idempotencyKey === committedCreateKey) {
      problem(response, 409, 'IDEMPOTENCY_CONFLICT', 'The Idempotency-Key was committed with another create request.');
      return;
    }
    problem(response, 409, 'IDEMPOTENCY_CONFLICT', 'Only the reviewed create mutation is available in this fixture.');
    return;
  }

  if (assignMatch) {
    if (assignMatch[1] !== siteId || assignMatch[2] !== workOrderId || !authoritativeWorkOrder) {
      problem(response, 404, 'RESOURCE_NOT_FOUND', 'The Work Order resource is not visible.');
      return;
    }
    let parsed;
    try {
      parsed = await readJSONBody(request);
    } catch {
      problem(response, 400, 'WORK_ORDER_ASSIGNMENT_INVALID', 'The Work Order assignment body is invalid.');
      return;
    }
    if (parsed.value.expectedVersion !== authoritativeWorkOrder.version) {
      problem(response, 409, 'VERSION_CONFLICT', 'The Work Order changed before assignment.');
      return;
    }
    authoritativeWorkOrder = {
      ...authoritativeWorkOrder,
      assigneeId: parsed.value.assigneeId,
      teamId: parsed.value.teamId,
      version: authoritativeWorkOrder.version + 1,
      updatedAt: '2026-08-01T00:01:00Z',
      timeline: [...authoritativeWorkOrder.timeline, {
        operation: parsed.value.assigneeId || parsed.value.teamId ? 'ASSIGN' : 'UNASSIGN',
        fromStatus: 'OPEN',
        toStatus: 'OPEN',
        reason: parsed.value.reason,
        actorType: 'PRINCIPAL',
        actorId: selectedSubject,
        occurredAt: '2026-08-01T00:01:00Z',
        version: authoritativeWorkOrder.version + 1,
      }],
    };
    json(response, 200, authoritativeWorkOrder);
    return;
  }

  problem(response, 404, 'ROUTE_NOT_FOUND', 'The requested Work Order lifecycle mutation is not reviewed.');
});

async function availablePort() {
  const socket = createTCPServer();
  socket.listen(0, '127.0.0.1');
  await once(socket, 'listening');
  const address = socket.address();
  await new Promise((resolveClose) => socket.close(resolveClose));
  return address.port;
}

async function cdp(url) {
  const socket = new WebSocket(url);
  const pending = new Map();
  let id = 0;
  await once(socket, 'open');
  socket.on('message', (raw) => {
    const message = JSON.parse(String(raw));
    if (!message.id) return;
    const item = pending.get(message.id);
    if (!item) return;
    pending.delete(message.id);
    if (message.error) item.reject(new Error(message.error.message));
    else item.resolve(message.result);
  });
  return {
    send(method, params = {}) {
      const commandId = ++id;
      socket.send(JSON.stringify({ id: commandId, method, params }));
      return new Promise((resolveCommand, rejectCommand) => {
        pending.set(commandId, { resolve: resolveCommand, reject: rejectCommand });
      });
    },
    close() {
      socket.close();
    },
  };
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text);
  }
  return response.result.value;
}

async function wait(client, expression, label) {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const value = await evaluate(client, expression);
      if (value) return value;
    } catch {}
    await pause(100);
  }
  throw new Error(`${label} did not become ready`);
}

async function stop(child) {
  if (!child || child.exitCode !== null) return;
  child.kill('SIGTERM');
  const done = await Promise.race([
    once(child, 'exit').then(() => true),
    pause(1500).then(() => false),
  ]);
  if (!done && process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  } else if (!done) {
    child.kill('SIGKILL');
  }
}

const candidates = [
  process.env.BROWSER_BINARY,
  process.env['PROGRAMFILES(X86)']
    ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe')
    : null,
  process.env.PROGRAMFILES
    ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe')
    : null,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium-browser',
  '/usr/bin/chromium',
].filter(Boolean);
const browserPath = candidates.find((candidate) => existsSync(candidate));
if (!browserPath) throw new Error('A CDP-compatible browser was not found');

const port = await availablePort();
const debugPort = await availablePort();
let browser;
let client;
let passed = false;
const assertions = [];

try {
  await mkdir(outputRoot, { recursive: true });
  await mkdir(profileDir, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    server.once('error', rejectListen);
    server.listen(port, '127.0.0.1', resolveListen);
  });
  browser = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--disable-extensions',
    '--no-sandbox',
    '--no-first-run',
    '--no-default-browser-check',
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profileDir}`,
    'about:blank',
  ], { stdio: 'ignore' });
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break;
    } catch {}
    if (attempt === 299) throw new Error('browser debugger not ready');
    await pause(100);
  }

  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  client = await cdp(pages.find((page) => page.type === 'page').webSocketDebuggerUrl);
  await client.send('Runtime.enable');
  await client.send('Page.enable');
  await client.send('Page.navigate', { url: `http://127.0.0.1:${port}/` });

  await wait(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__?.initial`, 'initial Work Order create');
  const created = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.initial`);
  assert(created.status === 201 && created.body.workOrderId === workOrderId && created.body.version === 1, 'authorized create did not return the authoritative version-1 Work Order');

  const assigned = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.assignSelected()`);
  assert(assigned.status === 200 && assigned.body.version === 2 && assigned.body.assigneeId === assigneeId, 'authorized assignment did not return the authoritative version-2 Work Order');
  assertions.push('authorized-create-assign');

  const replayed = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.retryCreate()`);
  assert(replayed.status === 200 && replayed.replayed === 'true' && replayed.body.workOrderId === workOrderId, 'exact create retry was not replayed');
  assertions.push('exact-idempotent-retry');

  const stale = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.staleAssign()`);
  assert(stale.status === 409 && stale.body.code === 'VERSION_CONFLICT', 'stale assignment did not return VERSION_CONFLICT');
  assertions.push('stale-version-conflict');

  const authorizationDenied = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.denyAuthorization()`);
  assert(authorizationDenied.status === 403 && authorizationDenied.body.code === 'WORK_ORDER_ACCESS_DENIED', 'authorization denial did not fail closed');
  assert(!(await evaluate(client, `document.body.innerText.includes(${JSON.stringify(createRequest.title)})`)), 'authorization denial retained protected Work Order data');
  assertions.push('authorization-denial-no-data');

  const denied = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.switchDenied()`);
  assert(denied.status === 404 && denied.body.code === 'ROUTE_NOT_FOUND', 'non-selected Session did not receive route absence');
  assert(!(await evaluate(client, `document.body.innerText.includes(${JSON.stringify(createRequest.title)})`)), 'non-selected Session retained protected Work Order data');
  assertions.push('non-selected-session-route-absence');

  const crossSite = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.switchSite()`);
  assert(crossSite.status === 404 && crossSite.body.code === 'RESOURCE_NOT_FOUND', 'cross-Site create did not fail generically');
  assertions.push('cross-site-nondiscovery');

  const lost = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.loseSession()`);
  assert(lost.status === 401 && lost.body.code === 'SESSION_REQUIRED', 'Session loss did not fail closed');
  assert(!(await evaluate(client, `document.body.innerText.includes(${JSON.stringify(createRequest.title)})`)), 'Session loss retained protected Work Order data');
  assertions.push('session-loss-purge');

  const lifecycle = await evaluate(client, `globalThis.__WORK_ORDER_MUTATION_AUDIT__.unreviewedLifecycle()`);
  assert(lifecycle.status === 404 && lifecycle.body.code === 'ROUTE_NOT_FOUND', 'unreviewed lifecycle mutation was exposed');
  assertions.push('unreviewed-lifecycle-absence');

  const apiRequests = requests.filter((entry) => entry.path.includes('/work-orders'));
  assert(apiRequests.length >= 9, 'browser audit did not exercise Work Order create/assignment boundaries');
  assert(apiRequests.every((entry) => entry.method === 'POST'), 'browser issued a non-mutation request to the Work Order write canary');
  assert(apiRequests.every((entry) => entry.path.startsWith('/api/v1/sites/')), 'browser bypassed the public Gateway path');
  assert(apiRequests.every((entry) => entry.headers['x-csrf-token'] === csrfToken), 'browser omitted the public CSRF proof');
  assert(apiRequests.every((entry) => typeof entry.headers['idempotency-key'] === 'string'), 'browser omitted mutation idempotency keys');
  assert(apiRequests.every((entry) => !Object.keys(entry.headers).some((name) => forbiddenAuthorityHeaders.has(name))), 'browser supplied Work Order authority headers');
  assertions.push('public-gateway-mutation-only');

  passed = true;
  await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({
    schemaVersion: 1,
    passed: true,
    generatedAt: new Date().toISOString(),
    assertions,
    cohort: {
      percentage: 1,
      salt,
      group,
      revision,
      selectedSubject,
      selectedBucket: cohortBucket(selectedSubject),
      authorizationDeniedSubject,
      authorizationDeniedBucket: cohortBucket(authorizationDeniedSubject),
      deniedSubject,
      deniedBucket: cohortBucket(deniedSubject),
    },
    network: { requests },
    safety: {
      publicGatewayOnly: true,
      csrfRequired: true,
      exactIdempotency: true,
      optimisticConcurrency: true,
      lifecycleWrites: false,
      fallbackOwner: false,
      shadowOwner: false,
    },
  }, null, 2));
  console.log(`S5 Work Order create/assign browser audit passed. Evidence: ${join(outputRoot, 'browser-evidence.json')}`);
} finally {
  client?.close();
  await stop(browser);
  await new Promise((resolveClose) => server.close(() => resolveClose()));
  try {
    await rm(profileDir, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 });
  } catch {}
  if (!passed) {
    await mkdir(outputRoot, { recursive: true });
    await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({
      schemaVersion: 1,
      passed: false,
      generatedAt: new Date().toISOString(),
      assertions,
      network: { requests },
    }, null, 2));
  }
}

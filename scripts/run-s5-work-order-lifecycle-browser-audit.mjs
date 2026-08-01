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
const outputRoot = resolve(root, 'out/s5-work-order-lifecycle');
const profileDir = join(tmpdir(), `s5-work-order-lifecycle-${process.pid}`);
const organizationId = '01910000-0000-7000-8000-000000000001';
const siteId = '01910000-0001-7000-8000-000000000001';
const otherSiteId = '01910000-0002-7000-8000-000000000001';
const workOrderId = '01930000-5000-7000-8000-000000000001';
const sourceAlarmId = '01910000-4000-7000-8000-000000000001';
const assigneeId = '01910000-9000-7000-8000-000000000001';
const browserProof = ['work', 'order', 'lifecycle', 'proof'].join('-');
const salt = 's5-work-order-lifecycle-canary-v1';
const group = 's5-work-order-lifecycle-v1';
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
    const subject = `work-order-lifecycle-${label}-${index}`;
    if ((cohortBucket(subject) < 1) === selected) return subject;
  }
  throw new Error('unable to resolve deterministic Work Order lifecycle cohort subject');
}

const selectedSubject = findSubject(true, 'selected');
const authorizationDeniedSubject = findSubject(true, 'authorization-denied');
const deniedSubject = findSubject(false, 'denied');
const requests = [];
const idempotency = new Map();

function initialWorkOrder() {
  return {
    schemaVersion: 1,
    workOrderId,
    organizationId,
    siteId,
    title: 'Inspect authoritative AHU fan vibration',
    description: 'Governed Work Order lifecycle certification.',
    priority: 'HIGH',
    status: 'OPEN',
    sourceReferences: [{ domain: 'ALARM', resourceId: sourceAlarmId, relationship: 'ORIGIN' }],
    assigneeId,
    tasks: { total: 0, completed: 0, blocked: 0 },
    noteCount: 0,
    attachmentCount: 0,
    completionEvidence: [],
    timeline: [{ operation: 'CREATE', toStatus: 'OPEN', reason: 'WORK_ORDER_CREATED', actorType: 'PRINCIPAL', actorId: selectedSubject, assigneeId, occurredAt: '2026-08-02T00:00:00Z', version: 1 }],
    version: 1,
    createdAt: '2026-08-02T00:00:00Z',
    updatedAt: '2026-08-02T00:00:00Z',
  };
}

let authoritativeWorkOrder = initialWorkOrder();
let clockMinute = 1;
const clone = (value) => JSON.parse(JSON.stringify(value));
function instant() {
  const value = `2026-08-03T${String(Math.floor(clockMinute / 60)).padStart(2, '0')}:${String(clockMinute % 60).padStart(2, '0')}:00Z`;
  clockMinute += 1;
  return value;
}

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
  return `<!doctype html><meta charset="utf-8"><title>Work Order lifecycle canary audit</title>
  <main data-testid="work-order-lifecycle-audit" data-state="EMPTY"><h1>Work Order lifecycle canary</h1><pre data-testid="result"></pre></main>
  <script>
  const siteId=${JSON.stringify(siteId)}, otherSiteId=${JSON.stringify(otherSiteId)}, workOrderId=${JSON.stringify(workOrderId)};
  const selected=${JSON.stringify(selectedSubject)}, authorizationDenied=${JSON.stringify(authorizationDeniedSubject)}, denied=${JSON.stringify(deniedSubject)};
  const proof=${JSON.stringify(browserProof)};
  const main=document.querySelector('[data-testid="work-order-lifecycle-audit"]'), result=document.querySelector('[data-testid="result"]');
  const setSubject=(value)=>{document.cookie='subject='+encodeURIComponent(value)+'; path=/; SameSite=Lax';};
  const clear=()=>{result.textContent='';main.dataset.state='DENIED';};
  async function mutate(action,body,key,targetSite=siteId,suffix=action){
    const response=await fetch('/api/v1/sites/'+targetSite+'/work-orders/'+workOrderId+':'+suffix,{method:'POST',headers:{accept:'application/json','content-type':'application/json','x-csrf-token':proof,'idempotency-key':key},body:JSON.stringify(body)});
    const payload=await response.json();
    if(!response.ok){clear();result.textContent=payload.code;}else{main.dataset.state='READY';result.textContent=[payload.title,payload.status,'v'+payload.version,payload.completionEvidence?.length??0].join(' | ');}
    return {status:response.status,body:payload,replayed:response.headers.get('idempotency-replayed')};
  }
  globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__={
    plan:async()=>{setSubject(selected);return mutate('plan',{expectedVersion:1,scheduledStart:'2026-08-03T01:00:00Z',dueAt:'2026-08-03T04:00:00Z',reason:'plan maintenance window'},'plan-browser-0001');},
    replayPlan:async()=>{setSubject(selected);return mutate('plan',{expectedVersion:1,scheduledStart:'2026-08-03T01:00:00Z',dueAt:'2026-08-03T04:00:00Z',reason:'plan maintenance window'},'plan-browser-0001');},
    start:async()=>{setSubject(selected);return mutate('start',{expectedVersion:2,reason:'begin repair'},'start-browser-0001');},
    illegalStart:async()=>{setSubject(selected);return mutate('start',{expectedVersion:3,reason:'start again'},'start-browser-illegal-0001');},
    crossActionKey:async()=>{setSubject(selected);return mutate('block',{expectedVersion:3,reason:'reuse start key'},'start-browser-0001');},
    staleBlock:async()=>{setSubject(selected);return mutate('block',{expectedVersion:2,reason:'stale block'},'block-browser-stale-0001');},
    block:async()=>{setSubject(selected);return mutate('block',{expectedVersion:3,reason:'replacement part unavailable'},'block-browser-0001');},
    resume:async()=>{setSubject(selected);return mutate('resume',{expectedVersion:4,reason:'replacement part received'},'resume-browser-0001');},
    missingEvidence:async()=>{setSubject(selected);return mutate('complete',{expectedVersion:5,reason:'complete without evidence'},'complete-browser-missing-0001');},
    complete:async()=>{setSubject(selected);return mutate('complete',{expectedVersion:5,completionEvidence:[{kind:'verification-report',reference:'object://reports/ahu-17',capturedAt:'2026-08-03T02:30:00Z'}],reason:'repair verified'},'complete-browser-0001');},
    reopen:async()=>{setSubject(selected);return mutate('reopen',{expectedVersion:6,reason:'vibration recurred'},'reopen-browser-0001');},
    cancel:async()=>{setSubject(selected);return mutate('cancel',{expectedVersion:7,reason:'asset retired'},'cancel-browser-0001');},
    denyAuthorization:async()=>{setSubject(authorizationDenied);return mutate('reopen',{expectedVersion:8,reason:'denied'},'reopen-browser-denied-0001');},
    switchDenied:async()=>{setSubject(denied);return mutate('reopen',{expectedVersion:8,reason:'non-selected'},'reopen-browser-nonselected-0001');},
    switchSite:async()=>{setSubject(selected);return mutate('reopen',{expectedVersion:8,reason:'cross site'},'reopen-browser-cross-site-0001',otherSiteId);},
    loseSession:async()=>{document.cookie='subject=; Max-Age=0; path=/';return mutate('reopen',{expectedVersion:8,reason:'session loss'},'reopen-browser-session-loss-0001');},
    unreviewedCollaboration:async()=>{setSubject(selected);return mutate('add-note',{expectedVersion:8,note:'not reviewed'},'note-browser-0001',siteId,'add-note');},
  };
  setSubject(selected);
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

const allowedActions = new Set(['plan', 'start', 'block', 'resume', 'complete', 'cancel', 'reopen']);

function transition(action, body, subject) {
  if (body.expectedVersion !== authoritativeWorkOrder.version) return { error: [409, 'VERSION_CONFLICT', 'The Work Order changed before the lifecycle action committed.'] };
  const before = clone(authoritativeWorkOrder);
  const fromStatus = before.status;
  let toStatus = fromStatus;
  let operation = action.toUpperCase();
  const at = instant();
  switch (action) {
    case 'plan':
      if (fromStatus !== 'OPEN' || !Object.hasOwn(body, 'scheduledStart') || !Object.hasOwn(body, 'dueAt') || (!body.scheduledStart && !body.dueAt)) return { error: [422, 'WORK_ORDER_LIFECYCLE_INVALID', 'The plan transition is invalid.'] };
      authoritativeWorkOrder.scheduledStart = body.scheduledStart ?? undefined;
      authoritativeWorkOrder.dueAt = body.dueAt ?? undefined;
      operation = 'SCHEDULE';
      break;
    case 'start':
      if (fromStatus !== 'OPEN' || (!before.assigneeId && !before.teamId)) return { error: [422, 'WORK_ORDER_LIFECYCLE_INVALID', 'The start transition is invalid.'] };
      toStatus = 'IN_PROGRESS';
      break;
    case 'block':
      if (fromStatus !== 'IN_PROGRESS') return { error: [422, 'WORK_ORDER_LIFECYCLE_INVALID', 'The block transition is invalid.'] };
      toStatus = 'BLOCKED';
      break;
    case 'resume':
      if (fromStatus !== 'BLOCKED') return { error: [422, 'WORK_ORDER_LIFECYCLE_INVALID', 'The resume transition is invalid.'] };
      toStatus = 'IN_PROGRESS';
      break;
    case 'complete': {
      if (fromStatus !== 'IN_PROGRESS' || !Array.isArray(body.completionEvidence) || body.completionEvidence.length === 0 || before.tasks.completed !== before.tasks.total || before.tasks.blocked !== 0) return { error: [422, 'WORK_ORDER_LIFECYCLE_INVALID', 'Completion evidence and converged tasks are required.'] };
      const seen = new Set(before.completionEvidence.map((item) => `${item.kind}\0${item.reference}`));
      for (const evidence of body.completionEvidence) {
        if (!evidence.kind || !evidence.reference || !evidence.capturedAt || seen.has(`${evidence.kind}\0${evidence.reference}`)) return { error: [422, 'WORK_ORDER_LIFECYCLE_INVALID', 'Completion evidence is invalid.'] };
        seen.add(`${evidence.kind}\0${evidence.reference}`);
      }
      authoritativeWorkOrder.completionEvidence = [...before.completionEvidence, ...body.completionEvidence];
      toStatus = 'COMPLETED';
      break;
    }
    case 'cancel':
      if (!['OPEN', 'IN_PROGRESS', 'BLOCKED'].includes(fromStatus)) return { error: [422, 'WORK_ORDER_LIFECYCLE_INVALID', 'The cancel transition is invalid.'] };
      toStatus = 'CANCELLED';
      break;
    case 'reopen':
      if (!['COMPLETED', 'CANCELLED'].includes(fromStatus)) return { error: [422, 'WORK_ORDER_LIFECYCLE_INVALID', 'The reopen transition is invalid.'] };
      toStatus = 'OPEN';
      break;
    default:
      return { error: [404, 'ROUTE_NOT_FOUND', 'The requested collaboration route is not reviewed.'] };
  }
  authoritativeWorkOrder.status = toStatus;
  authoritativeWorkOrder.version = before.version + 1;
  authoritativeWorkOrder.updatedAt = at;
  authoritativeWorkOrder.timeline = [...before.timeline, { operation, fromStatus, toStatus, reason: body.reason, actorType: 'PRINCIPAL', actorId: subject, occurredAt: at, version: before.version + 1 }];
  return { value: clone(authoritativeWorkOrder) };
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? '/', 'http://fixture');
  if (url.pathname === '/' || url.pathname === '/favicon.ico') {
    if (url.pathname === '/favicon.ico') { response.writeHead(204); response.end(); return; }
    response.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' }); response.end(html()); return;
  }
  const subject = cookieValue(request, 'subject');
  const bucket = subject ? cohortBucket(subject) : null;
  const recordedHeaders = Object.fromEntries(Object.entries(request.headers).filter(([name]) => name.startsWith('x-') || name === 'idempotency-key' || name === 'content-type' || name === 'origin'));
  requests.push({ method: request.method, path: url.pathname, subject, bucket, headers: recordedHeaders });
  if (request.method !== 'POST') { problem(response, 405, 'METHOD_NOT_ALLOWED', 'Work Order lifecycle canary is POST-only.'); return; }
  if (Object.keys(request.headers).some((name) => forbiddenAuthorityHeaders.has(name))) { problem(response, 400, 'FORGED_IDENTITY_HEADER', 'Browser authority headers are rejected.'); return; }
  if (!subject) { problem(response, 401, 'SESSION_REQUIRED', 'The authenticated Session is required.'); return; }
  if (bucket >= 1) { problem(response, 404, 'ROUTE_NOT_FOUND', 'The requested route is not available.'); return; }
  if (subject === authorizationDeniedSubject) { problem(response, 403, 'WORK_ORDER_ACCESS_DENIED', 'The Work Order lifecycle action is not authorized.'); return; }
  if (request.headers.origin !== `http://127.0.0.1:${server.address().port}` || request.headers['x-csrf-token'] !== browserProof) { problem(response, 403, 'CSRF_INVALID', 'The request Origin or CSRF proof is invalid.'); return; }
  const idempotencyKey = String(request.headers['idempotency-key'] ?? '');
  if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$/.test(idempotencyKey)) { problem(response, 400, 'WORK_ORDER_LIFECYCLE_INVALID', 'A bounded Idempotency-Key is required.'); return; }
  const match = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/work-orders\/([^/:]+):([^/]+)$/);
  if (!match || match[1] !== siteId || match[2] !== workOrderId) { problem(response, 404, 'RESOURCE_NOT_FOUND', 'The Work Order resource is not visible.'); return; }
  const action = match[3];
  if (!allowedActions.has(action)) { problem(response, 404, 'ROUTE_NOT_FOUND', 'The requested collaboration route is not reviewed.'); return; }
  let parsed;
  try { parsed = await readJSONBody(request); } catch { problem(response, 400, 'WORK_ORDER_LIFECYCLE_INVALID', 'The lifecycle body is invalid.'); return; }
  const replayKey = `${subject}\0${workOrderId}\0${idempotencyKey}`;
  const prior = idempotency.get(replayKey);
  if (prior) {
    if (prior.raw !== parsed.raw) { problem(response, 409, 'IDEMPOTENCY_CONFLICT', 'The Idempotency-Key was committed with a different request.'); return; }
    json(response, 200, prior.value, { 'Idempotency-Replayed': 'true' }); return;
  }
  const result = transition(action, parsed.value, subject);
  if (result.error) { problem(response, ...result.error); return; }
  idempotency.set(replayKey, { raw: parsed.raw, value: result.value });
  json(response, 200, result.value);
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
  await mkdir(outputRoot, { recursive: true }); await mkdir(profileDir, { recursive: true });
  await new Promise((resolveListen, rejectListen) => { server.once('error', rejectListen); server.listen(port, '127.0.0.1', resolveListen); });
  browser = spawn(browserPath, ['--headless=new','--disable-gpu','--disable-extensions','--no-sandbox','--no-first-run','--no-default-browser-check',`--remote-debugging-port=${debugPort}`,`--user-data-dir=${profileDir}`,'about:blank'], { stdio: 'ignore' });
  for (let attempt=0;attempt<300;attempt+=1){try{if((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok)break;}catch{}if(attempt===299)throw new Error('browser debugger not ready');await pause(100);}
  const pages=await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response)=>response.json());
  client=await cdp(pages.find((page)=>page.type==='page').webSocketDebuggerUrl); await client.send('Runtime.enable'); await client.send('Page.enable'); await client.send('Page.navigate',{url:`http://127.0.0.1:${port}/`});
  await wait(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__`,'Work Order lifecycle audit');
  const planned=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.plan()`); assert(planned.status===200&&planned.body.status==='OPEN'&&planned.body.version===2&&planned.body.timeline.at(-1).operation==='SCHEDULE','plan did not commit OPEN-to-OPEN');
  const replayed=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.replayPlan()`); assert(replayed.status===200&&replayed.replayed==='true'&&replayed.body.version===2,'exact plan retry was not replayed'); assertions.push('exact-idempotent-retry');
  const started=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.start()`); assert(started.status===200&&started.body.status==='IN_PROGRESS'&&started.body.version===3,'start did not enter IN_PROGRESS');
  const illegal=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.illegalStart()`); assert(illegal.status===422&&illegal.body.code==='WORK_ORDER_LIFECYCLE_INVALID','illegal repeated start did not fail closed'); assertions.push('illegal-transition');
  const crossAction=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.crossActionKey()`); assert(crossAction.status===409&&crossAction.body.code==='IDEMPOTENCY_CONFLICT','cross-action key reuse did not conflict'); assertions.push('cross-action-idempotency-conflict');
  const stale=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.staleBlock()`); assert(stale.status===409&&stale.body.code==='VERSION_CONFLICT','stale transition did not return VERSION_CONFLICT'); assertions.push('stale-version-conflict');
  const blocked=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.block()`); assert(blocked.status===200&&blocked.body.status==='BLOCKED'&&blocked.body.version===4,'block did not enter BLOCKED');
  const resumed=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.resume()`); assert(resumed.status===200&&resumed.body.status==='IN_PROGRESS'&&resumed.body.version===5,'resume did not return to IN_PROGRESS');
  const missing=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.missingEvidence()`); assert(missing.status===422&&missing.body.code==='WORK_ORDER_LIFECYCLE_INVALID','complete without evidence did not fail closed'); assertions.push('missing-completion-evidence');
  const completed=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.complete()`); assert(completed.status===200&&completed.body.status==='COMPLETED'&&completed.body.version===6&&completed.body.completionEvidence.length===1,'complete did not append evidence');
  const reopened=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.reopen()`); assert(reopened.status===200&&reopened.body.status==='OPEN'&&reopened.body.version===7&&reopened.body.completionEvidence.length===1,'reopen did not preserve evidence');
  const cancelled=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.cancel()`); assert(cancelled.status===200&&cancelled.body.status==='CANCELLED'&&cancelled.body.version===8&&cancelled.body.timeline.length===8,'cancel did not finish the legal graph'); assertions.push('legal-lifecycle-graph');
  const authDenied=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.denyAuthorization()`); assert(authDenied.status===403&&authDenied.body.code==='WORK_ORDER_ACCESS_DENIED','authorization denial did not fail closed'); assert(!(await evaluate(client,`document.body.innerText.includes(${JSON.stringify(assigneeId)})`)),'authorization denial retained protected data'); assertions.push('authorization-denial-no-data');
  const denied=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.switchDenied()`); assert(denied.status===404&&denied.body.code==='ROUTE_NOT_FOUND','non-selected Session did not receive route absence'); assertions.push('non-selected-session-route-absence');
  const crossSite=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.switchSite()`); assert(crossSite.status===404&&crossSite.body.code==='RESOURCE_NOT_FOUND','cross-Site action did not fail generically'); assertions.push('cross-site-nondiscovery');
  const lost=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.loseSession()`); assert(lost.status===401&&lost.body.code==='SESSION_REQUIRED','Session loss did not fail closed'); assert(!(await evaluate(client,`document.body.innerText.includes(${JSON.stringify(assigneeId)})`)),'Session loss retained protected data'); assertions.push('session-loss-purge');
  const unreviewed=await evaluate(client,`globalThis.__WORK_ORDER_LIFECYCLE_AUDIT__.unreviewedCollaboration()`); assert(unreviewed.status===404&&unreviewed.body.code==='ROUTE_NOT_FOUND','unreviewed collaboration route was exposed'); assertions.push('unreviewed-collaboration-absence');
  const apiRequests=requests.filter((entry)=>entry.path.includes('/work-orders')); assert(apiRequests.length>=17,'browser audit did not exercise lifecycle boundaries'); assert(apiRequests.every((entry)=>entry.method==='POST'),'browser issued a non-lifecycle request'); assert(apiRequests.every((entry)=>entry.path.startsWith('/api/v1/sites/')),'browser bypassed public Gateway paths'); assert(apiRequests.every((entry)=>entry.headers['x-csrf-token']===browserProof),'browser omitted CSRF proof'); assert(apiRequests.every((entry)=>typeof entry.headers['idempotency-key']==='string'),'browser omitted idempotency proof'); assert(apiRequests.every((entry)=>!Object.keys(entry.headers).some((name)=>forbiddenAuthorityHeaders.has(name))),'browser supplied authority headers'); assertions.push('public-gateway-lifecycle-only');
  passed=true;
  await writeFile(join(outputRoot,'browser-evidence.json'),JSON.stringify({schemaVersion:1,passed:true,generatedAt:new Date().toISOString(),assertions,cohort:{percentage:1,salt,group,revision,selectedSubject,selectedBucket:cohortBucket(selectedSubject),authorizationDeniedSubject,authorizationDeniedBucket:cohortBucket(authorizationDeniedSubject),deniedSubject,deniedBucket:cohortBucket(deniedSubject)},network:{requests},finalProjection:authoritativeWorkOrder,safety:{publicGatewayOnly:true,csrfOriginBound:true,exactIdempotency:true,optimisticConcurrency:true,completionEvidenceAppendOnly:true,unreviewedCollaborationAbsent:true,fallbackOwner:false,shadowOwner:false}},null,2));
  console.log(`S5 Work Order lifecycle browser audit passed. Evidence: ${join(outputRoot,'browser-evidence.json')}`);
} finally {
  client?.close(); await stop(browser); await new Promise((resolveClose)=>server.close(()=>resolveClose())); try{await rm(profileDir,{recursive:true,force:true,maxRetries:8,retryDelay:250});}catch{}
  if(!passed){await mkdir(outputRoot,{recursive:true});await writeFile(join(outputRoot,'browser-evidence.json'),JSON.stringify({schemaVersion:1,passed:false,generatedAt:new Date().toISOString(),assertions,network:{requests}},null,2));}
}

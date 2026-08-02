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
const outputRoot = resolve(root, 'out/s5-work-order-tasks');
const profileDir = join(tmpdir(), `s5-work-order-tasks-${process.pid}`);
const organizationId = '01910000-0000-7000-8000-000000000001';
const siteId = '01910000-0001-7000-8000-000000000001';
const otherSiteId = '01910000-0002-7000-8000-000000000001';
const workOrderId = '01930000-5000-7000-8000-000000000001';
const taskOneId = '01930000-6000-7000-8000-000000000001';
const taskTwoId = '01930000-6000-7000-8000-000000000002';
const browserProof = ['work', 'order', 'task', 'proof'].join('-');
const salt = 's5-work-order-task-canary-v1';
const group = 's5-work-order-task-v1';
const revision = 1;
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const clone = (value) => JSON.parse(JSON.stringify(value));

function cohortBucket(subject) {
  const digest = createHash('sha256').update(`${salt}\0${group}\0${revision}\0${organizationId}\0${subject}`).digest();
  return Number(digest.readBigUInt64BE(0) % 100n);
}

function findSubject(selected, label) {
  for (let index = 0; index < 100000; index += 1) {
    const subject = `work-order-task-${label}-${index}`;
    if ((cohortBucket(subject) < 1) === selected) return subject;
  }
  throw new Error('unable to resolve deterministic Work Order task cohort subject');
}

const selectedSubject = findSubject(true, 'selected');
const authorizationDeniedSubject = findSubject(true, 'authorization-denied');
const deniedSubject = findSubject(false, 'denied');
const requests = [];
const idempotency = new Map();
let workOrderVersion = 1;
let clockMinute = 1;
let tasks = [];

const summary = () => ({
  total: tasks.length,
  completed: tasks.filter((task) => task.status === 'COMPLETED').length,
  blocked: tasks.filter((task) => task.status === 'BLOCKED').length,
});
const checklist = () => ({ schemaVersion: 1, organizationId, siteId, workOrderId, workOrderVersion, summary: summary(), tasks: clone(tasks) });
const instant = () => {
  const value = `2026-08-04T00:${String(clockMinute).padStart(2, '0')}:00Z`;
  clockMinute += 1;
  return value;
};

function problem(response, status, code, detail) {
  response.writeHead(status, { 'content-type': 'application/problem+json', 'cache-control': 'private, no-store' });
  response.end(JSON.stringify({ type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`, title: code, status, detail, code, retryable: false }));
}
function json(response, status, payload, headers = {}) {
  response.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'private, no-store', ...headers });
  response.end(JSON.stringify(payload));
}
function cookieValue(request, key) {
  const match = String(request.headers.cookie ?? '').split(';').map((part) => part.trim()).find((part) => part.startsWith(`${key}=`));
  return match ? decodeURIComponent(match.slice(key.length + 1)) : '';
}
async function readJSONBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const raw = Buffer.concat(chunks).toString('utf8');
  return { raw, value: JSON.parse(raw) };
}

function html() {
  return `<!doctype html><meta charset="utf-8"><title>Work Order task canary audit</title>
  <main data-testid="work-order-task-audit" data-state="EMPTY"><h1>Work Order task canary</h1><pre data-testid="result"></pre></main>
  <script>
  const siteId=${JSON.stringify(siteId)}, otherSiteId=${JSON.stringify(otherSiteId)}, workOrderId=${JSON.stringify(workOrderId)}, taskOneId=${JSON.stringify(taskOneId)}, taskTwoId=${JSON.stringify(taskTwoId)};
  const selected=${JSON.stringify(selectedSubject)}, authorizationDenied=${JSON.stringify(authorizationDeniedSubject)}, denied=${JSON.stringify(deniedSubject)}, proof=${JSON.stringify(browserProof)};
  const main=document.querySelector('[data-testid="work-order-task-audit"]'), result=document.querySelector('[data-testid="result"]');
  const setSubject=(value)=>{document.cookie='subject='+encodeURIComponent(value)+'; path=/; SameSite=Lax';};
  const clear=()=>{result.textContent='';main.dataset.state='DENIED';};
  async function call(method,path,body,key){
    const headers={accept:'application/json'};
    if(method==='POST'){headers['content-type']='application/json';headers['x-csrf-token']=proof;headers['idempotency-key']=key;}
    const response=await fetch(path,{method,headers,body:body===undefined?undefined:JSON.stringify(body)});
    const payload=await response.json();
    if(!response.ok){clear();result.textContent=payload.code;}else{main.dataset.state='READY';result.textContent=[payload.workOrderVersion,payload.summary.total,payload.summary.completed,payload.tasks.map((task)=>task.taskId+':'+task.status).join(',')].join(' | ');}
    return {status:response.status,body:payload,replayed:response.headers.get('idempotency-replayed')};
  }
  const base=(targetSite=siteId)=>'/api/v1/sites/'+targetSite+'/work-orders/'+workOrderId;
  globalThis.__WORK_ORDER_TASK_AUDIT__={
    list:async()=>{setSubject(selected);return call('GET',base()+'/tasks');},
    appendOne:async()=>{setSubject(selected);return call('POST',base()+'/tasks',{expectedWorkOrderVersion:1,title:'Inspect fan bearings',reason:'append first task'},'task-browser-0001');},
    replayAppendOne:async()=>{setSubject(selected);return call('POST',base()+'/tasks',{expectedWorkOrderVersion:1,title:'Inspect fan bearings',reason:'append first task'},'task-browser-0001');},
    appendTwo:async()=>{setSubject(selected);return call('POST',base()+'/tasks',{expectedWorkOrderVersion:2,title:'Record vibration',reason:'append second task'},'task-browser-0002');},
    staleStatus:async()=>{setSubject(selected);return call('POST',base()+'/tasks/'+taskOneId+':status',{expectedWorkOrderVersion:2,expectedTaskVersion:1,status:'COMPLETED',reason:'stale'},'task-browser-stale-1');},
    completeOne:async()=>{setSubject(selected);return call('POST',base()+'/tasks/'+taskOneId+':status',{expectedWorkOrderVersion:3,expectedTaskVersion:1,status:'COMPLETED',reason:'inspection complete'},'task-browser-0003');},
    crossActionKey:async()=>{setSubject(selected);return call('POST',base()+'/tasks:reorder',{expectedWorkOrderVersion:4,taskIds:[taskTwoId,taskOneId],reason:'reuse append key'},'task-browser-0001');},
    duplicateOrder:async()=>{setSubject(selected);return call('POST',base()+'/tasks:reorder',{expectedWorkOrderVersion:4,taskIds:[taskOneId,taskOneId],reason:'duplicate'},'task-browser-duplicate-1');},
    reorder:async()=>{setSubject(selected);return call('POST',base()+'/tasks:reorder',{expectedWorkOrderVersion:4,taskIds:[taskTwoId,taskOneId],reason:'measure first'},'task-browser-0004');},
    completeTwo:async()=>{setSubject(selected);return call('POST',base()+'/tasks/'+taskTwoId+':status',{expectedWorkOrderVersion:5,expectedTaskVersion:2,status:'COMPLETED',reason:'vibration recorded'},'task-browser-0005');},
    denyAuthorization:async()=>{setSubject(authorizationDenied);return call('GET',base()+'/tasks');},
    switchDenied:async()=>{setSubject(denied);return call('GET',base()+'/tasks');},
    switchSite:async()=>{setSubject(selected);return call('GET',base(otherSiteId)+'/tasks');},
    loseSession:async()=>{document.cookie='subject=; Max-Age=0; path=/';return call('GET',base()+'/tasks');},
    unreviewedDelete:async()=>{setSubject(selected);return call('POST',base()+'/tasks/'+taskOneId+':delete',{expectedWorkOrderVersion:6},'task-browser-delete-1');},
    unreviewedTitleEdit:async()=>{setSubject(selected);return call('POST',base()+'/tasks/'+taskOneId+':title',{expectedWorkOrderVersion:6,title:'Retitled task'},'task-browser-title-1');},
  };
  setSubject(selected);
  </script>`;
}

const forbiddenAuthorityHeaders = new Set(['x-work-order-write-context','x-work-order-read-context','x-organization-id','x-site-id','x-work-order-id','x-task-id','x-delegation-grant','x-principal-subject']);

function applyMutation(action, body, taskId) {
  if (body.expectedWorkOrderVersion !== workOrderVersion) return { error: [409, 'VERSION_CONFLICT', 'The Work Order changed before the task mutation committed.'] };
  const before = clone(tasks);
  const at = instant();
  if (action === 'append') {
    const id = tasks.length === 0 ? taskOneId : taskTwoId;
    if (!body.title || tasks.length >= 2) return { error: [422, 'WORK_ORDER_TASK_INVALID', 'The task append is invalid.'] };
    tasks.push({ taskId: id, position: tasks.length, title: body.title, status: 'OPEN', version: 1, createdAt: at, updatedAt: at });
  } else if (action === 'status') {
    const task = tasks.find((candidate) => candidate.taskId === taskId);
    if (!task) return { error: [404, 'RESOURCE_NOT_FOUND', 'The task is not visible.'] };
    if (body.expectedTaskVersion !== task.version) return { error: [409, 'VERSION_CONFLICT', 'The task changed before the status mutation committed.'] };
    if (!['OPEN','BLOCKED','COMPLETED'].includes(body.status) || task.status === body.status) return { error: [422, 'WORK_ORDER_TASK_INVALID', 'The task status mutation is invalid.'] };
    task.status = body.status; task.version += 1; task.updatedAt = at;
  } else if (action === 'reorder') {
    if (!Array.isArray(body.taskIds) || body.taskIds.length !== tasks.length || new Set(body.taskIds).size !== tasks.length || body.taskIds.some((id) => !tasks.some((task) => task.taskId === id))) return { error: [422, 'WORK_ORDER_TASK_INVALID', 'The task order is not an exact full permutation.'] };
    if (body.taskIds.every((id, index) => tasks[index].taskId === id)) return { error: [422, 'WORK_ORDER_TASK_INVALID', 'The task order did not change.'] };
    const byId = new Map(tasks.map((task) => [task.taskId, task]));
    tasks = body.taskIds.map((id, position) => ({ ...byId.get(id), position, version: byId.get(id).version + 1, updatedAt: at }));
  } else {
    return { error: [404, 'ROUTE_NOT_FOUND', 'The requested task route is not reviewed.'] };
  }
  workOrderVersion += 1;
  return { value: checklist(), before };
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
  if (Object.keys(request.headers).some((name) => forbiddenAuthorityHeaders.has(name))) { problem(response, 400, 'FORGED_IDENTITY_HEADER', 'Browser authority headers are rejected.'); return; }
  if (!subject) { problem(response, 401, 'SESSION_REQUIRED', 'The authenticated Session is required.'); return; }
  if (bucket >= 1) { problem(response, 404, 'ROUTE_NOT_FOUND', 'The requested route is not available.'); return; }
  if (subject === authorizationDeniedSubject) { problem(response, 403, 'WORK_ORDER_ACCESS_DENIED', 'The Work Order task action is not authorized.'); return; }
  const match = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/work-orders\/([^/]+)\/(tasks(?::reorder)?|tasks\/([^/:]+):(status|delete|title))$/);
  if (!match || match[1] !== siteId || match[2] !== workOrderId) { problem(response, 404, 'RESOURCE_NOT_FOUND', 'The Work Order resource is not visible.'); return; }
  const route = match[3];
  if (route.endsWith(':delete') || route.endsWith(':title')) { problem(response, 404, 'ROUTE_NOT_FOUND', 'Task deletion and title editing are not reviewed.'); return; }
  if (request.method === 'GET') {
    if (route !== 'tasks') { problem(response, 405, 'METHOD_NOT_ALLOWED', 'The route is not readable.'); return; }
    json(response, 200, checklist()); return;
  }
  if (request.method !== 'POST') { problem(response, 405, 'METHOD_NOT_ALLOWED', 'Task mutation is POST-only.'); return; }
  if (request.headers.origin !== `http://127.0.0.1:${server.address().port}` || request.headers['x-csrf-token'] !== browserProof) { problem(response, 403, 'CSRF_INVALID', 'The request Origin or CSRF proof is invalid.'); return; }
  const idempotencyKey = String(request.headers['idempotency-key'] ?? '');
  if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$/.test(idempotencyKey)) { problem(response, 400, 'WORK_ORDER_TASK_INVALID', 'A bounded Idempotency-Key is required.'); return; }
  let parsed;
  try { parsed = await readJSONBody(request); } catch { problem(response, 400, 'WORK_ORDER_TASK_INVALID', 'The task body is invalid.'); return; }
  const action = route === 'tasks' ? 'append' : route === 'tasks:reorder' ? 'reorder' : 'status';
  const replayKey = `${subject}\0${workOrderId}\0${idempotencyKey}`;
  const prior = idempotency.get(replayKey);
  if (prior) {
    if (prior.raw !== parsed.raw || prior.action !== action) { problem(response, 409, 'IDEMPOTENCY_CONFLICT', 'The Idempotency-Key was committed with a different task request.'); return; }
    json(response, action === 'append' ? 200 : 200, prior.value, { 'Idempotency-Replayed': 'true' }); return;
  }
  const result = applyMutation(action, parsed.value, match[4]);
  if (result.error) { problem(response, ...result.error); return; }
  idempotency.set(replayKey, { raw: parsed.raw, action, value: result.value });
  json(response, action === 'append' ? 201 : 200, result.value);
});

async function availablePort() { const socket=createTCPServer(); socket.listen(0,'127.0.0.1'); await once(socket,'listening'); const address=socket.address(); await new Promise((resolveClose)=>socket.close(resolveClose)); return address.port; }
async function cdp(url) { const socket=new WebSocket(url); const pending=new Map(); let id=0; await once(socket,'open'); socket.on('message',(raw)=>{const message=JSON.parse(String(raw));if(!message.id)return;const item=pending.get(message.id);if(!item)return;pending.delete(message.id);if(message.error)item.reject(new Error(message.error.message));else item.resolve(message.result);}); return {send(method,params={}){const commandId=++id;socket.send(JSON.stringify({id:commandId,method,params}));return new Promise((resolveCommand,rejectCommand)=>pending.set(commandId,{resolve:resolveCommand,reject:rejectCommand}));},close(){socket.close();}}; }
async function evaluate(client,expression){const response=await client.send('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(response.exceptionDetails)throw new Error(response.exceptionDetails.exception?.description??response.exceptionDetails.text);return response.result.value;}
async function wait(client,expression,label){for(let attempt=0;attempt<300;attempt+=1){try{const value=await evaluate(client,expression);if(value)return value;}catch{}await pause(100);}throw new Error(`${label} did not become ready`);}
async function stop(child){if(!child||child.exitCode!==null)return;child.kill('SIGTERM');const done=await Promise.race([once(child,'exit').then(()=>true),pause(1500).then(()=>false)]);if(!done&&process.platform==='win32')spawnSync('taskkill',['/PID',String(child.pid),'/T','/F'],{stdio:'ignore'});else if(!done)child.kill('SIGKILL');}

const candidates=[process.env.BROWSER_BINARY,process.env['PROGRAMFILES(X86)']?join(process.env['PROGRAMFILES(X86)'],'Microsoft','Edge','Application','msedge.exe'):null,process.env.PROGRAMFILES?join(process.env.PROGRAMFILES,'Microsoft','Edge','Application','msedge.exe'):null,'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe','C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe','/usr/bin/google-chrome','/usr/bin/google-chrome-stable','/usr/bin/chromium-browser','/usr/bin/chromium'].filter(Boolean);
const browserPath=candidates.find((candidate)=>existsSync(candidate));
if(!browserPath)throw new Error('A CDP-compatible browser was not found');
const port=await availablePort(); const debugPort=await availablePort(); let browser; let client; let passed=false; const assertions=[];
try {
  await mkdir(outputRoot,{recursive:true}); await mkdir(profileDir,{recursive:true});
  await new Promise((resolveListen,rejectListen)=>{server.once('error',rejectListen);server.listen(port,'127.0.0.1',resolveListen);});
  browser=spawn(browserPath,['--headless=new','--disable-gpu','--disable-extensions','--no-sandbox','--no-first-run','--no-default-browser-check',`--remote-debugging-port=${debugPort}`,`--user-data-dir=${profileDir}`,'about:blank'],{stdio:'ignore'});
  for(let attempt=0;attempt<300;attempt+=1){try{if((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok)break;}catch{}if(attempt===299)throw new Error('browser debugger not ready');await pause(100);}
  const pages=await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response)=>response.json()); client=await cdp(pages.find((page)=>page.type==='page').webSocketDebuggerUrl); await client.send('Runtime.enable'); await client.send('Page.enable'); await client.send('Page.navigate',{url:`http://127.0.0.1:${port}/`});
  await wait(client,`globalThis.__WORK_ORDER_TASK_AUDIT__`,'Work Order task audit');
  const empty=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.list()`); assert(empty.status===200&&empty.body.workOrderVersion===1&&empty.body.tasks.length===0,'empty task list was invalid');
  const first=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.appendOne()`); assert(first.status===201&&first.body.workOrderVersion===2&&first.body.tasks[0].taskId===taskOneId,'first append failed');
  const replay=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.replayAppendOne()`); assert(replay.status===200&&replay.replayed==='true'&&replay.body.workOrderVersion===2,'append replay failed'); assertions.push('exact-snapshot-replay');
  const second=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.appendTwo()`); assert(second.status===201&&second.body.workOrderVersion===3&&second.body.tasks.length===2,'second append failed');
  const stale=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.staleStatus()`); assert(stale.status===409&&stale.body.code==='VERSION_CONFLICT','stale Work Order version was not rejected'); assertions.push('dual-version-conflict');
  const completedOne=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.completeOne()`); assert(completedOne.status===200&&completedOne.body.workOrderVersion===4&&completedOne.body.summary.completed===1&&completedOne.body.tasks[0].version===2,'first status failed');
  const crossAction=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.crossActionKey()`); assert(crossAction.status===409&&crossAction.body.code==='IDEMPOTENCY_CONFLICT','cross-action key reuse did not conflict'); assertions.push('unified-task-idempotency-domain');
  const duplicate=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.duplicateOrder()`); assert(duplicate.status===422&&duplicate.body.code==='WORK_ORDER_TASK_INVALID','duplicate order did not fail closed'); assertions.push('exact-full-permutation');
  const reordered=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.reorder()`); assert(reordered.status===200&&reordered.body.workOrderVersion===5&&reordered.body.tasks[0].taskId===taskTwoId&&reordered.body.tasks[0].position===0&&reordered.body.tasks[1].position===1,'exact reorder failed');
  const completedTwo=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.completeTwo()`); assert(completedTwo.status===200&&completedTwo.body.workOrderVersion===6&&completedTwo.body.summary.completed===2,'second status failed'); assertions.push('summary-convergence');
  const authDenied=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.denyAuthorization()`); assert(authDenied.status===403&&authDenied.body.code==='WORK_ORDER_ACCESS_DENIED','authorization denial did not fail closed'); assert(!(await evaluate(client,`document.body.innerText.includes(${JSON.stringify(taskOneId)})`)),'authorization denial retained task data'); assertions.push('authorization-denial-no-data');
  const denied=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.switchDenied()`); assert(denied.status===404&&denied.body.code==='ROUTE_NOT_FOUND','non-selected Session saw task route'); assertions.push('non-selected-route-absence');
  const crossSite=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.switchSite()`); assert(crossSite.status===404&&crossSite.body.code==='RESOURCE_NOT_FOUND','cross-Site task list did not fail generically'); assertions.push('cross-site-nondiscovery');
  const lost=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.loseSession()`); assert(lost.status===401&&lost.body.code==='SESSION_REQUIRED','Session loss did not fail closed'); assert(!(await evaluate(client,`document.body.innerText.includes(${JSON.stringify(taskTwoId)})`)),'Session loss retained task data'); assertions.push('session-loss-purge');
  const unreviewedDelete=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.unreviewedDelete()`); assert(unreviewedDelete.status===404&&unreviewedDelete.body.code==='ROUTE_NOT_FOUND','unreviewed task delete was exposed');
  const unreviewedTitleEdit=await evaluate(client,`globalThis.__WORK_ORDER_TASK_AUDIT__.unreviewedTitleEdit()`); assert(unreviewedTitleEdit.status===404&&unreviewedTitleEdit.body.code==='ROUTE_NOT_FOUND','unreviewed task title edit was exposed'); assertions.push('delete-and-title-edit-absence');
  const apiRequests=requests.filter((entry)=>entry.path.includes('/work-orders/')); assert(apiRequests.length>=15,'browser audit did not exercise task boundaries'); assert(apiRequests.every((entry)=>entry.path.startsWith('/api/v1/sites/')),'browser bypassed public Gateway paths'); assert(apiRequests.filter((entry)=>entry.method==='POST').every((entry)=>entry.headers['x-csrf-token']===browserProof&&typeof entry.headers['idempotency-key']==='string'),'browser omitted mutation proofs'); assert(apiRequests.every((entry)=>!Object.keys(entry.headers).some((name)=>forbiddenAuthorityHeaders.has(name))),'browser supplied authority headers'); assertions.push('public-gateway-task-only');
  passed=true;
  await writeFile(join(outputRoot,'browser-evidence.json'),JSON.stringify({schemaVersion:1,passed:true,generatedAt:new Date().toISOString(),assertions,cohort:{percentage:1,salt,group,revision,selectedSubject,selectedBucket:cohortBucket(selectedSubject),authorizationDeniedSubject,authorizationDeniedBucket:cohortBucket(authorizationDeniedSubject),deniedSubject,deniedBucket:cohortBucket(deniedSubject)},network:{requests},finalChecklist:checklist(),safety:{publicGatewayOnly:true,csrfOriginBound:true,unifiedIdempotencyDomain:true,dualVersionConcurrency:true,exactFullPermutation:true,deleteAbsent:true,titleEditAbsent:true,fallbackOwner:false,shadowOwner:false}},null,2));
  console.log(`S5 Work Order task browser audit passed. Evidence: ${join(outputRoot,'browser-evidence.json')}`);
} finally {
  client?.close(); await stop(browser); await new Promise((resolveClose)=>server.close(()=>resolveClose())); try{await rm(profileDir,{recursive:true,force:true,maxRetries:8,retryDelay:250});}catch{}
  if(!passed){await mkdir(outputRoot,{recursive:true});await writeFile(join(outputRoot,'browser-evidence.json'),JSON.stringify({schemaVersion:1,passed:false,generatedAt:new Date().toISOString(),assertions,network:{requests}},null,2));}
}

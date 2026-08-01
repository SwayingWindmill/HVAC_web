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
const outputRoot = resolve(root, 'out/s5-work-order-read-canary');
const profileDir = join(tmpdir(), `s5-work-order-browser-${process.pid}`);
const organizationId = '01910000-0000-7000-8000-000000000001';
const siteId = '01910000-0001-7000-8000-000000000001';
const otherSiteId = '01910000-0002-7000-8000-000000000001';
const workOrderId = '01910000-5000-7000-8000-000000000001';
const salt = 's5-work-order-read-canary-v1';
const group = 's5-work-order-read-v1';
const revision = 2;
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const assert = (condition, message) => { if (!condition) throw new Error(message); };

function cohortBucket(subject) {
  const digest = createHash('sha256').update(`${salt}\0${group}\0${revision}\0${organizationId}\0${subject}`).digest();
  return Number(digest.readBigUInt64BE(0) % 100n);
}
function findSubject(selected, label) {
  for (let index = 0; index < 100000; index += 1) {
    const subject = 'work-order-browser-' + label + '-' + index;
    if ((cohortBucket(subject) < 1) === selected) return subject;
  }
  throw new Error('unable to resolve deterministic Work Order cohort subject');
}
const selectedSubject = findSubject(true, 'selected');
const authorizationDeniedSubject = findSubject(true, 'authorization-denied');
const deniedSubject = findSubject(false, 'denied');
const requests = [];
function problem(response, status, code, detail) {
  response.writeHead(status, { 'content-type': 'application/problem+json', 'cache-control': 'private, no-store' });
  response.end(JSON.stringify({ type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_','-')}`, title: code, status, detail, code, retryable: false }));
}
function workOrder(site = siteId) {
  return {
    schemaVersion: 1, workOrderId, organizationId, siteId: site,
    title: 'Inspect authoritative AHU fan vibration', description: 'Server-owned Work Order read evidence.',
    priority: 'HIGH', status: 'OPEN', sourceReferences: [{ domain: 'ALARM', resourceId: '01910000-4000-7000-8000-000000000001', relationship: 'ORIGIN' }],
    tasks: { total: 0, completed: 0, blocked: 0 }, noteCount: 0, attachmentCount: 0, completionEvidence: [],
    timeline: [{ operation: 'CREATE', toStatus: 'OPEN', reason: 'WORK_ORDER_CREATED', actorType: 'WORKLOAD', actorId: 'work-order-service', occurredAt: '2026-08-01T00:00:00Z', version: 1 }],
    version: 1, createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-08-01T01:00:00Z',
  };
}
function cookieValue(request, key) {
  const match = String(request.headers.cookie ?? '').split(';').map((part) => part.trim()).find((part) => part.startsWith(`${key}=`));
  return match ? decodeURIComponent(match.slice(key.length + 1)) : '';
}
function html() {
  return `<!doctype html><meta charset="utf-8"><title>Work Order read canary audit</title>
  <main data-testid="work-order-audit" data-state="LOADING"><h1>Work Order read canary</h1><pre data-testid="result"></pre></main>
  <script>
  const siteId=${JSON.stringify(siteId)}, otherSiteId=${JSON.stringify(otherSiteId)}, workOrderId=${JSON.stringify(workOrderId)};
  const selected=${JSON.stringify(selectedSubject)}, authorizationDenied=${JSON.stringify(authorizationDeniedSubject)}, denied=${JSON.stringify(deniedSubject)};
  const main=document.querySelector('[data-testid="work-order-audit"]'), result=document.querySelector('[data-testid="result"]');
  const setSubject=(value)=>{ document.cookie='subject='+encodeURIComponent(value)+'; path=/; SameSite=Lax'; };
  const clear=()=>{ result.textContent=''; main.dataset.state='EMPTY'; };
  async function read(targetSite=siteId) {
    const list=await fetch('/api/v1/sites/'+targetSite+'/work-orders?status=OPEN&limit=25', { headers: { accept: 'application/json' } });
    const listBody=await list.json();
    if(!list.ok){ clear(); main.dataset.state='DENIED'; result.textContent=listBody.code; return { status:list.status, body:listBody }; }
    const detail=await fetch('/api/v1/sites/'+targetSite+'/work-orders/'+workOrderId, { headers: { accept: 'application/json' } });
    const detailBody=await detail.json();
    if(!detail.ok){ clear(); main.dataset.state='DENIED'; result.textContent=detailBody.code; return { status:detail.status, body:detailBody }; }
    main.dataset.state='READY'; result.textContent=detailBody.title; return { status:detail.status, body:detailBody, list:listBody };
  }
  globalThis.__WORK_ORDER_READ_AUDIT__={
    selected, authorizationDenied, denied,
    repeatSelected: async()=>{setSubject(selected);return read();},
    denyAuthorization: async()=>{setSubject(authorizationDenied);return read();},
    switchDenied: async()=>{setSubject(denied);return read();},
    switchSite: async()=>{setSubject(selected);return read(otherSiteId);},
    loseSession: async()=>{document.cookie='subject=; Max-Age=0; path=/';return read();},
    state:()=>main.dataset.state,
  };
  setSubject(selected); read();
  </script>`;
}
const server = createServer((request, response) => {
  const url = new URL(request.url ?? '/', 'http://fixture');
  if (url.pathname === '/' || url.pathname === '/favicon.ico') {
    if (url.pathname === '/favicon.ico') { response.writeHead(204); response.end(); return; }
    response.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' }); response.end(html()); return;
  }
  const subject = cookieValue(request, 'subject');
  const bucket = subject ? cohortBucket(subject) : null;
  requests.push({ method: request.method, path: url.pathname, query: url.search, subject, bucket, headers: Object.fromEntries(Object.entries(request.headers).filter(([name]) => name.startsWith('x-'))) });
  if (request.method !== 'GET') { problem(response, 405, 'METHOD_NOT_ALLOWED', 'Work Order public canary is GET-only.'); return; }
  if (Object.keys(request.headers).some((name) => ['x-work-order-read-context','x-organization-id','x-site-id','x-work-order-id','x-delegation-grant'].includes(name))) { problem(response, 400, 'FORGED_IDENTITY_HEADER', 'Browser authority headers are rejected.'); return; }
  if (!subject) { problem(response, 401, 'SESSION_REQUIRED', 'The authenticated Session is required.'); return; }
  if (bucket >= 1) { problem(response, 404, 'ROUTE_NOT_FOUND', 'The requested route is not available.'); return; }
  if (subject === authorizationDeniedSubject) { problem(response, 403, 'WORK_ORDER_ACCESS_DENIED', 'The Work Order resource is not authorized.'); return; }
  const match = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/work-orders(?:\/([^/]+))?$/);
  if (!match || match[1] !== siteId || (match[2] && match[2] !== workOrderId)) { problem(response, 404, 'RESOURCE_NOT_FOUND', 'The Work Order resource is not visible.'); return; }
  response.writeHead(200, { 'content-type': 'application/json', 'cache-control': 'private, no-store' });
  response.end(JSON.stringify(match[2] ? workOrder() : { schemaVersion: 1, items: [workOrder()], nextCursor: null, hasMore: false }));
});
async function availablePort() { const socket=createTCPServer(); socket.listen(0,'127.0.0.1'); await once(socket,'listening'); const address=socket.address(); await new Promise((resolveClose)=>socket.close(resolveClose)); return address.port; }
async function cdp(url) { const socket=new WebSocket(url), pending=new Map(); let id=0; await once(socket,'open'); socket.on('message',(raw)=>{const message=JSON.parse(String(raw)); if(!message.id)return; const item=pending.get(message.id); if(!item)return; pending.delete(message.id); message.error?item.reject(new Error(message.error.message)):item.resolve(message.result);}); return {send(method,params={}){const commandId=++id; socket.send(JSON.stringify({id:commandId,method,params})); return new Promise((resolveCommand,rejectCommand)=>pending.set(commandId,{resolve:resolveCommand,reject:rejectCommand}));},close(){socket.close();}}; }
async function evaluate(client, expression) { const response=await client.send('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true}); if(response.exceptionDetails)throw new Error(response.exceptionDetails.text); return response.result.value; }
async function wait(client, expression, label) { for(let attempt=0;attempt<300;attempt+=1){try{const value=await evaluate(client,expression);if(value)return value;}catch{} await pause(100);} throw new Error(`${label} did not become ready`); }
async function stop(child){if(!child||child.exitCode!==null)return;child.kill('SIGTERM');const done=await Promise.race([once(child,'exit').then(()=>true),pause(1500).then(()=>false)]);if(!done&&process.platform==='win32')spawnSync('taskkill',['/PID',String(child.pid),'/T','/F'],{stdio:'ignore'});else if(!done)child.kill('SIGKILL');}
const candidates=[process.env.BROWSER_BINARY,process.env['PROGRAMFILES(X86)']?join(process.env['PROGRAMFILES(X86)'],'Microsoft','Edge','Application','msedge.exe'):null,process.env.PROGRAMFILES?join(process.env.PROGRAMFILES,'Microsoft','Edge','Application','msedge.exe'):null,'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe','C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe','/usr/bin/google-chrome','/usr/bin/google-chrome-stable','/usr/bin/chromium-browser','/usr/bin/chromium'].filter(Boolean);
const browserPath=candidates.find((candidate)=>existsSync(candidate)); if(!browserPath)throw new Error('A CDP-compatible browser was not found');
const port=await availablePort(), debugPort=await availablePort(); let browser,client,passed=false; const assertions=[];
try{
  await mkdir(outputRoot,{recursive:true}); await mkdir(profileDir,{recursive:true}); await new Promise((resolveListen,rejectListen)=>{server.once('error',rejectListen);server.listen(port,'127.0.0.1',resolveListen);});
  browser=spawn(browserPath,['--headless=new','--disable-gpu','--disable-extensions','--no-sandbox','--no-first-run','--no-default-browser-check',`--remote-debugging-port=${debugPort}`,`--user-data-dir=${profileDir}`,'about:blank'],{stdio:'ignore'});
  for(let attempt=0;attempt<300;attempt+=1){try{if((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok)break;}catch{} if(attempt===299)throw new Error('browser debugger not ready');await pause(100);}
  const pages=await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response)=>response.json()); client=await cdp(pages.find((page)=>page.type==='page').webSocketDebuggerUrl); await client.send('Runtime.enable');await client.send('Page.enable');await client.send('Page.navigate',{url:`http://127.0.0.1:${port}/`});
  await wait(client,`document.querySelector('[data-testid="work-order-audit"]')?.dataset.state==='READY'`,'selected Work Order read');
  const selected=await evaluate(client,`({state:globalThis.__WORK_ORDER_READ_AUDIT__.state(),text:document.body.innerText})`); assert(selected.state==='READY'&&selected.text.includes('Inspect authoritative AHU fan vibration'),'selected cohort did not receive Work Order authority'); assertions.push('selected-session-authoritative-list-detail');
  const repeated=await evaluate(client,`globalThis.__WORK_ORDER_READ_AUDIT__.repeatSelected()`); assert(repeated.status===200&&repeated.body.workOrderId===workOrderId,'selected cohort was not stable'); assertions.push('stable-session-cohort');
  const authorizationDenied=await evaluate(client,`globalThis.__WORK_ORDER_READ_AUDIT__.denyAuthorization()`); assert(authorizationDenied.status===403&&authorizationDenied.body.code==='WORK_ORDER_ACCESS_DENIED','authorization denial did not fail closed'); await wait(client,`globalThis.__WORK_ORDER_READ_AUDIT__.state()==='DENIED'`,'authorization denied state'); assert(!(await evaluate(client,`document.body.innerText.includes('Inspect authoritative AHU fan vibration')`)),'authorization denial retained protected Work Order data'); assertions.push('authorization-denial-no-data');
  const denied=await evaluate(client,`globalThis.__WORK_ORDER_READ_AUDIT__.switchDenied()`); assert(denied.status===404&&denied.body.code==='ROUTE_NOT_FOUND','non-selected session did not receive route absence'); await wait(client,`globalThis.__WORK_ORDER_READ_AUDIT__.state()==='DENIED'`,'denied state'); assert(!(await evaluate(client,`document.body.innerText.includes('Inspect authoritative AHU fan vibration')`)),'denied session retained protected Work Order data'); assertions.push('non-selected-session-route-absence');
  const crossSite=await evaluate(client,`globalThis.__WORK_ORDER_READ_AUDIT__.switchSite()`); assert(crossSite.status===404&&crossSite.body.code==='RESOURCE_NOT_FOUND','cross-Site request did not fail generically'); assertions.push('cross-site-nondiscovery');
  const lost=await evaluate(client,`globalThis.__WORK_ORDER_READ_AUDIT__.loseSession()`); assert(lost.status===401&&lost.body.code==='SESSION_REQUIRED','Session loss did not fail closed'); assert(!(await evaluate(client,`document.body.innerText.includes('Inspect authoritative AHU fan vibration')`)),'Session loss retained protected data'); assertions.push('session-loss-purge');
  const apiRequests=requests.filter((entry)=>entry.path.includes('/work-orders')); assert(apiRequests.length>=8,'browser audit did not exercise Work Order reads'); assert(apiRequests.every((entry)=>entry.method==='GET'),'browser issued a Work Order write'); assert(apiRequests.every((entry)=>entry.path.startsWith('/api/v1/sites/')),'browser bypassed public Gateway path'); assert(apiRequests.every((entry)=>Object.keys(entry.headers).length===0),'browser supplied authority headers'); assertions.push('public-gateway-get-only');
  passed=true; await writeFile(join(outputRoot,'browser-evidence.json'),JSON.stringify({schemaVersion:1,passed:true,generatedAt:new Date().toISOString(),assertions,cohort:{percentage:1,salt,group,revision,selectedSubject,selectedBucket:cohortBucket(selectedSubject),authorizationDeniedSubject,authorizationDeniedBucket:cohortBucket(authorizationDeniedSubject),deniedSubject,deniedBucket:cohortBucket(deniedSubject)},network:{requests},safety:{publicReadsOnly:true,lifecycleWrites:false,fallbackOwner:false,shadowOwner:false}},null,2));
  console.log(`S5 Work Order read canary browser audit passed. Evidence: ${join(outputRoot,'browser-evidence.json')}`);
}finally{client?.close();await stop(browser);await new Promise((resolveClose)=>server.close(()=>resolveClose()));try{await rm(profileDir,{recursive:true,force:true,maxRetries:8,retryDelay:250});}catch{}if(!passed){await mkdir(outputRoot,{recursive:true});await writeFile(join(outputRoot,'browser-evidence.json'),JSON.stringify({schemaVersion:1,passed:false,generatedAt:new Date().toISOString(),assertions,network:{requests}},null,2));}}

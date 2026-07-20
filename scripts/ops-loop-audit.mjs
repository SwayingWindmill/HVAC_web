import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import WebSocket from 'ws';

const baseUrl = process.env.HVAC_AUDIT_BASE_URL ?? 'http://localhost:5173';
const debugPort = 9341;
const profileDir = join(tmpdir(), `hvac-ops-loop-${process.pid}`);
const edgeCandidates = [
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  join('C:\\Program Files (x86)', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
  join('C:\\Program Files', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
].filter(Boolean);
const edgePath = edgeCandidates.find((candidate) => existsSync(candidate));
if (!edgePath) throw new Error('Microsoft Edge executable not found');
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

await mkdir(profileDir, { recursive: true });

const edge = spawn(edgePath, [
  '--headless=new',
  '--disable-gpu',
  '--no-first-run',
  '--no-default-browser-check',
  `--remote-debugging-port=${debugPort}`,
  `--user-data-dir=${profileDir}`,
  `${baseUrl}/dashboard`,
], { stdio: 'ignore' });

async function waitForDebugger() {
  for (let index = 0; index < 100; index += 1) {
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json/version`);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error('Edge debugger unavailable');
}

function createClient(webSocketUrl) {
  const socket = new WebSocket(webSocketUrl);
  const pending = new Map();
  let nextId = 0;
  const ready = new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', reject, { once: true });
  });

  socket.addEventListener('message', ({ data }) => {
    const message = JSON.parse(String(data));
    const job = pending.get(message.id);
    if (!job) return;
    pending.delete(message.id);
    if (message.error) job.reject(new Error(message.error.message));
    else job.resolve(message.result);
  });

  return {
    async send(method, params = {}) {
      await ready;
      const id = ++nextId;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        socket.send(JSON.stringify({ id, method, params }));
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
    throw new Error(response.exceptionDetails.exception?.description ?? 'Browser evaluation failed');
  }
  return response.result.value;
}

async function waitFor(client, expression, label, attempts = 180) {
  for (let index = 0; index < attempts; index += 1) {
    try {
      if (await evaluate(client, expression)) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`Timeout: ${label}`);
}

async function spaNavigate(client, path) {
  await evaluate(client, `(() => {
    history.pushState({}, '', ${JSON.stringify(path)});
    window.dispatchEvent(new PopStateEvent('popstate'));
    return true;
  })()`);
  const pathname = path.split('?')[0];
  await waitFor(client, `location.pathname === ${JSON.stringify(pathname)}`, `navigate ${path}`);
}

async function hardNavigate(client, path) {
  await client.send('Page.navigate', { url: `${baseUrl}${path}` });
  const pathname = path.split('?')[0];
  await waitFor(
    client,
    `document.readyState === 'complete' && location.pathname === ${JSON.stringify(pathname)}`,
    `hard navigate ${path}`,
  );
}

async function clickText(client, text, scope = 'document') {
  const expression = `(() => {
    const root = ${scope === 'document' ? 'document' : `document.querySelector(${JSON.stringify(scope)})`};
    if (!root) return false;
    const candidates = [...root.querySelectorAll('button, [role="button"], .ant-segmented-item')];
    const expected = ${JSON.stringify(text.replace(/\s+/g, ''))};
    const target = candidates.find((element) => (
      element.offsetParent !== null && element.textContent.replace(/\\s+/g, '') === expected
    ));
    if (!target) return false;
    target.click();
    return true;
  })()`;

  for (let index = 0; index < 80; index += 1) {
    if (await evaluate(client, expression)) return;
    await pause(100);
  }
  const visibleActions = await evaluate(client, `(() => {
    const root = ${scope === 'document' ? 'document' : `document.querySelector(${JSON.stringify(scope)})`};
    if (!root) return [];
    return [...root.querySelectorAll('button, [role="button"], .ant-segmented-item')]
      .filter((element) => element.offsetParent !== null)
      .map((element) => element.textContent.trim())
      .filter(Boolean);
  })()`);
  throw new Error(`Visible action not found: ${text}; available: ${JSON.stringify(visibleActions)}`);
}

async function ensureRoleControl(client) {
  await waitFor(
    client,
    `(() => {
      const visibleSegmented = [...document.querySelectorAll('.ant-segmented')].some((element) => element.offsetParent !== null);
      const viewButton = [...document.querySelectorAll('button')].some((element) => (
        element.offsetParent !== null && element.textContent.replace(/\\s+/g, '') === '视图配置'
      ));
      return visibleSegmented || viewButton;
    })()`,
    'role control entry',
  );

  const visibleSegmented = await evaluate(
    client,
    `[...document.querySelectorAll('.ant-segmented')].some((element) => element.offsetParent !== null)`,
  );
  if (!visibleSegmented) {
    await clickText(client, '视图配置');
    await waitFor(
      client,
      `[...document.querySelectorAll('.ant-segmented')].some((element) => element.offsetParent !== null)`,
      'expanded role control',
    );
  }
}

async function selectRole(client, label) {
  await ensureRoleControl(client);
  await clickText(client, label);
  const expected = label.replace(/\s+/g, '');
  await waitFor(
    client,
    `[...document.querySelectorAll('.ant-segmented-item-selected')].some((item) => item.textContent.replace(/\\s+/g, '') === ${JSON.stringify(expected)})`,
    `role ${label}`,
  );
}

async function waitForDrawer(client, token) {
  await waitFor(
    client,
    `(() => {
      const drawer = document.querySelector('.ops-detail-drawer.ant-drawer-open');
      return Boolean(drawer && drawer.textContent.includes(${JSON.stringify(token)}));
    })()`,
    `drawer ${token}`,
  );
}

async function waitForDrawerStatus(client, status) {
  await waitFor(
    client,
    `document.querySelector('.ops-detail-drawer.ant-drawer-open .ops-detail-status')?.textContent.includes(${JSON.stringify(status)})`,
    `drawer status ${status}`,
  );
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

try {
  await waitForDebugger();
  const targets = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`)).json();
  const target = targets.find((item) => item.type === 'page');
  if (!target) throw new Error('No browser page target');

  const client = createClient(target.webSocketDebuggerUrl);
  const checks = [];
  try {
    await client.send('Page.enable');
    await client.send('Runtime.enable');
    await client.send('Emulation.setDeviceMetricsOverride', {
      width: 1440,
      height: 900,
      deviceScaleFactor: 1,
      mobile: false,
    });
    await waitFor(client, `document.readyState === 'complete' && location.pathname === '/dashboard'`, 'initial app');

    await spaNavigate(client, '/fdd?diagnosis=FDD-77');
    await waitForDrawer(client, 'FDD-77');
    checks.push('FDD deep link opens exact diagnosis');

    await clickText(client, '查看资产', '.ops-detail-drawer.ant-drawer-open');
    await waitFor(client, `location.pathname === '/assets' && new URLSearchParams(location.search).get('device') === 'b1-z1-u1'`, 'asset deep link');
    await waitForDrawer(client, 'b1-z1-u1');
    checks.push('FDD opens exact linked asset');

    await spaNavigate(client, '/fdd?diagnosis=FDD-77');
    await waitForDrawer(client, 'FDD-77');
    await clickText(client, '查看优化建议', '.ops-detail-drawer.ant-drawer-open');
    await waitFor(client, `location.pathname === '/optimize' && new URLSearchParams(location.search).get('suggestion') === 'OPT-201'`, 'optimization deep link');
    await waitForDrawer(client, 'OPT-201');
    checks.push('FDD opens exact linked optimization');

    await waitForDrawerStatus(client, '草稿');
    await clickText(client, '提交审批', '.ops-detail-drawer.ant-drawer-open');
    await waitForDrawerStatus(client, '待审批');
    await clickText(client, '批准', '.ops-detail-drawer.ant-drawer-open');
    await waitForDrawerStatus(client, '已批准');
    await clickText(client, '模拟下发', '.ops-detail-drawer.ant-drawer-open');
    await waitForDrawerStatus(client, '已下发');
    checks.push('Optimization submit, approve and dispatch stay synchronized');

    await spaNavigate(client, '/fdd?diagnosis=FDD-75');
    await waitForDrawer(client, 'FDD-75');
    await clickText(client, '生成工单', '.ops-detail-drawer.ant-drawer-open');
    await waitFor(client, `location.pathname === '/alarms' && Boolean(new URLSearchParams(location.search).get('workOrder'))`, 'generated work order route');
    const generatedWorkOrderId = await evaluate(client, `new URLSearchParams(location.search).get('workOrder')`);
    assert(/^WO-\d+$/.test(generatedWorkOrderId), `Invalid generated work order id: ${generatedWorkOrderId}`);
    await waitForDrawer(client, generatedWorkOrderId);
    const generatedDrawerText = await evaluate(client, `document.querySelector('.ops-detail-drawer.ant-drawer-open')?.textContent ?? ''`);
    assert(generatedDrawerText.includes('FDD-75'), 'Generated work order lost source diagnosis');
    assert(generatedDrawerText.includes('b1-z1-p3'), 'Generated work order lost linked asset');
    checks.push('FDD creates and opens the exact enriched work order');

    await waitForDrawerStatus(client, '待接手');
    await clickText(client, '接手', '.ops-detail-drawer.ant-drawer-open');
    await waitForDrawerStatus(client, '已派工');
    const assignedText = await evaluate(client, `document.querySelector('.ops-detail-drawer.ant-drawer-open')?.textContent ?? ''`);
    assert(assignedText.includes('运维值班组'), 'Assigned work order did not receive default assignee');
    await clickText(client, '开始处理', '.ops-detail-drawer.ant-drawer-open');
    await waitForDrawerStatus(client, '处理中');
    await clickText(client, '完成闭环', '.ops-detail-drawer.ant-drawer-open');
    await waitForDrawerStatus(client, '已完成');
    checks.push('Work order transitions from open to closed with synchronized detail state');

    await clickText(client, '查看诊断', '.ops-detail-drawer.ant-drawer-open');
    await waitFor(client, `location.pathname === '/fdd' && new URLSearchParams(location.search).get('diagnosis') === 'FDD-75'`, 'return to diagnosis');
    await waitForDrawer(client, 'FDD-75');
    const closedDiagnosisText = await evaluate(client, `document.querySelector('.ops-detail-drawer.ant-drawer-open')?.textContent ?? ''`);
    assert(closedDiagnosisText.includes('已完成'), 'FDD did not reflect the closed work order status');
    assert(closedDiagnosisText.includes(generatedWorkOrderId), 'FDD did not retain the generated work order id');
    checks.push('Closed work order state propagates back to FDD');

    await hardNavigate(client, '/dashboard');
    await selectRole(client, '安装/运维');
    await spaNavigate(client, '/fdd?diagnosis=FDD-71');
    await waitForDrawer(client, 'FDD-71');
    const opsCanGenerate = await evaluate(client, `(() => {
      const drawer = document.querySelector('.ops-detail-drawer.ant-drawer-open');
      const button = [...drawer.querySelectorAll('button')].find((item) => item.textContent.trim() === '生成工单');
      return Boolean(button && !button.disabled);
    })()`);
    assert(opsCanGenerate, 'Operations role should be able to generate work orders');
    await spaNavigate(client, '/optimize');
    await waitFor(client, `document.querySelector('.ant-result-title')?.textContent.trim() === '403'`, 'ops optimization denial');
    const opsDeniedText = await evaluate(client, `document.body.textContent`);
    assert(opsDeniedText.includes('安装/运维'), 'Operations access denial did not identify the role');
    checks.push('Operations role can close work orders but cannot access optimization');

    await spaNavigate(client, '/dashboard');
    await selectRole(client, '演示/汇报');
    await spaNavigate(client, '/alarms');
    await waitFor(client, `document.querySelector('.ant-result-title')?.textContent.trim() === '403'`, 'demo alarms denial');
    checks.push('Demo role remains restricted to presentation surfaces');

    await spaNavigate(client, '/dashboard');
    await selectRole(client, '内部研发');

    console.log(JSON.stringify({ passed: checks.length, checks }, null, 2));
  } finally {
    client.close();
  }
} finally {
  edge.kill();
  await pause(700);
  await rm(profileDir, { recursive: true, force: true });
}

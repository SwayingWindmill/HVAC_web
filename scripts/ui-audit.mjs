import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const baseUrl = process.env.HVAC_AUDIT_BASE_URL ?? 'http://localhost:5173';
const debugPort = Number(process.env.HVAC_UI_AUDIT_DEBUG_PORT ?? 9342);
const profileDir = join(tmpdir(), `hvac-ui-audit-${process.pid}`);
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const edgeCandidates = [
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  join('C:\\Program Files (x86)', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
  join('C:\\Program Files', 'Microsoft', 'Edge', 'Application', 'msedge.exe'),
].filter(Boolean);
const edgePath = edgeCandidates.find((candidate) => existsSync(candidate));
if (!edgePath) throw new Error('Microsoft Edge executable not found');

const ROUTES = [
  { path: '/dashboard', title: '智慧能源运营总览', roles: ['demo', 'ops', 'rd'] },
  { path: '/assets', title: '设备与建筑', roles: ['ops', 'rd'] },
  { path: '/energy/year', title: '年度能耗分析', roles: ['rd'] },
  { path: '/energy/month', title: '月度能耗分析', roles: ['rd'] },
  { path: '/energy/week', title: '周度能耗分析', roles: ['rd'] },
  { path: '/energy/day', title: '日度能耗分析', roles: ['rd'] },
  { path: '/cost', title: '成本与绩效', roles: ['rd'] },
  { path: '/fdd', title: '故障检测与诊断 FDD', roles: ['ops', 'rd'] },
  { path: '/alarms', title: '报警工单', roles: ['ops', 'rd'] },
  { path: '/optimize', title: '节能优化建议', roles: ['rd'] },
  { path: '/ai', title: 'HVAC AI 运维助手', roles: ['rd'] },
  { path: '/system', title: '系统管理', roles: ['rd'] },
  { path: '/bigscreen', title: '商业建筑智慧能源驾驶舱', roles: ['demo', 'ops', 'rd'], bigscreen: true },
];

const ROLE_LABELS = {
  demo: '演示/汇报',
  ops: '安装/运维',
  rd: '内部研发',
};

const VIEWPORTS = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 1024, height: 768 },
  { name: 'mobile', width: 390, height: 844 },
];

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

await mkdir(profileDir, { recursive: true });
const edge = spawn(edgePath, [
  '--headless=new',
  '--disable-gpu',
  '--no-first-run',
  '--no-default-browser-check',
  '--hide-scrollbars',
  `--remote-debugging-port=${debugPort}`,
  `--user-data-dir=${profileDir}`,
  `${baseUrl}/dashboard`,
], { stdio: 'ignore' });

async function waitForDebugger() {
  for (let index = 0; index < 120; index += 1) {
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
  const listeners = new Map();
  let nextId = 0;
  const ready = new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', reject, { once: true });
  });

  socket.addEventListener('message', ({ data }) => {
    const message = JSON.parse(String(data));
    if (message.id) {
      const job = pending.get(message.id);
      if (!job) return;
      pending.delete(message.id);
      if (message.error) job.reject(new Error(message.error.message));
      else job.resolve(message.result);
      return;
    }
    for (const listener of listeners.get(message.method) ?? []) listener(message.params ?? {});
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
    on(method, listener) {
      const current = listeners.get(method) ?? [];
      current.push(listener);
      listeners.set(method, current);
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

async function waitFor(client, expression, label, attempts = 220) {
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
  const expected = text.replace(/\s+/g, '');
  const expression = `(() => {
    const root = ${scope === 'document' ? 'document' : `document.querySelector(${JSON.stringify(scope)})`};
    if (!root) return false;
    const candidates = [...root.querySelectorAll('button, a, [role="button"], .ant-segmented-item, .ant-tabs-tab')];
    const target = candidates.find((element) => (
      element.offsetParent !== null && element.textContent.replace(/\\s+/g, '') === ${JSON.stringify(expected)}
    ));
    if (!target) return false;
    target.click();
    return true;
  })()`;

  for (let index = 0; index < 100; index += 1) {
    if (await evaluate(client, expression)) return;
    await pause(100);
  }
  throw new Error(`Visible action not found: ${text}`);
}

async function ensureRoleControl(client) {
  await waitFor(
    client,
    `(() => {
      const segmented = [...document.querySelectorAll('.ant-segmented')].some((element) => element.offsetParent !== null);
      const viewButton = [...document.querySelectorAll('button')].some((element) => (
        element.offsetParent !== null && element.textContent.replace(/\\s+/g, '') === '视图配置'
      ));
      return segmented || viewButton;
    })()`,
    'role control entry',
  );
  const visible = await evaluate(client, `[...document.querySelectorAll('.ant-segmented')].some((element) => element.offsetParent !== null)`);
  if (!visible) {
    await clickText(client, '视图配置');
    await waitFor(client, `[...document.querySelectorAll('.ant-segmented')].some((element) => element.offsetParent !== null)`, 'expanded role control');
  }
}

async function selectRole(client, role) {
  await ensureRoleControl(client);
  await clickText(client, ROLE_LABELS[role]);
  const expected = ROLE_LABELS[role].replace(/\s+/g, '');
  await waitFor(
    client,
    `[...document.querySelectorAll('.ant-segmented-item-selected')].some((item) => item.textContent.replace(/\\s+/g, '') === ${JSON.stringify(expected)})`,
    `role ${role}`,
  );
}

async function waitForRoute(client, route, allowed) {
  if (!allowed) {
    await waitFor(client, `document.querySelector('.ant-result-title')?.textContent.trim() === '403'`, `403 ${route.path}`);
    return;
  }
  await waitFor(
    client,
    `(() => {
      const text = document.body.innerText;
      const spinning = [...document.querySelectorAll('.ant-spin-spinning')].some((element) => element.offsetParent !== null);
      return text.includes(${JSON.stringify(route.title)}) && !spinning;
    })()`,
    `route ${route.path}`,
  );
}

async function setViewport(client, viewport) {
  await client.send('Emulation.setDeviceMetricsOverride', {
    width: viewport.width,
    height: viewport.height,
    deviceScaleFactor: 1,
    mobile: false,
  });
  await waitFor(client, `window.innerWidth === ${viewport.width}`, `viewport ${viewport.name}`);
}

async function setTheme(client, mode) {
  await client.send('Emulation.setEmulatedMedia', {
    media: 'screen',
    features: [{ name: 'prefers-color-scheme', value: mode }],
  });
  await waitFor(client, `document.documentElement.dataset.theme === ${JSON.stringify(mode)}`, `theme ${mode}`);
}

async function inspectPage(client, route, viewport, theme) {
  const result = await evaluate(client, `(() => {
    const root = document.documentElement;
    const body = document.body;
    const content = document.querySelector('.ant-layout-content') ?? body;
    const visibleText = body.innerText.replace(/\\s+/g, ' ').trim();
    const visibleSpinners = [...document.querySelectorAll('.ant-spin-spinning')]
      .filter((element) => element.offsetParent !== null).length;
    const rootOverflow = root.scrollWidth > root.clientWidth + 2 || body.scrollWidth > root.clientWidth + 2;
    const contentRect = content.getBoundingClientRect();
    const escaped = [...document.querySelectorAll('h1, h2, h3, .ops-page, .dashboard-page, .ant-result, .ant-card')]
      .filter((element) => element.offsetParent !== null)
      .filter((element) => {
        const rect = element.getBoundingClientRect();
        const insideScroller = element.closest('.ant-table-content, .ant-table-body, .ant-tabs-nav-wrap');
        return !insideScroller && (rect.left < -3 || rect.right > window.innerWidth + 3);
      })
      .slice(0, 5)
      .map((element) => ({
        tag: element.tagName,
        className: String(element.className).slice(0, 120),
        text: element.textContent.trim().slice(0, 80),
        rect: element.getBoundingClientRect().toJSON(),
      }));
    return {
      pathname: location.pathname,
      titleFound: visibleText.includes(${JSON.stringify(route.title)}),
      textLength: visibleText.length,
      visibleSpinners,
      rootOverflow,
      escaped,
      contentWidth: contentRect.width,
      viewportWidth: window.innerWidth,
      firstChartTop: document.querySelector('.ops-chart-card')?.getBoundingClientRect().top ?? null,
      datasetTheme: root.dataset.theme,
    };
  })()`);

  const minimumTextLength = route.path === '/404' ? 12 : 40;
  assert(result.titleFound, `${route.path} missing title at ${viewport.name}/${theme}`);
  assert(result.textLength >= minimumTextLength, `${route.path} appears blank at ${viewport.name}/${theme}`);
  assert(result.visibleSpinners === 0, `${route.path} still loading at ${viewport.name}/${theme}`);
  assert(result.contentWidth > 0, `${route.path} has zero-width content at ${viewport.name}/${theme}`);
  assert(!result.rootOverflow, `${route.path} root overflow at ${viewport.name}/${theme}`);
  assert(result.escaped.length === 0, `${route.path} escaped viewport at ${viewport.name}/${theme}: ${JSON.stringify(result.escaped)}`);
  if (route.path.startsWith('/energy/')) {
    const maximumFirstChartTop = viewport.name === 'mobile' ? 1000 : viewport.name === 'tablet' ? 820 : 650;
    assert(
      typeof result.firstChartTop === 'number' && result.firstChartTop <= maximumFirstChartTop,
      `${route.path} pushes primary evidence below the visual-density limit at ${viewport.name}/${theme}: ${result.firstChartTop}px > ${maximumFirstChartTop}px`,
    );
  }
  assert(result.datasetTheme === theme, `${route.path} theme mismatch: expected ${theme}, got ${result.datasetTheme}`);
  return result;
}

async function pressEscape(client) {
  await client.send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await client.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
}

try {
  await waitForDebugger();
  const targets = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`)).json();
  const target = targets.find((item) => item.type === 'page');
  if (!target) throw new Error('No browser page target');

  const client = createClient(target.webSocketDebuggerUrl);
  const accessChecks = [];
  const visualChecks = [];
  const interactionChecks = [];
  const browserProblems = [];
  let context = 'startup';

  const addProblem = (type, detail) => browserProblems.push({ context, type, detail });
  client.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
    addProblem('exception', exceptionDetails.exception?.description ?? exceptionDetails.text ?? 'unknown exception');
  });
  client.on('Runtime.consoleAPICalled', ({ type, args }) => {
    if (!['error', 'assert'].includes(type)) return;
    addProblem('console', args.map((item) => item.value ?? item.description ?? '').join(' '));
  });
  client.on('Log.entryAdded', ({ entry }) => {
    if (entry.level === 'error') addProblem('log', `${entry.text}${entry.url ? ` · ${entry.url}` : ''}`);
  });
  client.on('Network.responseReceived', ({ response }) => {
    if (response.status < 400) return;
    if (!response.url.startsWith(baseUrl)) return;
    addProblem('http', `${response.status} ${response.url}`);
  });
  client.on('Network.loadingFailed', ({ errorText, canceled, type }) => {
    if (canceled || errorText.includes('ERR_ABORTED') || type === 'WebSocket') return;
    addProblem('network', `${type}: ${errorText}`);
  });

  try {
    await client.send('Page.enable');
    await client.send('Runtime.enable');
    await client.send('Log.enable');
    await client.send('Network.enable');
    await setViewport(client, VIEWPORTS[0]);
    await waitFor(client, `document.readyState === 'complete' && location.pathname === '/dashboard'`, 'initial app');

    // Role × route access matrix.
    for (const role of ['rd', 'ops', 'demo']) {
      context = `access:${role}`;
      await spaNavigate(client, '/dashboard');
      await waitForRoute(client, ROUTES[0], true);
      await selectRole(client, role);
      for (const route of ROUTES) {
        context = `access:${role}:${route.path}`;
        await spaNavigate(client, route.path);
        const allowed = route.roles.includes(role);
        await waitForRoute(client, route, allowed);
        accessChecks.push(`${role}:${route.path}:${allowed ? 'allowed' : '403'}`);
      }
      context = `access:${role}:404`;
      await spaNavigate(client, '/release-audit-missing-route');
      await waitFor(client, `document.querySelector('.ant-result-title')?.textContent.trim() === '404'`, `404 ${role}`);
      accessChecks.push(`${role}:404`);
    }

    // Visual matrix for every real route using the fully authorized role.
    await spaNavigate(client, '/dashboard');
    await waitForRoute(client, ROUTES[0], true);
    await selectRole(client, 'rd');
    for (const theme of ['light', 'dark']) {
      await setTheme(client, theme);
      for (const viewport of VIEWPORTS) {
        await setViewport(client, viewport);
        for (const route of ROUTES) {
          context = `visual:${theme}:${viewport.name}:${route.path}`;
          await spaNavigate(client, route.path);
          await waitForRoute(client, route, true);
          await pause(route.bigscreen ? 450 : 180);
          await inspectPage(client, route, viewport, theme);
          visualChecks.push(`${theme}:${viewport.name}:${route.path}`);
        }
        context = `visual:${theme}:${viewport.name}:404`;
        await spaNavigate(client, '/release-audit-missing-route');
        await waitFor(client, `document.querySelector('.ant-result-title')?.textContent.trim() === '404'`, 'visual 404');
        const notFound = { path: '/404', title: '页面不存在。' };
        await inspectPage(client, notFound, viewport, theme);
        visualChecks.push(`${theme}:${viewport.name}:404`);
      }
    }

    // Interaction smoke tests.
    await setViewport(client, VIEWPORTS[0]);
    await setTheme(client, 'light');

    context = 'interaction:ai-floating-panel';
    await spaNavigate(client, '/dashboard');
    await waitForRoute(client, ROUTES[0], true);
    const aiLauncherOpened = await evaluate(client, `(() => {
      const trigger = document.querySelector('[aria-label="打开 AI 运维助手"]');
      if (!trigger) return false;
      trigger.click();
      return true;
    })()`);
    assert(aiLauncherOpened, 'AI floating panel launcher was not found');
    await waitFor(client, `Boolean(document.querySelector('.global-ai-floating-root.ant-drawer-open .ant-drawer-content'))`, 'AI floating panel open');
    const aiPanelState = await evaluate(client, `(() => {
      const root = document.querySelector('.global-ai-floating-root.ant-drawer-open');
      const wrapper = root?.querySelector('.ant-drawer-content-wrapper');
      const content = root?.querySelector('.ant-drawer-content');
      const body = root?.querySelector('.ant-drawer-body');
      const rect = wrapper?.getBoundingClientRect();
      const isOpaque = (element) => {
        if (!element) return false;
        const color = getComputedStyle(element).backgroundColor;
        const match = color.match(/^rgba?\\(([^)]+)\\)$/);
        if (!match) return color !== 'transparent';
        const parts = match[1].split(',').map((part) => part.trim());
        return parts.length < 4 || Number(parts[3]) >= 0.999;
      };
      return {
        hasMask: Boolean(root?.querySelector('.ant-drawer-mask')),
        pointerEvents: root ? getComputedStyle(root).pointerEvents : null,
        contentOpaque: isOpaque(content),
        bodyOpaque: isOpaque(body),
        contentBackground: content ? getComputedStyle(content).backgroundColor : null,
        bodyBackground: body ? getComputedStyle(body).backgroundColor : null,
        panelOverflow: Boolean(content && content.scrollWidth > content.clientWidth + 1),
        duplicatePromptStrip: root?.querySelectorAll('.assistant-prompt-strip').length ?? 0,
        homeVisible: Boolean(root?.querySelector('.global-ai-welcome')),
        skillCount: root?.querySelectorAll('.global-ai-skill-card').length ?? 0,
        containerOverflow: [
          '.ant-drawer-content',
          '.ant-drawer-body',
          '.global-ai-main-view',
          '.assistant-conversation',
          '.assistant-conversation-messages',
          '.global-ai-home',
          '.global-ai-skill-list',
          '.assistant-composer',
        ].some((selector) => {
          const element = root?.querySelector(selector);
          return Boolean(element && element.scrollWidth > element.clientWidth + 1);
        }),
        width: rect?.width ?? 0,
        top: rect?.top ?? -1,
        rightGap: rect ? window.innerWidth - rect.right : -1,
        bottomGap: rect ? window.innerHeight - rect.bottom : -1,
      };
    })()`);
    assert(!aiPanelState.hasMask, 'AI floating panel unexpectedly renders a mask');
    assert(aiPanelState.pointerEvents === 'none', 'AI floating panel root blocks underlying page interaction');
    assert(aiPanelState.contentOpaque && aiPanelState.bodyOpaque, `AI floating panel surface is transparent: ${JSON.stringify(aiPanelState)}`);
    assert(!aiPanelState.panelOverflow && !aiPanelState.containerOverflow, 'AI floating panel has horizontal overflow');
    assert(aiPanelState.duplicatePromptStrip === 0, 'AI idle home renders a duplicate prompt strip');
    assert(aiPanelState.homeVisible && aiPanelState.skillCount === 5, 'AI skill home is incomplete');
    assert(aiPanelState.width >= 560 && aiPanelState.width <= 640, 'AI standard panel width is invalid');
    await waitFor(client, `(() => { const rect = document.querySelector('.global-ai-floating-root.ant-drawer-open .ant-drawer-content-wrapper')?.getBoundingClientRect(); return Boolean(rect && rect.top >= 60 && window.innerWidth - rect.right >= 12 && window.innerHeight - rect.bottom >= 12); })()`, 'AI panel floating margins');
    await setTheme(client, 'dark');
    const aiDarkSurface = await evaluate(client, `(() => {
      const root = document.querySelector('.global-ai-floating-root.ant-drawer-open');
      const content = root?.querySelector('.ant-drawer-content');
      const body = root?.querySelector('.ant-drawer-body');
      return {
        content: content ? getComputedStyle(content).backgroundColor : '',
        body: body ? getComputedStyle(body).backgroundColor : '',
        overflow: Boolean(content && content.scrollWidth > content.clientWidth + 1),
      };
    })()`);
    assert(aiDarkSurface.content !== 'rgba(0, 0, 0, 0)' && aiDarkSurface.body !== 'rgba(0, 0, 0, 0)', 'AI dark-theme panel surface is transparent');
    assert(!aiDarkSurface.overflow, 'AI dark-theme panel has horizontal overflow');
    await setTheme(client, 'light');
    interactionChecks.push('ai-floating-panel');

    context = 'interaction:ai-context-routing';
    const energyNavClicked = await evaluate(client, `(() => {
      const item = [...document.querySelectorAll('.ant-menu-item')].find((element) => element.offsetParent !== null && element.textContent.includes('能耗分析'));
      if (!item) return false;
      item.click();
      return true;
    })()`);
    assert(energyNavClicked, 'Energy navigation item was not found while AI panel was open');
    await waitFor(client, `location.pathname.startsWith('/energy') && document.querySelector('.global-ai-floating-root.ant-drawer-open')?.textContent.includes('能耗分析')`, 'AI panel route context update');
    interactionChecks.push('ai-context-routing');

    context = 'interaction:ai-agent-workflow';
    const aiSkillStarted = await evaluate(client, `(() => {
      const skill = [...document.querySelectorAll('.global-ai-skill-card')].find((element) => element.offsetParent !== null);
      if (!skill) return false;
      skill.click();
      return true;
    })()`);
    assert(aiSkillStarted, 'AI skill card was not available');
    await waitFor(client, `Boolean(document.querySelector('.global-ai-floating-root.ant-drawer-open .assistant-message.is-user'))`, 'AI skill user message');
    await waitFor(client, `Boolean(document.querySelector('.global-ai-floating-root.ant-drawer-open .global-ai-processing')) || Boolean([...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .assistant-message:not(.is-user) .assistant-message-bubble')].find((element) => element.textContent.trim().length > 0))`, 'AI processing or result');
    const aiChatOverflow = await evaluate(client, `(() => {
      const root = document.querySelector('.global-ai-floating-root.ant-drawer-open');
      return [
        '.ant-drawer-content',
        '.ant-drawer-body',
        '.global-ai-main-view',
        '.assistant-conversation',
        '.assistant-conversation-messages',
        '.global-ai-processing',
        '.assistant-composer',
      ].some((selector) => {
        const element = root?.querySelector(selector);
        return Boolean(element && element.scrollWidth > element.clientWidth + 1);
      });
    })()`);
    assert(!aiChatOverflow, 'AI chat or processing state has horizontal overflow');

    await evaluate(client, `document.querySelector('[aria-label="新建 AI 会话"]')?.click()`);
    await waitFor(client, `Boolean(document.querySelector('.global-ai-floating-root.ant-drawer-open .global-ai-welcome'))`, 'AI new conversation home');
    await evaluate(client, `document.querySelector('[aria-label="打开 AI 对话历史"]')?.click()`);
    await waitFor(client, `Boolean(document.querySelector('.global-ai-floating-root.ant-drawer-open .global-ai-history'))`, 'AI history view');
    const aiHistoryState = await evaluate(client, `(() => {
      const root = document.querySelector('.global-ai-floating-root.ant-drawer-open');
      const history = root?.querySelector('.global-ai-history');
      const list = root?.querySelector('.global-ai-history-list');
      return {
        itemCount: root?.querySelectorAll('.global-ai-history-item').length ?? 0,
        overflow: Boolean(
          (history && history.scrollWidth > history.clientWidth + 1)
          || (list && list.scrollWidth > list.clientWidth + 1)
        ),
      };
    })()`);
    assert(aiHistoryState.itemCount >= 1, 'AI conversation was not archived into history');
    assert(!aiHistoryState.overflow, 'AI history view has horizontal overflow');
    await evaluate(client, `([...document.querySelectorAll('.global-ai-history-item')].find((element) => element.offsetParent !== null))?.click()`);
    await waitFor(client, `Boolean(document.querySelector('.global-ai-floating-root.ant-drawer-open .assistant-message.is-user'))`, 'AI history restore');
    interactionChecks.push('ai-agent-workflow');

    context = 'interaction:ai-panel-modes';
    await waitFor(client, `[...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .anticon-expand')].some((icon) => icon.offsetParent !== null)`, 'AI focus trigger');
    await evaluate(client, `([...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .anticon-expand')].find((icon) => icon.offsetParent !== null))?.closest('button')?.click()`);
    await waitFor(client, `[...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .ant-drawer-content-wrapper')].some((wrapper) => wrapper.offsetParent !== null && wrapper.getBoundingClientRect().width >= 820)`, 'AI focus mode');
    const aiFocusState = await evaluate(client, `(() => {
      const wrapper = [...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .ant-drawer-content-wrapper')].find((element) => element.offsetParent !== null);
      const content = wrapper?.querySelector('.ant-drawer-content');
      return {
        width: wrapper?.getBoundingClientRect().width ?? 0,
        overflow: Boolean(content && content.scrollWidth > content.clientWidth + 1),
      };
    })()`);
    assert(aiFocusState.width >= 820 && aiFocusState.width <= 900, 'AI focus panel width is invalid');
    assert(!aiFocusState.overflow, 'AI focus panel has horizontal overflow');
    await evaluate(client, `([...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .anticon-compress')].find((icon) => icon.offsetParent !== null))?.closest('button')?.click()`);
    await waitFor(client, `[...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .ant-drawer-content-wrapper')].some((wrapper) => wrapper.offsetParent !== null && wrapper.getBoundingClientRect().width <= 640)`, 'AI panel standard mode restored');
    await evaluate(client, `([...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .anticon-close')].find((icon) => icon.offsetParent !== null))?.closest('button')?.click()`);
    await waitFor(client, `!document.querySelector('.global-ai-floating-root.ant-drawer-open') && Boolean(document.querySelector('[aria-label="打开 AI 运维助手"]'))`, 'AI panel closed');
    interactionChecks.push('ai-panel-modes');

    context = 'interaction:ai-mobile-panel';
    await setViewport(client, VIEWPORTS[2]);
    await evaluate(client, `document.querySelector('[aria-label="打开 AI 运维助手"]')?.click()`);
    await waitFor(client, `Boolean(document.querySelector('.global-ai-floating-root.ant-drawer-open .ant-drawer-content-wrapper'))`, 'AI mobile panel open');
    await waitFor(client, `(() => { const rect = document.querySelector('.global-ai-floating-root.ant-drawer-open .ant-drawer-content-wrapper')?.getBoundingClientRect(); return Boolean(rect && Math.abs(rect.left) < 1 && Math.abs(rect.top) < 1 && Math.abs(rect.width - window.innerWidth) < 1 && Math.abs(rect.height - window.innerHeight) < 1); })()`, 'AI mobile panel full screen');
    const aiMobileState = await evaluate(client, `(() => {
      const rect = document.querySelector('.global-ai-floating-root.ant-drawer-open .ant-drawer-content-wrapper')?.getBoundingClientRect();
      return {
        left: rect?.left ?? -1,
        top: rect?.top ?? -1,
        width: rect?.width ?? 0,
        height: rect?.height ?? 0,
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight,
        bodyOverflow: document.body.scrollWidth > window.innerWidth + 2,
      };
    })()`);
    assert(Math.abs(aiMobileState.left) < 1 && Math.abs(aiMobileState.top) < 1, 'AI mobile panel is not full screen');
    assert(Math.abs(aiMobileState.width - aiMobileState.viewportWidth) < 1 && Math.abs(aiMobileState.height - aiMobileState.viewportHeight) < 1, 'AI mobile panel size mismatch');
    assert(!aiMobileState.bodyOverflow, 'AI mobile panel causes horizontal overflow');
    await evaluate(client, `([...document.querySelectorAll('.global-ai-floating-root.ant-drawer-open .anticon-close')].find((icon) => icon.offsetParent !== null))?.closest('button')?.click()`);
    await waitFor(client, `!document.querySelector('.global-ai-floating-root.ant-drawer-open')`, 'AI mobile panel close');
    await setViewport(client, VIEWPORTS[0]);
    interactionChecks.push('ai-mobile-panel');

    context = 'interaction:deep-link-refresh';
    await hardNavigate(client, '/assets?device=b1-z1-u1');
    await waitFor(client, `document.querySelector('.ops-detail-drawer.ant-drawer-open')?.textContent.includes('b1-z1-u1')`, 'asset deep link drawer');
    await client.send('Page.reload', { ignoreCache: true });
    await waitFor(client, `document.querySelector('.ops-detail-drawer.ant-drawer-open')?.textContent.includes('b1-z1-u1')`, 'asset deep link reload');
    interactionChecks.push('deep-link-refresh');

    context = 'interaction:drawer-escape';
    await pressEscape(client);
    await waitFor(client, `!document.querySelector('.ops-detail-drawer.ant-drawer-open') && !new URLSearchParams(location.search).has('device')`, 'drawer escape close');
    interactionChecks.push('drawer-escape');

    context = 'interaction:history';
    await spaNavigate(client, '/fdd?diagnosis=FDD-77');
    await waitFor(client, `document.querySelector('.ops-detail-drawer.ant-drawer-open')?.textContent.includes('FDD-77')`, 'FDD drawer');
    await clickText(client, '查看资产', '.ops-detail-drawer.ant-drawer-open');
    await waitFor(client, `location.pathname === '/assets' && new URLSearchParams(location.search).get('device') === 'b1-z1-u1'`, 'history target asset');
    await evaluate(client, `history.back()`);
    await waitFor(client, `location.pathname === '/fdd' && new URLSearchParams(location.search).get('diagnosis') === 'FDD-77'`, 'history back');
    await evaluate(client, `history.forward()`);
    await waitFor(client, `location.pathname === '/assets' && new URLSearchParams(location.search).get('device') === 'b1-z1-u1'`, 'history forward');
    interactionChecks.push('history-back-forward');

    context = 'interaction:energy-mtd';
    await spaNavigate(client, '/energy/month');
    await waitForRoute(client, ROUTES.find((route) => route.path === '/energy/month'), true);
    await waitFor(client, `new URLSearchParams(location.search).has('year') && new URLSearchParams(location.search).has('month') && new URLSearchParams(location.search).has('day')`, 'energy canonical URL');
    const energyMtdState = await evaluate(client, `(() => {
      const now = new Date();
      const params = new URLSearchParams(location.search);
      return {
        year: Number(params.get('year')),
        month: Number(params.get('month')),
        day: Number(params.get('day')),
        currentYear: now.getFullYear(),
        currentMonth: now.getMonth() + 1,
        currentDay: now.getDate(),
        measuredLabel: document.body.innerText.includes(now.getDate() + ' 个计量日'),
        futureRows: document.querySelectorAll('.energy-month-row.is-future').length,
      };
    })()`);
    assert(energyMtdState.year === energyMtdState.currentYear, 'Energy default year is not current year');
    assert(energyMtdState.month === energyMtdState.currentMonth, 'Energy default month is not current month');
    assert(energyMtdState.day === energyMtdState.currentDay, 'Energy current month did not stop at today');
    assert(energyMtdState.measuredLabel, 'Energy current month measured-day label is incorrect');
    assert(energyMtdState.futureRows === 12 - energyMtdState.currentMonth, 'Energy future month rows are not disabled');
    interactionChecks.push('energy-mtd-boundary');

    context = 'interaction:energy-period';
    const targetMonth = await evaluate(client, `(() => {
      const current = new Date().getMonth() + 1;
      return current === 1 ? 12 : current - 1;
    })()`);
    const targetMonthLabel = `${targetMonth}月`;
    const energyMonthClicked = await evaluate(client, `(() => {
      const row = [...document.querySelectorAll('.energy-month-row')].find((element) => (
        element.querySelector('td')?.textContent.trim() === ${JSON.stringify(targetMonthLabel)}
      ));
      if (!row) return false;
      row.click();
      return true;
    })()`);
    assert(energyMonthClicked, `Energy month row not found: ${targetMonth}`);
    await waitFor(
      client,
      `[...document.querySelectorAll('.energy-month-row.is-selected')].some((row) => row.querySelector('td')?.textContent.trim() === ${JSON.stringify(targetMonthLabel)})`,
      'energy month selection',
    );
    const energyPeriodState = await evaluate(client, `(() => {
      const params = new URLSearchParams(location.search);
      const year = Number(params.get('year'));
      const month = Number(params.get('month'));
      return {
        month,
        day: Number(params.get('day')),
        expectedDay: new Date(year, month, 0).getDate(),
      };
    })()`);
    assert(energyPeriodState.month === targetMonth, 'Energy selected month was not written to URL');
    assert(energyPeriodState.day === energyPeriodState.expectedDay, 'Energy historical month did not select its last measured day');
    interactionChecks.push('energy-period-selection');

    context = 'interaction:energy-category-drilldown';
    const energyTypeClicked = await evaluate(client, `(() => {
      const root = document.querySelector('.energy-table-card');
      const item = root ? [...root.querySelectorAll('.ant-segmented-item')].find((element) => (
        element.offsetParent !== null && element.textContent.replace(/\\s+/g, '') === '冷水机组'
      )) : null;
      if (!item) return false;
      item.click();
      return true;
    })()`);
    assert(energyTypeClicked, 'Energy category drilldown control was not found');
    await waitFor(client, `new URLSearchParams(location.search).get('type') === 'chiller'`, 'energy category URL');
    const energyFilteredDevices = await evaluate(client, `[...document.querySelectorAll('.energy-device-link')].map((element) => element.dataset.opsDetailTrigger)`);
    assert(energyFilteredDevices.length === 2, `Energy chiller drilldown expected 2 devices, got ${energyFilteredDevices.length}`);
    assert(energyFilteredDevices.every((id) => ['b1-z1-u1', 'b1-z1-u2'].includes(id)), 'Energy category drilldown returned non-chiller devices');
    interactionChecks.push('energy-category-drilldown');

    context = 'interaction:energy-device-detail';
    const energyDeviceId = energyFilteredDevices[0];
    const energyDeviceOpened = await evaluate(client, `(() => {
      const trigger = [...document.querySelectorAll('.energy-device-link')].find((element) => (
        element.offsetParent !== null && element.dataset.opsDetailTrigger === ${JSON.stringify(energyDeviceId)}
      ));
      if (!trigger) return false;
      trigger.click();
      return true;
    })()`);
    assert(energyDeviceOpened, 'Energy device detail trigger was not found');
    await waitFor(client, `new URLSearchParams(location.search).get('device') === ${JSON.stringify(energyDeviceId)}`, 'energy device URL');
    await waitFor(client, `Boolean(document.querySelector('.ops-detail-drawer.ant-drawer-open'))`, 'energy device drawer');
    const openedEnergyDeviceId = await evaluate(client, `new URLSearchParams(location.search).get('device')`);
    await pressEscape(client);
    await waitFor(client, `!new URLSearchParams(location.search).has('device') && !document.querySelector('.ops-detail-drawer.ant-drawer-open')`, 'energy device drawer close');
    await waitFor(client, `document.activeElement?.dataset?.opsDetailTrigger === ${JSON.stringify(openedEnergyDeviceId)}`, 'energy device focus restore');
    interactionChecks.push('energy-device-detail');

    context = 'interaction:energy-export';
    await evaluate(client, `(() => {
      window.__energyAuditDownload = null;
      HTMLAnchorElement.prototype.click = function energyAuditClick() {
        window.__energyAuditDownload = { download: this.download, href: this.href };
      };
      return true;
    })()`);
    await clickText(client, '导出当前视图');
    const energyDownload = await evaluate(client, `window.__energyAuditDownload`);
    assert(Boolean(energyDownload?.download?.endsWith('.csv')), 'Energy export did not produce a CSV filename');
    assert(Boolean(energyDownload?.href?.startsWith('blob:')), 'Energy export did not create a Blob URL');
    interactionChecks.push('energy-export');

    context = 'interaction:energy-year-to-month';
    await clickText(client, '年度', '.energy-granularity-nav');
    await waitFor(client, `location.pathname === '/energy/year' && document.body.innerText.includes('年度能耗分析')`, 'energy year workspace');
    await clickText(client, '进入月度异常定位');
    await waitFor(client, `location.pathname === '/energy/month' && document.body.innerText.includes('月度能耗分析')`, 'energy year to month');
    interactionChecks.push('energy-year-to-month');

    context = 'interaction:energy-week-to-day';
    await clickText(client, '周度', '.energy-granularity-nav');
    await waitFor(client, `location.pathname === '/energy/week' && document.body.innerText.includes('周度能耗分析')`, 'energy week workspace');
    const weekDayOpened = await evaluate(client, `(() => {
      const trigger = [...document.querySelectorAll('.energy-table-card .energy-device-link')].find((element) => element.offsetParent !== null);
      if (!trigger) return false;
      trigger.click();
      return true;
    })()`);
    assert(weekDayOpened, 'Energy week day drilldown trigger was not found');
    await waitFor(client, `location.pathname === '/energy/day' && document.body.innerText.includes('日度能耗分析')`, 'energy week to day');
    interactionChecks.push('energy-week-to-day');

    context = 'interaction:modal';
    await spaNavigate(client, '/system?tab=users');
    await waitFor(client, `document.body.innerText.includes('用户权限')`, 'system users tab');
    await clickText(client, '新建用户');
    await waitFor(client, `[...document.querySelectorAll('.ant-modal')].some((element) => element.offsetParent !== null && element.textContent.includes('新建用户'))`, 'user modal');
    await pressEscape(client);
    await waitFor(client, `![...document.querySelectorAll('.ant-modal')].some((element) => element.offsetParent !== null)`, 'user modal escape');
    interactionChecks.push('modal-escape');

    context = 'interaction:popconfirm';
    await clickText(client, '禁用');
    await waitFor(client, `[...document.querySelectorAll('.ant-popover')].some((element) => element.offsetParent !== null && element.textContent.includes('确认禁用该用户'))`, 'disable popconfirm');
    await clickText(client, '取消');
    await waitFor(client, `![...document.querySelectorAll('.ant-popover')].some((element) => element.offsetParent !== null && element.textContent.includes('确认禁用该用户'))`, 'popconfirm cancel');
    interactionChecks.push('popconfirm');

    context = 'interaction:mobile-popover';
    await setViewport(client, VIEWPORTS[2]);
    await spaNavigate(client, '/dashboard');
    await waitForRoute(client, ROUTES[0], true);
    await clickText(client, '视图配置');
    await waitFor(client, `[...document.querySelectorAll('.ant-popover')].some((element) => element.offsetParent !== null && element.textContent.includes('视图配置仅影响'))`, 'mobile view popover');
    const popoverOverflow = await evaluate(client, `(() => {
      const popover = [...document.querySelectorAll('.ant-popover')].find((element) => element.offsetParent !== null);
      if (!popover) return true;
      const rect = popover.getBoundingClientRect();
      return rect.left < -2 || rect.right > window.innerWidth + 2;
    })()`);
    assert(!popoverOverflow, 'Mobile view configuration popover overflows viewport');
    await pressEscape(client);
    interactionChecks.push('mobile-popover');

    context = 'interaction:bigscreen-escape';
    await spaNavigate(client, '/bigscreen');
    await waitForRoute(client, ROUTES.find((route) => route.path === '/bigscreen'), true);
    await pressEscape(client);
    await waitFor(client, `location.pathname === '/dashboard'`, 'bigscreen escape');
    interactionChecks.push('bigscreen-escape');

    context = 'interaction:404-return';
    await spaNavigate(client, '/release-audit-missing-route');
    await waitFor(client, `document.querySelector('.ant-result-title')?.textContent.trim() === '404'`, '404 return start');
    await clickText(client, '返回总览驾驶舱');
    await waitFor(client, `location.pathname === '/dashboard' && document.body.innerText.includes('智慧能源运营总览')`, '404 return dashboard');
    interactionChecks.push('404-return');

    await pause(300);
    const actionableProblems = browserProblems.filter((problem) => {
      const detail = String(problem.detail);
      return !detail.includes('ResizeObserver loop') && !detail.includes('favicon.ico');
    });
    assert(actionableProblems.length === 0, `Browser problems detected: ${JSON.stringify(actionableProblems, null, 2)}`);

    console.log(JSON.stringify({
      passed: accessChecks.length + visualChecks.length + interactionChecks.length,
      accessChecks: accessChecks.length,
      visualChecks: visualChecks.length,
      interactionChecks,
      browserProblems: actionableProblems,
    }, null, 2));
  } finally {
    client.close();
  }
} finally {
  edge.kill();
  await pause(700);
  await rm(profileDir, { recursive: true, force: true });
}

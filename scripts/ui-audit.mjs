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
  { path: '/ai', title: '泉来禾 AI 运维助手', roles: ['rd'] },
  { path: '/system', title: '系统管理', roles: ['rd'] },
  { path: '/bigscreen', title: '泉来禾智慧能源驾驶舱', roles: ['demo', 'ops', 'rd'], bigscreen: true },
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
    const tableSurfaceIssues = [...document.querySelectorAll([
      '.ops-page .ant-table-thead > tr > th',
      '.ops-page .ant-table-cell-fix-left',
      '.ops-page .ant-table-cell-fix-right',
      '.ops-page .ant-table-sticky-holder',
    ].join(','))]
      .filter((element) => element.offsetParent !== null)
      .filter((element) => {
        const background = getComputedStyle(element).backgroundColor;
        if (background === 'transparent' || background === 'rgba(0, 0, 0, 0)') return true;
        const rgba = background.match(/^rgba\\([^,]+,[^,]+,[^,]+,\\s*([\\d.]+)\\)$/);
        return rgba ? Number(rgba[1]) < 0.98 : false;
      })
      .slice(0, 8)
      .map((element) => ({
        className: String(element.className).slice(0, 140),
        background: getComputedStyle(element).backgroundColor,
        text: element.textContent?.trim().slice(0, 40) ?? '',
      }));
    const redundantSecondaryHierarchy = [...document.querySelectorAll([
      '.ops-page > .ops-page-header .ops-page-eyebrow',
      '.ops-page > .ops-page-header .ops-page-subtitle',
      '.ops-page .ops-section-intro-description',
      '.dashboard-page .dashboard-eyebrow',
      '.dashboard-page .dashboard-hero-subtitle',
    ].join(','))]
      .filter((element) => element.offsetParent !== null)
      .map((element) => ({
        className: String(element.className).slice(0, 120),
        text: element.textContent?.trim().slice(0, 80) ?? '',
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
      pageHeaderHeight: document.querySelector('.ops-page-header')?.getBoundingClientRect().height ?? null,
      hasAssetsRedundantCopy: ['维护建筑、分区、设备、通讯网关与点位资产', '当前选中：', '后续接入真实资产接口后']
        .some((text) => visibleText.includes(text)),
      tableSurfaceIssues,
      redundantSecondaryHierarchy,
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
  assert(
    result.redundantSecondaryHierarchy.length === 0,
    `${route.path} restored redundant secondary hierarchy at ${viewport.name}/${theme}: ${JSON.stringify(result.redundantSecondaryHierarchy)}`,
  );
  assert(
    result.tableSurfaceIssues.length === 0,
    `${route.path} has transparent table header/fixed surfaces at ${viewport.name}/${theme}: ${JSON.stringify(result.tableSurfaceIssues)}`,
  );
  if (route.path.startsWith('/energy/')) {
    const maximumFirstChartTop = viewport.name === 'mobile' ? 1000 : viewport.name === 'tablet' ? 820 : 650;
    assert(
      typeof result.firstChartTop === 'number' && result.firstChartTop <= maximumFirstChartTop,
      `${route.path} pushes primary evidence below the visual-density limit at ${viewport.name}/${theme}: ${result.firstChartTop}px > ${maximumFirstChartTop}px`,
    );
  }
  if (route.path === '/assets') {
    const maximumHeaderHeight = viewport.name === 'mobile' ? 92 : 70;
    assert(!result.hasAssetsRedundantCopy, `/assets redundant explanatory copy returned at ${viewport.name}/${theme}`);
    assert(
      typeof result.pageHeaderHeight === 'number' && result.pageHeaderHeight <= maximumHeaderHeight,
      `/assets compact header is too tall at ${viewport.name}/${theme}: ${result.pageHeaderHeight}px > ${maximumHeaderHeight}px`,
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

    context = 'interaction:ai-copilot-popup';
    await spaNavigate(client, '/dashboard');
    await waitForRoute(client, ROUTES[0], true);
    await waitFor(client, `Boolean(document.querySelector('.copilotKitPopup') || document.querySelector('.hvac-copilot-toggle'))`, 'CopilotPopup mount');
    await waitFor(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      if (popup) {
        const rect = popup.getBoundingClientRect();
        if (rect.width > 0 && rect.height > 0 && Number(getComputedStyle(popup).opacity || 1) > 0.9) return true;
        if (rect.width > 0 && rect.height > 0) return false;
      }
      const trigger = document.querySelector('.hvac-copilot-toggle');
      if (!trigger) return false;
      const rect = trigger.getBoundingClientRect();
      const style = getComputedStyle(trigger);
      if (rect.width <= 0 || rect.height <= 0 || style.display === 'none' || style.visibility === 'hidden' || style.pointerEvents === 'none') return false;
      trigger.click();
      return false;
    })()`, 'CopilotPopup open');
    const aiPopupState = await evaluate(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      const rect = popup?.getBoundingClientRect();
      const background = popup ? getComputedStyle(popup).backgroundColor : '';
      const popupStyle = popup ? getComputedStyle(popup) : null;
      const toggle = document.querySelector('.hvac-copilot-toggle');
      const toggleRect = toggle?.getBoundingClientRect();
      const toggleStyle = toggle ? getComputedStyle(toggle) : null;
      const overlapWidth = rect && toggleRect ? Math.max(0, Math.min(rect.right, toggleRect.right) - Math.max(rect.left, toggleRect.left)) : 0;
      const overlapHeight = rect && toggleRect ? Math.max(0, Math.min(rect.bottom, toggleRect.bottom) - Math.max(rect.top, toggleRect.top)) : 0;
      const composer = popup?.querySelector('.copilotKitInput');
      const composerRect = composer?.getBoundingClientRect();
      const composerStyle = composer ? getComputedStyle(composer) : null;
      const sendButton = composer?.querySelector('[data-testid="copilot-send-button"]');
      const sendRect = sendButton?.getBoundingClientRect();
      const addButton = composer?.querySelector('[data-testid="copilot-add-menu-button"]');
      const addRect = addButton?.getBoundingClientRect();
      const customText = [...(popup?.querySelectorAll([
        '.hvac-copilot-popup-context',
        '.hvac-copilot-section-heading',
        '.hvac-copilot-recent-list strong',
        '.hvac-copilot-recent-list time',
      ].join(',')) ?? [])]
        .filter((element) => element.textContent?.trim() && getComputedStyle(element).display !== 'none')
        .map((element) => Number.parseFloat(getComputedStyle(element).fontSize));
      const offenders = [...(popup?.querySelectorAll('*') ?? [])].filter((element) => {
        if (element.offsetParent === null) return false;
        const overflowX = getComputedStyle(element).overflowX;
        return element.scrollWidth > element.clientWidth + 1 && overflowX !== 'hidden' && overflowX !== 'clip';
      });
      return {
        title: popup?.getAttribute('aria-label') ?? '',
        headerText: popup?.querySelector('.hvac-copilot-header')?.textContent?.trim() ?? '',
        popupText: popup?.textContent?.trim() ?? '',
        width: rect?.width ?? 0,
        height: rect?.height ?? 0,
        background,
        popupRadius: Number.parseFloat(popupStyle?.borderRadius || '0'),
        composerRadius: Number.parseFloat(composerStyle?.borderRadius || '0'),
        composerHeight: composerRect?.height ?? 0,
        composerBorderWidth: Number.parseFloat(composerStyle?.borderTopWidth || '0'),
        composerBorderColor: composerStyle?.borderTopColor ?? '',
        composerShadow: composerStyle?.boxShadow ?? '',
        sendWidth: sendRect?.width ?? 0,
        sendHeight: sendRect?.height ?? 0,
        disabledAddHidden: !addButton || !addButton.disabled || (addRect?.width ?? 0) === 0,
        toggleWidth: toggleRect?.width ?? 0,
        toggleHeight: toggleRect?.height ?? 0,
        toggleRadius: Number.parseFloat(toggleStyle?.borderRadius || '0'),
        toggleBackgroundImage: toggleStyle?.backgroundImage ?? '',
        toggleOpacity: Number.parseFloat(toggleStyle?.opacity || '0'),
        toggleOverlapArea: overlapWidth * overlapHeight,
        headerActionCount: popup?.querySelectorAll('.hvac-copilot-header-actions > button').length ?? 0,
        hasReadonlyBadge: Boolean(popup?.querySelector('.hvac-copilot-readonly-badge')),
        hasExpandAction: Boolean(popup?.querySelector('button[aria-label="打开完整 AI 工作台"]')),
        minCustomFontSize: customText.length ? Math.min(...customText) : 0,
        offenderCount: offenders.length,
        hasInput: Boolean(popup?.querySelector('textarea')),
        hasPersistentDisclaimer: [...(popup?.querySelectorAll('*') ?? [])].some((element) => (
          element.childElementCount === 0
          && (element.textContent?.includes('AI can make mistakes') || element.textContent?.includes('设备控制和业务写入必须人工确认'))
          && element.offsetParent !== null
          && getComputedStyle(element).display !== 'none'
        )),
        hasBrandedHeader: Boolean(popup?.querySelector('.hvac-copilot-header-layout')),
        hasBrandedWelcome: Boolean(popup?.querySelector('.hvac-copilot-welcome')),
        hasPopupContext: Boolean(popup?.querySelector('.hvac-copilot-popup-context')),
        hasDecorativeBrief: Boolean(popup?.querySelector('.hvac-copilot-brief, .hvac-copilot-scope-line')),
        recentThreadCount: popup?.querySelectorAll('.hvac-copilot-recent-list > button').length ?? 0,
        hasHistoryAction: Boolean(popup?.querySelector('button[aria-label="对话历史"]')),
        suggestionCount: popup?.querySelectorAll('.hvac-copilot-suggestion').length ?? 0,
        hasBrandedToggle: Boolean(document.querySelector('.hvac-copilot-toggle')),
        hasAttentionBadge: Boolean(document.querySelector('.hvac-copilot-toggle-count')),
        togglePointerEvents: getComputedStyle(document.querySelector('.hvac-copilot-toggle')).pointerEvents,
      };
    })()`);
    assert(
      aiPopupState.title === '泉来禾 AI 运维助手'
        || aiPopupState.headerText.includes('泉来禾 AI 运维助手')
        || aiPopupState.popupText.includes('泉来禾 AI 运维助手'),
      `CopilotPopup title is invalid: ${JSON.stringify(aiPopupState)}`,
    );
    assert(aiPopupState.width >= 500 && aiPopupState.width <= 560, 'CopilotPopup desktop width is invalid');
    assert(aiPopupState.height >= 600, 'CopilotPopup desktop height is invalid');
    assert(aiPopupState.popupRadius >= 19, 'CopilotPopup does not use the refined floating-window radius');
    assert(aiPopupState.composerRadius >= 16 && aiPopupState.composerRadius <= 20, 'CopilotPopup composer radius is outside the product contract');
    assert(
      aiPopupState.composerHeight >= 72 && aiPopupState.composerHeight <= 82,
      `CopilotPopup composer height is outside the product contract: ${JSON.stringify(aiPopupState)}`,
    );
    assert(aiPopupState.composerBorderWidth >= 1 && aiPopupState.composerShadow !== 'none', 'CopilotPopup composer lacks a clear surface boundary');
    assert(aiPopupState.sendWidth >= 39 && aiPopupState.sendHeight >= 39, 'CopilotPopup send action is too small');
    assert(aiPopupState.disabledAddHidden, 'CopilotPopup exposes an unavailable add action');
    assert(
      aiPopupState.toggleWidth >= 54
        && aiPopupState.toggleWidth <= 64
        && aiPopupState.toggleHeight >= 54
        && aiPopupState.toggleHeight <= 64
        && aiPopupState.toggleRadius >= 27,
      `CopilotPopup launcher is not a prominent circular control: ${JSON.stringify(aiPopupState)}`,
    );
    assert(
      aiPopupState.toggleBackgroundImage !== 'none'
        && aiPopupState.toggleOpacity >= 0.95
        && aiPopupState.toggleOverlapArea === 0,
      `CopilotPopup launcher visibility or placement is invalid: ${JSON.stringify(aiPopupState)}`,
    );
    assert(aiPopupState.headerActionCount === 3 && !aiPopupState.hasReadonlyBadge && !aiPopupState.hasExpandAction, 'CopilotPopup header hierarchy is invalid');
    assert(aiPopupState.minCustomFontSize >= 11, 'CopilotPopup custom business text is too small');
    assert(aiPopupState.background !== 'rgba(0, 0, 0, 0)' && aiPopupState.background !== 'transparent', 'CopilotPopup surface is transparent');
    assert(aiPopupState.offenderCount === 0, 'CopilotPopup has horizontal overflow');
    assert(
      aiPopupState.hasInput
        && !aiPopupState.hasPersistentDisclaimer
        && aiPopupState.hasBrandedHeader
        && aiPopupState.hasBrandedWelcome
        && aiPopupState.hasPopupContext
        && !aiPopupState.hasDecorativeBrief
        && aiPopupState.recentThreadCount >= 3
        && aiPopupState.hasHistoryAction
        && aiPopupState.suggestionCount === 0
        && aiPopupState.hasBrandedToggle
        && !aiPopupState.hasAttentionBadge
        && aiPopupState.togglePointerEvents !== 'none',
      `CopilotPopup HVAC product UI is incomplete: ${JSON.stringify(aiPopupState)}`,
    );
    await evaluate(client, `document.querySelector('.copilotKitPopup .copilotKitInput textarea')?.focus()`);
    await pause(180);
    const aiPopupFocusState = await evaluate(client, `(() => {
      const composer = document.querySelector('.copilotKitPopup .copilotKitInput');
      const style = composer ? getComputedStyle(composer) : null;
      return {
        borderColor: style?.borderTopColor ?? '',
        shadow: style?.boxShadow ?? '',
      };
    })()`);
    assert(
      aiPopupFocusState.borderColor !== aiPopupState.composerBorderColor
        && aiPopupFocusState.shadow !== aiPopupState.composerShadow,
      `CopilotPopup composer focus feedback is missing: ${JSON.stringify(aiPopupFocusState)}`,
    );
    interactionChecks.push('ai-copilot-popup');

    context = 'interaction:ai-popup-history';
    const popupHistoryOpened = await evaluate(client, `(() => {
      const button = document.querySelector('.copilotKitPopup button[aria-label="对话历史"]');
      if (!button) return false;
      button.click();
      return true;
    })()`);
    assert(popupHistoryOpened, 'CopilotPopup history action was not available');
    await waitFor(client, `(() => {
      const panel = document.querySelector('.hvac-copilot-history-panel');
      return Boolean(panel?.querySelector('input[aria-label="搜索 AI 会话"]'))
        && panel.querySelectorAll('.hvac-copilot-history-list > button').length >= 3;
    })()`, 'CopilotPopup history panel');
    const popupHistoryOverflow = await evaluate(client, `(() => {
      const panel = document.querySelector('.hvac-copilot-history-panel');
      return [...(panel?.querySelectorAll('*') ?? [])].some((element) => {
        if (element.offsetParent === null) return false;
        const overflowX = getComputedStyle(element).overflowX;
        return element.scrollWidth > element.clientWidth + 1 && overflowX !== 'hidden' && overflowX !== 'clip';
      });
    })()`);
    assert(!popupHistoryOverflow, 'CopilotPopup history panel has horizontal overflow');
    const historicalThreadOpened = await evaluate(client, `(() => {
      const item = [...document.querySelectorAll('.hvac-copilot-history-list > button')]
        .find((element) => element.textContent.includes('峰时段费用异常分析'));
      if (!item) return false;
      item.click();
      return true;
    })()`);
    assert(historicalThreadOpened, 'CopilotPopup historical thread was not available');
    await waitFor(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      return !document.querySelector('.hvac-copilot-history-panel')
        && popup?.textContent.includes('峰时费用占比约 42%');
    })()`, 'CopilotPopup historical thread restore');
    const popupHistoryNewSession = await evaluate(client, `(() => {
      const button = document.querySelector('.copilotKitPopup button[aria-label="新建会话"]');
      if (!button) return false;
      button.click();
      return true;
    })()`);
    assert(popupHistoryNewSession, 'CopilotPopup new session after history restore was not available');
    await waitFor(client, `Boolean(document.querySelector('.hvac-copilot-welcome'))`, 'CopilotPopup welcome after history restore');
    interactionChecks.push('ai-popup-history');

    context = 'interaction:ai-context-routing';
    const energyNavClicked = await evaluate(client, `(() => {
      const item = [...document.querySelectorAll('.ant-menu-item')].find((element) => element.offsetParent !== null && element.textContent.includes('能耗分析'));
      if (!item) return false;
      item.click();
      return true;
    })()`);
    assert(energyNavClicked, 'Energy navigation item was not found while CopilotPopup was open');
    await waitFor(client, `location.pathname.startsWith('/energy')`, 'energy route with CopilotPopup');
    await waitFor(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      if (!popup) {
        const trigger = document.querySelector('.hvac-copilot-toggle');
        if (!trigger) return false;
        const triggerRect = trigger.getBoundingClientRect();
        const triggerStyle = getComputedStyle(trigger);
        if (triggerRect.width > 0 && triggerRect.height > 0 && triggerStyle.display !== 'none' && triggerStyle.visibility !== 'hidden' && triggerStyle.pointerEvents !== 'none') trigger.click();
        return false;
      }
      const rect = popup.getBoundingClientRect();
      const opacity = Number(getComputedStyle(popup).opacity || 1);
      if (rect.width <= 0 || rect.height <= 0 || opacity < 0.9) return false;
      return popup.querySelector('.hvac-copilot-popup-context')?.textContent.includes('能耗分析') ?? false;
    })()`, 'CopilotPopup route context update');
    const aiRouteContextState = await evaluate(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      return {
        contextText: popup?.querySelector('.hvac-copilot-popup-context')?.textContent?.trim() ?? '',
        placeholder: popup?.querySelector('textarea')?.getAttribute('placeholder') ?? '',
        headerTitle: popup?.querySelector('.hvac-copilot-header-identity > strong')?.textContent?.trim() ?? '',
      };
    })()`);
    assert(
      aiRouteContextState.contextText.includes('能耗分析')
        && aiRouteContextState.contextText.includes('2026 年')
        && aiRouteContextState.placeholder.includes('询问「')
        && aiRouteContextState.headerTitle.length > 0,
      `CopilotPopup route-specific content is stale: ${JSON.stringify(aiRouteContextState)}`,
    );
    interactionChecks.push('ai-context-routing');

    context = 'interaction:ai-agent-workflow';
    const aiQuestionEntered = await evaluate(client, `(() => {
      const textarea = document.querySelector('.copilotKitPopup textarea');
      if (!textarea) return false;
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set;
      setter?.call(textarea, '当前园区总功率和综合 COP 是多少？');
      textarea.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: '当前园区总功率和综合 COP 是多少？' }));
      textarea.dispatchEvent(new Event('change', { bubbles: true }));
      return true;
    })()`);
    assert(aiQuestionEntered, 'CopilotPopup input was not available');
    await pause(100);
    const aiSendClicked = await evaluate(client, `(() => {
      const input = document.querySelector('.copilotKitInput');
      const buttons = [...(input?.querySelectorAll('button') ?? [])].filter((element) => element.offsetParent !== null && !element.disabled);
      const send = buttons.at(-1);
      if (!send) return false;
      send.click();
      return true;
    })()`);
    assert(aiSendClicked, 'CopilotPopup send button was not available');
    await waitFor(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      const card = popup?.querySelector('.hvac-agent-result-card');
      return popup?.textContent.includes('总功率约')
        && card?.textContent.includes('能耗异常调查')
        && card?.textContent.includes('额外能耗');
    })()`, 'CopilotPopup local Agent answer and energy result card');
    const aiAnswerOverflow = await evaluate(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      return [...(popup?.querySelectorAll('*') ?? [])].some((element) => {
        if (element.offsetParent === null) return false;
        const overflowX = getComputedStyle(element).overflowX;
        return element.scrollWidth > element.clientWidth + 1 && overflowX !== 'hidden' && overflowX !== 'clip';
      });
    })()`);
    assert(!aiAnswerOverflow, 'CopilotPopup answer state has horizontal overflow');

    const startNewCopilotSession = async (label) => {
      const clicked = await evaluate(client, `(() => {
        const button = [...document.querySelectorAll('.copilotKitPopup button')].find((element) => element.offsetParent !== null && element.getAttribute('aria-label') === '新建会话');
        if (!button) return false;
        button.click();
        return true;
      })()`);
      assert(clicked, `CopilotPopup new-session action was not available for ${label}`);
      await waitFor(client, `Boolean(document.querySelector('.hvac-copilot-welcome')) && !document.querySelector('.hvac-agent-result-card')`, `CopilotPopup new session ${label}`);
    };

    const submitCopilotQuestion = async (question, expectedCardText, label) => {
      const entered = await evaluate(client, `(() => {
        const textarea = document.querySelector('.copilotKitPopup textarea');
        if (!textarea) return false;
        const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set;
        setter?.call(textarea, ${JSON.stringify(question)});
        textarea.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: ${JSON.stringify(question)} }));
        textarea.dispatchEvent(new Event('change', { bubbles: true }));
        return true;
      })()`);
      assert(entered, `CopilotPopup input was not available for ${label}`);
      await pause(100);
      const sent = await evaluate(client, `(() => {
        const input = document.querySelector('.copilotKitInput');
        const buttons = [...(input?.querySelectorAll('button') ?? [])].filter((element) => element.offsetParent !== null && !element.disabled);
        const send = buttons.at(-1);
        if (!send) return false;
        send.click();
        return true;
      })()`);
      assert(sent, `CopilotPopup send button was not available for ${label}`);
      await waitFor(client, `document.querySelector('.hvac-agent-result-card')?.textContent.includes(${JSON.stringify(expectedCardText)})`, `CopilotPopup ${label} result card`);
    };

    await startNewCopilotSession('asset card');
    await submitCopilotQuestion('哪台设备 COP 最低？', '设备运行分析', 'asset');
    await startNewCopilotSession('FDD card');
    await submitCopilotQuestion('解释最严重的诊断证据', 'AI 置信度', 'FDD');
    const aiStructuredCardsOverflow = await evaluate(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      return [...(popup?.querySelectorAll('*') ?? [])].some((element) => {
        if (element.offsetParent === null) return false;
        const overflowX = getComputedStyle(element).overflowX;
        return element.scrollWidth > element.clientWidth + 1 && overflowX !== 'hidden' && overflowX !== 'clip';
      });
    })()`);
    assert(!aiStructuredCardsOverflow, 'CopilotPopup structured result cards have horizontal overflow');
    await setTheme(client, 'dark');
    const aiDarkState = await evaluate(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      const card = popup?.querySelector('.hvac-agent-result-card');
      const popupBackground = popup ? getComputedStyle(popup).backgroundColor : '';
      const cardBackground = card ? getComputedStyle(card).backgroundColor : '';
      const overflow = [...(popup?.querySelectorAll('*') ?? [])].some((element) => {
        if (element.offsetParent === null) return false;
        const overflowX = getComputedStyle(element).overflowX;
        return element.scrollWidth > element.clientWidth + 1 && overflowX !== 'hidden' && overflowX !== 'clip';
      });
      return { popupBackground, cardBackground, overflow };
    })()`);
    assert(
      aiDarkState.popupBackground !== 'rgba(0, 0, 0, 0)'
        && aiDarkState.popupBackground !== 'transparent'
        && aiDarkState.cardBackground !== 'rgba(0, 0, 0, 0)'
        && aiDarkState.cardBackground !== 'transparent'
        && !aiDarkState.overflow,
      `CopilotPopup dark result state is invalid: ${JSON.stringify(aiDarkState)}`,
    );
    await setTheme(client, 'light');
    interactionChecks.push('ai-agent-workflow');

    context = 'interaction:ai-popup-toggle';
    const aiClosed = await evaluate(client, `(() => {
      const close = [...document.querySelectorAll('.copilotKitPopup button')].find((element) => element.offsetParent !== null && element.getAttribute('aria-label') === 'Close');
      if (!close) return false;
      close.click();
      return true;
    })()`);
    assert(aiClosed, 'CopilotPopup close action was not available');
    await waitFor(client, `(() => { if (document.querySelector('.copilotKitPopup')) return false; const button = document.querySelector('.hvac-copilot-toggle'); if (!button) return false; const rect = button.getBoundingClientRect(); const style = getComputedStyle(button); return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden' && style.pointerEvents !== 'none'; })()`, 'CopilotPopup closed');
    await evaluate(client, `document.querySelector('.hvac-copilot-toggle')?.click()`);
    await waitFor(client, `document.querySelector('.copilotKitPopup')?.offsetParent !== null`, 'CopilotPopup reopened');
    interactionChecks.push('ai-popup-toggle');

    context = 'interaction:ai-mobile-popup';
    const aiClosedBeforeMobile = await evaluate(client, `(() => {
      const close = [...document.querySelectorAll('.copilotKitPopup button')].find((element) => element.offsetParent !== null && element.getAttribute('aria-label') === 'Close');
      if (!close) return false;
      close.click();
      return true;
    })()`);
    assert(aiClosedBeforeMobile, 'CopilotPopup could not close before mobile viewport');
    await waitFor(client, `(() => { if (document.querySelector('.copilotKitPopup')) return false; const button = document.querySelector('.hvac-copilot-toggle'); if (!button) return false; const rect = button.getBoundingClientRect(); return rect.width > 0 && rect.height > 0; })()`, 'CopilotPopup closed before mobile viewport');
    await setViewport(client, VIEWPORTS[2]);
    await hardNavigate(client, '/dashboard');
    await waitForRoute(client, ROUTES[0], true);
    await waitFor(client, `(() => {
      const popup = [...document.querySelectorAll('.copilotKitPopup')].find((element) => {
        const rect = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden';
      });
      const button = document.querySelector('.hvac-copilot-toggle');
      const rect = button?.getBoundingClientRect();
      return Boolean(popup) || Boolean(button && rect.width > 0 && rect.height > 0);
    })()`, 'CopilotPopup mobile state');
    await evaluate(client, `(() => {
      const close = [...document.querySelectorAll('.copilotKitPopup button')]
        .find((element) => element.getAttribute('aria-label') === 'Close');
      close?.click();
    })()`);
    await waitFor(client, `(() => {
      if (document.querySelector('.copilotKitPopup')) return false;
      const button = document.querySelector('.hvac-copilot-toggle');
      if (!button) return false;
      const rect = button.getBoundingClientRect();
      const style = getComputedStyle(button);
      return rect.width > 0 && rect.height > 0
        && style.display !== 'none'
        && style.visibility !== 'hidden'
        && style.pointerEvents !== 'none'
        && Number(style.opacity || 1) > 0.01;
    })()`, 'CopilotPopup mobile launcher');
    const mobileTriggered = await evaluate(client, `(() => {
      const trigger = document.querySelector('.hvac-copilot-toggle');
      if (!trigger) return false;
      trigger.click();
      return true;
    })()`);
    assert(mobileTriggered, 'CopilotPopup mobile launcher could not be clicked');
    await pause(1400);
    const mobileLaunchState = await evaluate(client, `(() => {
      const isVisible = (element) => {
        const rect = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        return rect.width > 0
          && rect.height > 0
          && style.display !== 'none'
          && style.visibility !== 'hidden'
          && Number(style.opacity || 1) > 0.01;
      };
      return {
        popupCount: document.querySelectorAll('.copilotKitPopup').length,
        visiblePopupCount: [...document.querySelectorAll('.copilotKitPopup')].filter(isVisible).length,
      };
    })()`);
    assert(mobileLaunchState.visiblePopupCount > 0, `CopilotPopup mobile window did not open: ${JSON.stringify(mobileLaunchState)}`);
    const mobileFullScreenExpression = `(() => {
      return [...document.querySelectorAll('.copilotKitPopup')].some((popup) => {
        const rect = popup.getBoundingClientRect();
        const style = getComputedStyle(popup);
        const visible = rect.width > 0
          && rect.height > 0
          && style.display !== 'none'
          && style.visibility !== 'hidden'
          && Number(style.opacity || 1) > 0.01;
        return visible
          && Math.abs(rect.left) < 1.5
          && Math.abs(rect.top) < 1.5
          && Math.abs(rect.width - window.innerWidth) < 1.5
          && Math.abs(rect.height - window.innerHeight) < 1.5;
      });
    })()`;
    await waitFor(client, mobileFullScreenExpression, 'CopilotPopup mobile open');
    await pause(320);
    await waitFor(client, mobileFullScreenExpression, 'CopilotPopup mobile stable');
    const aiMobileState = await evaluate(client, `(() => {
      const popup = [...document.querySelectorAll('.copilotKitPopup')]
        .filter((element) => {
          const rect = element.getBoundingClientRect();
          const style = getComputedStyle(element);
          return rect.width > 0
            && rect.height > 0
            && style.display !== 'none'
            && style.visibility !== 'hidden'
            && Number(style.opacity || 1) > 0.01;
        })
        .sort((a, b) => {
          const ar = a.getBoundingClientRect();
          const br = b.getBoundingClientRect();
          return (br.width * br.height) - (ar.width * ar.height);
        })[0];
      const rect = popup?.getBoundingClientRect();
      const offenders = [...(popup?.querySelectorAll('*') ?? [])].filter((element) => {
        if (element.offsetParent === null) return false;
        const overflowX = getComputedStyle(element).overflowX;
        return element.scrollWidth > element.clientWidth + 1 && overflowX !== 'hidden' && overflowX !== 'clip';
      });
      return {
        left: rect?.left ?? -1,
        top: rect?.top ?? -1,
        width: rect?.width ?? 0,
        height: rect?.height ?? 0,
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight,
        bodyOverflow: document.body.scrollWidth > window.innerWidth + 2,
        offenderCount: offenders.length,
        mediaMatches: matchMedia('(max-width: 767px)').matches,
        instances: [...document.querySelectorAll('.copilotKitPopup')].map((element) => {
          const itemRect = element.getBoundingClientRect();
          const style = getComputedStyle(element);
          return {
            visible: element.offsetParent !== null,
            left: itemRect.left,
            top: itemRect.top,
            width: itemRect.width,
            height: itemRect.height,
            position: style.position,
            inset: style.inset,
            transform: style.transform,
          };
        }),
      };
    })()`);
    assert(
      Math.abs(aiMobileState.left) < 1.5 && Math.abs(aiMobileState.top) < 1.5,
      `CopilotPopup mobile view is not full screen: ${JSON.stringify(aiMobileState)}`,
    );
    assert(
      Math.abs(aiMobileState.width - aiMobileState.viewportWidth) < 1.5 && Math.abs(aiMobileState.height - aiMobileState.viewportHeight) < 1.5,
      `CopilotPopup mobile size mismatch: ${JSON.stringify(aiMobileState)}`,
    );
    assert(!aiMobileState.bodyOverflow && aiMobileState.offenderCount === 0, 'CopilotPopup mobile view has horizontal overflow');
    await evaluate(client, `([...document.querySelectorAll('.copilotKitPopup button')].find((element) => element.offsetParent !== null && element.getAttribute('aria-label') === 'Close'))?.click()`);
    await waitFor(client, `(() => { if (document.querySelector('.copilotKitPopup')) return false; const button = document.querySelector('.hvac-copilot-toggle'); if (!button) return false; const rect = button.getBoundingClientRect(); const style = getComputedStyle(button); return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden' && style.pointerEvents !== 'none'; })()`, 'CopilotPopup mobile close');
    await setViewport(client, VIEWPORTS[0]);
    interactionChecks.push('ai-mobile-popup');

    context = 'interaction:ai-copilot-chat-workspace';
    await hardNavigate(client, '/ai');
    await waitForRoute(client, ROUTES.find((route) => route.path === '/ai'), true);
    await waitFor(client, `Boolean(document.querySelector('.ai-copilot-chat .copilotKitChat, .ai-copilot-chat.copilotKitChat'))`, 'CopilotChat workspace mount');
    const aiWorkspaceInitialState = await evaluate(client, `(() => {
      const workspace = document.querySelector('.ai-copilot-chat-shell');
      const textarea = workspace?.querySelector('textarea');
      const content = document.querySelector('.app-content-ai');
      const page = document.querySelector('.ai-ops-workspace');
      const hub = document.querySelector('.ai-hub');
      const surface = document.querySelector('.ai-hub-body');
      const threadSidebar = document.querySelector('.ai-thread-sidebar');
      const conversation = document.querySelector('.ai-conversation-pane');
      const evidenceSidebar = document.querySelector('.ai-evidence-sidebar');
      const composer = workspace?.querySelector('.copilotKitInput');
      const composerRect = composer?.getBoundingClientRect();
      const composerStyle = composer ? getComputedStyle(composer) : null;
      const sendButton = composer?.querySelector('[data-testid="copilot-send-button"]');
      const sendRect = sendButton?.getBoundingClientRect();
      const addButton = composer?.querySelector('[data-testid="copilot-add-menu-button"]');
      const addRect = addButton?.getBoundingClientRect();
      const welcomeInput = workspace?.querySelector('.hvac-copilot-welcome-input');
      const welcomeIntro = workspace?.querySelector('.hvac-copilot-workspace-intro');
      const suggestionSection = workspace?.querySelector('.hvac-copilot-suggestion-section');
      const contentRect = content?.getBoundingClientRect();
      const pageRect = page?.getBoundingClientRect();
      const conversationRect = conversation?.getBoundingClientRect();
      const composerDockRect = welcomeInput?.getBoundingClientRect() ?? composerRect;
      const customText = [...(hub?.querySelectorAll([
        '.ai-thread-copy p',
        '.ai-thread-meta',
        '.ai-thread-footer',
        '.ai-conversation-header span',
        '.ai-conversation-header p',
        '.ai-evidence-section > header',
        '.ai-evidence-section > p',
        '.ai-runtime-readings span',
        '.ai-evidence-list small',
        '.ai-governance-bar',
      ].join(',')) ?? [])]
        .filter((element) => element.textContent?.trim() && getComputedStyle(element).display !== 'none')
        .map((element) => Number.parseFloat(getComputedStyle(element).fontSize));
      const chatScrollContainers = [...document.querySelectorAll('.ai-copilot-chat *')]
        .filter((element) => ['auto', 'scroll'].includes(getComputedStyle(element).overflowY));
      const outsideScrollers = [...(hub?.querySelectorAll('*') ?? [])].filter((element) => {
        if (conversation?.contains(element)) return false;
        const style = getComputedStyle(element);
        return ['auto', 'scroll'].includes(style.overflowY) && element.scrollHeight > element.clientHeight + 1;
      });
      return {
        workspace: Boolean(workspace),
        textarea: Boolean(textarea),
        hasPersistentDisclaimer: workspace?.textContent?.includes('仅用于分析与建议；设备控制及业务写入需人工确认。') ?? false,
        popupLauncher: Boolean(document.querySelector('.hvac-copilot-toggle')),
        popup: Boolean(document.querySelector('.copilotKitPopup')),
        threadRows: document.querySelectorAll('.ai-thread-list .ai-thread-row').length,
        hasThreadSearch: Boolean(document.querySelector('.ai-thread-search input')),
        hasEvidenceRail: Boolean(document.querySelector('.ai-evidence-sidebar')),
        hasDecorativePageHeader: Boolean(document.querySelector('.ai-ops-workspace .ops-page-header, .ai-page-status')),
        fillsContentWidth: Boolean(contentRect && pageRect && Math.abs(contentRect.width - pageRect.width) <= 1),
        fillsContentHeight: Boolean(contentRect && pageRect && Math.abs(contentRect.height - pageRect.height) <= 1),
        alignsContentLeft: Boolean(contentRect && pageRect && Math.abs(contentRect.left - pageRect.left) <= 1),
        alignsContentTop: Boolean(contentRect && pageRect && Math.abs(contentRect.top - pageRect.top) <= 1),
        surfaceRadius: surface ? Number.parseFloat(getComputedStyle(surface).borderRadius) : -1,
        surfaceGap: surface ? getComputedStyle(surface).columnGap : '',
        surfaceBorderWidth: surface ? Number.parseFloat(getComputedStyle(surface).borderTopWidth) : -1,
        threadRadius: threadSidebar ? Number.parseFloat(getComputedStyle(threadSidebar).borderRadius) : -1,
        conversationRadius: conversation ? Number.parseFloat(getComputedStyle(conversation).borderRadius) : -1,
        evidenceRadius: evidenceSidebar ? Number.parseFloat(getComputedStyle(evidenceSidebar).borderRadius) : -1,
        composerHeight: composerRect?.height ?? 0,
        composerWidth: composerRect?.width ?? 0,
        composerRadius: Number.parseFloat(composerStyle?.borderRadius || '0'),
        composerBorderWidth: Number.parseFloat(composerStyle?.borderTopWidth || '0'),
        composerBorderColor: composerStyle?.borderTopColor ?? '',
        composerShadow: composerStyle?.boxShadow ?? '',
        composerCenterOffset: composerRect && conversationRect
          ? Math.abs((composerRect.left + composerRect.width / 2) - (conversationRect.left + conversationRect.width / 2))
          : 999,
        composerSideInset: composerRect && conversationRect
          ? Math.min(composerRect.left - conversationRect.left, conversationRect.right - composerRect.right)
          : -1,
        composerBottomInset: composerDockRect && conversationRect
          ? conversationRect.bottom - composerDockRect.bottom
          : 999,
        sendWidth: sendRect?.width ?? 0,
        sendHeight: sendRect?.height ?? 0,
        disabledAddHidden: !addButton || !addButton.disabled || (addRect?.width ?? 0) === 0,
        welcomeOrderValid: !welcomeInput || !suggestionSection
          || welcomeInput.getBoundingClientRect().top > suggestionSection.getBoundingClientRect().bottom,
        welcomeIntroScrollable: !welcomeIntro || ['auto', 'scroll'].includes(getComputedStyle(welcomeIntro).overflowY),
        threadWidth: threadSidebar?.getBoundingClientRect().width ?? 0,
        conversationWidth: conversation?.getBoundingClientRect().width ?? 0,
        evidenceWidth: evidenceSidebar?.getBoundingClientRect().width ?? 0,
        minCustomFontSize: customText.length ? Math.min(...customText) : 0,
        pageScroll: document.scrollingElement.scrollHeight - window.innerHeight,
        contentScroll: content ? content.scrollHeight - content.clientHeight : 999,
        hubScroll: hub ? hub.scrollHeight - hub.clientHeight : 999,
        contentOverflowY: content ? getComputedStyle(content).overflowY : '',
        hubOverflowY: hub ? getComputedStyle(hub).overflowY : '',
        chatScrollContainerCount: chatScrollContainers.length,
        outsideScrollerCount: outsideScrollers.length,
      };
    })()`);
    assert(
      aiWorkspaceInitialState.workspace
        && aiWorkspaceInitialState.textarea
        && !aiWorkspaceInitialState.hasPersistentDisclaimer
        && !aiWorkspaceInitialState.popupLauncher
        && !aiWorkspaceInitialState.popup
        && aiWorkspaceInitialState.threadRows >= 3
        && aiWorkspaceInitialState.hasThreadSearch
        && aiWorkspaceInitialState.hasEvidenceRail
        && !aiWorkspaceInitialState.hasDecorativePageHeader
        && aiWorkspaceInitialState.fillsContentWidth
        && aiWorkspaceInitialState.fillsContentHeight
        && aiWorkspaceInitialState.alignsContentLeft
        && aiWorkspaceInitialState.alignsContentTop
        && aiWorkspaceInitialState.surfaceRadius === 0
        && aiWorkspaceInitialState.surfaceGap === '0px'
        && aiWorkspaceInitialState.surfaceBorderWidth === 0
        && aiWorkspaceInitialState.threadRadius === 0
        && aiWorkspaceInitialState.conversationRadius === 0
        && aiWorkspaceInitialState.evidenceRadius === 0
        && aiWorkspaceInitialState.composerHeight >= 74
        && aiWorkspaceInitialState.composerHeight <= 82
        && aiWorkspaceInitialState.composerWidth <= 760
        && aiWorkspaceInitialState.composerRadius >= 16
        && aiWorkspaceInitialState.composerRadius <= 20
        && aiWorkspaceInitialState.composerBorderWidth >= 1
        && aiWorkspaceInitialState.composerShadow !== 'none'
        && aiWorkspaceInitialState.composerCenterOffset <= 2
        && aiWorkspaceInitialState.composerSideInset >= 16
        && aiWorkspaceInitialState.composerBottomInset >= 0
        && aiWorkspaceInitialState.composerBottomInset <= 36
        && aiWorkspaceInitialState.sendWidth >= 41
        && aiWorkspaceInitialState.sendHeight >= 41
        && aiWorkspaceInitialState.disabledAddHidden
        && aiWorkspaceInitialState.welcomeOrderValid
        && aiWorkspaceInitialState.welcomeIntroScrollable
        && aiWorkspaceInitialState.conversationWidth > aiWorkspaceInitialState.threadWidth * 1.5
        && aiWorkspaceInitialState.conversationWidth > aiWorkspaceInitialState.evidenceWidth * 1.5
        && aiWorkspaceInitialState.minCustomFontSize >= 11
        && aiWorkspaceInitialState.pageScroll <= 2
        && aiWorkspaceInitialState.contentScroll <= 2
        && aiWorkspaceInitialState.hubScroll <= 2
        && ['hidden', 'clip'].includes(aiWorkspaceInitialState.contentOverflowY)
        && ['hidden', 'clip'].includes(aiWorkspaceInitialState.hubOverflowY)
        && aiWorkspaceInitialState.chatScrollContainerCount >= 1
        && aiWorkspaceInitialState.outsideScrollerCount === 0,
      `CopilotChat workspace shell is invalid: ${JSON.stringify(aiWorkspaceInitialState)}`,
    );

    await waitFor(client, `(() => {
      const textarea = document.querySelector('.ai-copilot-chat .copilotKitInput textarea');
      const composer = document.querySelector('.ai-copilot-chat .copilotKitInput');
      if (!textarea || !composer) return false;
      textarea.focus();
      const style = getComputedStyle(composer);
      return document.activeElement === textarea
        && style.borderTopColor !== ${JSON.stringify(aiWorkspaceInitialState.composerBorderColor)}
        && style.boxShadow !== ${JSON.stringify(aiWorkspaceInitialState.composerShadow)};
    })()`, 'AI workspace composer focus feedback');
    const aiWorkspaceFocusState = await evaluate(client, `(() => {
      const composer = document.querySelector('.ai-copilot-chat .copilotKitInput');
      const style = composer ? getComputedStyle(composer) : null;
      return {
        borderColor: style?.borderTopColor ?? '',
        shadow: style?.boxShadow ?? '',
      };
    })()`);
    assert(
      aiWorkspaceFocusState.borderColor !== aiWorkspaceInitialState.composerBorderColor
        && aiWorkspaceFocusState.shadow !== aiWorkspaceInitialState.composerShadow,
      `CopilotChat workspace composer focus feedback is missing: ${JSON.stringify(aiWorkspaceFocusState)}`,
    );

    const aiHistorySearchFocused = await evaluate(client, `(() => {
      const input = document.querySelector('.ai-thread-search input');
      if (!input) return false;
      input.focus();
      return document.activeElement === input;
    })()`);
    assert(aiHistorySearchFocused, 'AI workspace history search was unavailable');
    await client.send('Input.insertText', { text: 'CH-02' });
    await pause(400);
    const aiHistorySearchState = await evaluate(client, `(() => {
      const input = document.querySelector('.ai-thread-search input');
      const rows = [...document.querySelectorAll('.ai-thread-list .ai-thread-row')];
      return {
        value: input?.value ?? '',
        rows: rows.map((element) => element.textContent?.trim() ?? ''),
      };
    })()`);
    assert(
      aiHistorySearchState.value === 'CH-02'
        && aiHistorySearchState.rows.length >= 1
        && aiHistorySearchState.rows.every((text) => text.includes('CH-02'))
        && aiHistorySearchState.rows.some((text) => text.includes('CH-02 COP 下降调查')),
      `AI workspace history search is invalid: ${JSON.stringify(aiHistorySearchState)}`,
    );
    const aiHistoryThreadOpened = await evaluate(client, `(() => {
      const row = [...document.querySelectorAll('.ai-thread-list .ai-thread-row')]
        .find((element) => element.textContent.includes('CH-02 COP 下降调查'));
      if (!row) return false;
      row.click();
      return true;
    })()`);
    assert(aiHistoryThreadOpened, 'AI workspace historical investigation was unavailable');
    await waitFor(client, `(() => {
      const workspace = document.querySelector('.ai-copilot-chat');
      return workspace?.textContent.includes('冷凝温差持续偏高')
        && document.querySelector('.ai-conversation-header h2')?.textContent.includes('CH-02 COP 下降调查');
    })()`, 'AI workspace historical investigation restore');
    interactionChecks.push('ai-workspace-history');

    const aiWorkspaceNewThread = await evaluate(client, `(() => {
      const button = [...document.querySelectorAll('.ai-conversation-actions button')].find((element) => element.textContent.includes('新建调查'));
      if (!button) return false;
      button.click();
      return true;
    })()`);
    assert(aiWorkspaceNewThread, 'CopilotChat workspace new-thread action was unavailable');
    await waitFor(client, `Boolean(document.querySelector('.ai-copilot-chat .hvac-copilot-welcome')) && !document.querySelector('.ai-copilot-chat .hvac-agent-result-card')`, 'CopilotChat workspace welcome state');
    const aiWorkspaceWelcomeState = await evaluate(client, `(() => {
      const conversation = document.querySelector('.ai-conversation-pane');
      const intro = document.querySelector('.ai-copilot-chat .hvac-copilot-workspace-intro');
      const composerDock = document.querySelector('.ai-copilot-chat .hvac-copilot-welcome-input');
      const composer = composerDock?.querySelector('.copilotKitInput');
      const suggestions = document.querySelector('.ai-copilot-chat .hvac-copilot-suggestion-section');
      const suggestionItems = [...document.querySelectorAll('.ai-copilot-chat .hvac-copilot-suggestion')];
      const conversationRect = conversation?.getBoundingClientRect();
      const introRect = intro?.getBoundingClientRect();
      const dockRect = composerDock?.getBoundingClientRect();
      const suggestionRect = suggestions?.getBoundingClientRect();
      const suggestionItemStates = suggestionItems.map((element) => {
        const rect = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        return {
          left: rect.left,
          top: rect.top,
          width: rect.width,
          height: rect.height,
          radius: Number.parseFloat(style.borderRadius || '0'),
          background: style.backgroundColor,
          whiteSpace: style.whiteSpace,
          arrow: getComputedStyle(element, '::after').content,
        };
      });
      const evidence = document.querySelector('.ai-evidence-rail-content');
      return {
        headerTitle: document.querySelector('.ai-conversation-header h2')?.textContent?.trim() ?? '',
        hasComposerLabel: Boolean(document.querySelector('.hvac-copilot-composer-label')?.textContent?.includes('开始调查')),
        hasRedundantWelcomeCopy: ['从当前运行数据开始调查', '可以这样开始', '分析范围']
          .some((text) => document.querySelector('.ai-conversation-pane')?.textContent?.includes(text)),
        hasPersistentDisclaimer: [...document.querySelectorAll('.ai-copilot-chat *')].some((element) => (
          element.childElementCount === 0
          && element.textContent?.includes('AI can make mistakes')
          && element.offsetParent !== null
          && getComputedStyle(element).display !== 'none'
        )),
        composerBelowSuggestions: Boolean(dockRect && suggestionRect && dockRect.top > suggestionRect.bottom),
        composerBottomInset: dockRect && conversationRect ? conversationRect.bottom - dockRect.bottom : 999,
        composerHeight: composer?.getBoundingClientRect().height ?? 0,
        contentAxisAligned: Boolean(introRect && dockRect
          && Math.abs(introRect.left - dockRect.left) <= 2
          && Math.abs(introRect.right - dockRect.right) <= 2),
        suggestionCount: suggestionItemStates.length,
        suggestionsVerticallyStacked: suggestionItemStates.every((item, index) => index === 0 || item.top > suggestionItemStates[index - 1].top + 4),
        suggestionsLeftAligned: suggestionItemStates.every((item) => Math.abs(item.left - (introRect?.left ?? item.left)) <= 2),
        suggestionsUseContentWidth: suggestionItemStates.every((item) => !suggestionRect || item.width < suggestionRect.width - 24),
        suggestionGeometryValid: suggestionItemStates.every((item) => item.height >= 38 && item.height <= 44 && item.radius >= 10),
        suggestionSurfaceValid: suggestionItemStates.every((item) => item.background !== 'rgba(0, 0, 0, 0)' && item.background !== 'transparent'),
        suggestionTextValid: suggestionItemStates.every((item) => item.whiteSpace === 'nowrap' && item.arrow.includes('→')),
        introScrollable: ['auto', 'scroll'].includes(getComputedStyle(document.querySelector('.hvac-copilot-workspace-intro')).overflowY),
        compactEvidence: evidence?.classList.contains('is-empty') ?? false,
        hasContext: Boolean(evidence?.querySelector('.ai-context-summary')),
        hasAvailableData: Boolean(evidence?.querySelector('.ai-available-data-section')),
        hasRuntimeSnapshot: Boolean(evidence?.querySelector('.ai-runtime-readings')),
        hasHandoffs: Boolean(evidence?.querySelector('.ai-handoff-section')),
        hasDesktopNewAction: [...document.querySelectorAll('.ai-conversation-actions button')].some((element) => element.textContent.includes('新建调查')),
      };
    })()`);
    assert(
      aiWorkspaceWelcomeState.headerTitle === 'AI 运维助手'
        && !aiWorkspaceWelcomeState.hasComposerLabel
        && !aiWorkspaceWelcomeState.hasRedundantWelcomeCopy
        && !aiWorkspaceWelcomeState.hasPersistentDisclaimer
        && aiWorkspaceWelcomeState.composerBelowSuggestions
        && aiWorkspaceWelcomeState.composerBottomInset >= 0
        && aiWorkspaceWelcomeState.composerBottomInset <= 24
        && aiWorkspaceWelcomeState.composerHeight >= 74
        && aiWorkspaceWelcomeState.contentAxisAligned
        && aiWorkspaceWelcomeState.suggestionCount >= 3
        && aiWorkspaceWelcomeState.suggestionsVerticallyStacked
        && aiWorkspaceWelcomeState.suggestionsLeftAligned
        && aiWorkspaceWelcomeState.suggestionsUseContentWidth
        && aiWorkspaceWelcomeState.suggestionGeometryValid
        && aiWorkspaceWelcomeState.suggestionSurfaceValid
        && aiWorkspaceWelcomeState.suggestionTextValid
        && aiWorkspaceWelcomeState.introScrollable
        && aiWorkspaceWelcomeState.compactEvidence
        && aiWorkspaceWelcomeState.hasContext
        && aiWorkspaceWelcomeState.hasAvailableData
        && !aiWorkspaceWelcomeState.hasRuntimeSnapshot
        && !aiWorkspaceWelcomeState.hasHandoffs
        && !aiWorkspaceWelcomeState.hasDesktopNewAction,
      `CopilotChat workspace empty-state hierarchy is invalid: ${JSON.stringify(aiWorkspaceWelcomeState)}`,
    );
    const aiWorkspaceQuestionEntered = await evaluate(client, `(() => {
      const textarea = document.querySelector('.ai-copilot-chat textarea');
      if (!textarea) return false;
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set;
      setter?.call(textarea, '为什么当前能耗升高？');
      textarea.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: '为什么当前能耗升高？' }));
      textarea.dispatchEvent(new Event('change', { bubbles: true }));
      return true;
    })()`);
    assert(aiWorkspaceQuestionEntered, 'CopilotChat workspace input was unavailable');
    await pause(100);
    const aiWorkspaceSendClicked = await evaluate(client, `(() => {
      const input = document.querySelector('.ai-copilot-chat .copilotKitInput');
      const buttons = [...(input?.querySelectorAll('button') ?? [])].filter((element) => element.offsetParent !== null && !element.disabled);
      const send = buttons.at(-1);
      if (!send) return false;
      send.click();
      return true;
    })()`);
    assert(aiWorkspaceSendClicked, 'CopilotChat workspace send button was unavailable');
    await waitFor(client, `(() => {
      const workspace = document.querySelector('.ai-copilot-chat');
      const card = workspace?.querySelector('.hvac-agent-result-card');
      return workspace?.textContent.includes('总功率约')
        && card?.textContent.includes('能耗异常调查')
        && card?.textContent.includes('额外能耗');
    })()`, 'CopilotChat workspace Agent answer');
    const aiWorkspaceStartedState = await evaluate(client, `(() => {
      const conversation = document.querySelector('.ai-conversation-pane');
      const composer = document.querySelector('.ai-copilot-chat .copilotKitInput');
      const messageAxis = document.querySelector('.ai-copilot-chat [class*="max-w-3xl"]:has(.copilotKitMessages)');
      const conversationRect = conversation?.getBoundingClientRect();
      const composerRect = composer?.getBoundingClientRect();
      const messageAxisRect = messageAxis?.getBoundingClientRect();
      const evidence = document.querySelector('.ai-evidence-rail-content');
      return {
        composerNearBottom: Boolean(conversationRect && composerRect && composerRect.top > conversationRect.top + conversationRect.height * 0.7),
        messageAxisAligned: Boolean(messageAxisRect && composerRect
          && Math.abs((messageAxisRect.left + messageAxisRect.width / 2) - (composerRect.left + composerRect.width / 2)) <= 2
          && messageAxisRect.width <= 760),
        evidenceExpanded: Boolean(evidence && !evidence.classList.contains('is-empty')),
        hasRuntimeSnapshot: Boolean(evidence?.querySelector('.ai-runtime-readings')),
        hasHandoffs: Boolean(evidence?.querySelector('.ai-handoff-section')),
        hasNewAction: [...document.querySelectorAll('.ai-conversation-actions button')].some((element) => element.textContent.includes('新建调查')),
      };
    })()`);
    assert(
      aiWorkspaceStartedState.composerNearBottom
        && aiWorkspaceStartedState.messageAxisAligned
        && aiWorkspaceStartedState.evidenceExpanded
        && aiWorkspaceStartedState.hasRuntimeSnapshot
        && aiWorkspaceStartedState.hasHandoffs
        && aiWorkspaceStartedState.hasNewAction,
      `CopilotChat workspace started-state hierarchy is invalid: ${JSON.stringify(aiWorkspaceStartedState)}`,
    );
    await waitFor(client, `(() => {
      const raw = localStorage.getItem('hvac-ai-thread-history-v1');
      return Boolean(raw?.includes('为什么当前能耗升高？') && raw.includes('总功率约'));
    })()`, 'AI workspace history persistence');
    await client.send('Page.reload', { ignoreCache: true });
    await pause(1800);
    const aiPersistedRestoreState = await evaluate(client, `(() => {
      const workspace = document.querySelector('.ai-copilot-chat');
      const raw = localStorage.getItem('hvac-ai-thread-history-v1');
      return {
        text: workspace?.textContent?.slice(0, 600) ?? '',
        header: document.querySelector('.ai-conversation-header h2')?.textContent ?? '',
        threadRows: [...document.querySelectorAll('.ai-thread-row')].map((element) => element.textContent?.slice(0, 80) ?? ''),
        storage: raw?.slice(0, 1200) ?? '',
        pageFixed: document.querySelector('.app-content-ai')?.scrollHeight <= document.querySelector('.app-content-ai')?.clientHeight + 2,
      };
    })()`);
    assert(
      aiPersistedRestoreState.header === '为什么当前能耗升高？'
        && aiPersistedRestoreState.storage.includes('总功率约')
        && aiPersistedRestoreState.threadRows.some((row) => row.includes('为什么当前能耗升高？'))
        && aiPersistedRestoreState.pageFixed,
      `AI workspace persisted thread restore is invalid: ${JSON.stringify(aiPersistedRestoreState)}`,
    );
    const aiWorkspaceOverflow = await evaluate(client, `(() => {
      const workspace = document.querySelector('.ai-copilot-chat-shell');
      const offenders = [...(workspace?.querySelectorAll('*') ?? [])].filter((element) => {
        if (element.offsetParent === null) return false;
        const overflowX = getComputedStyle(element).overflowX;
        return element.scrollWidth > element.clientWidth + 1 && overflowX !== 'hidden' && overflowX !== 'clip';
      });
      return {
        body: document.body.scrollWidth > window.innerWidth + 2,
        shell: workspace ? workspace.scrollWidth > workspace.clientWidth + 1 : true,
        offenders: offenders.length,
      };
    })()`);
    assert(
      !aiWorkspaceOverflow.body && !aiWorkspaceOverflow.shell && aiWorkspaceOverflow.offenders === 0,
      `CopilotChat workspace has horizontal overflow: ${JSON.stringify(aiWorkspaceOverflow)}`,
    );
    await setViewport(client, VIEWPORTS[2]);
    await hardNavigate(client, '/ai');
    await waitForRoute(client, ROUTES.find((route) => route.path === '/ai'), true);
    await waitFor(client, `Boolean(document.querySelector('.ai-copilot-chat-shell textarea'))`, 'CopilotChat mobile workspace');
    const aiWorkspaceMobileState = await evaluate(client, `(() => {
      const shell = document.querySelector('.ai-copilot-chat-shell');
      const rect = shell?.getBoundingClientRect();
      const offenders = [...(shell?.querySelectorAll('*') ?? [])].filter((element) => {
        if (element.offsetParent === null) return false;
        const overflowX = getComputedStyle(element).overflowX;
        return element.scrollWidth > element.clientWidth + 1 && overflowX !== 'hidden' && overflowX !== 'clip';
      });
      return {
        left: rect?.left ?? -1,
        right: rect?.right ?? -1,
        viewport: window.innerWidth,
        body: document.body.scrollWidth > window.innerWidth + 2,
        shell: shell ? shell.scrollWidth > shell.clientWidth + 1 : true,
        offenders: offenders.length,
      };
    })()`);
    assert(
      aiWorkspaceMobileState.left >= -1
        && aiWorkspaceMobileState.right <= aiWorkspaceMobileState.viewport + 1
        && !aiWorkspaceMobileState.body
        && !aiWorkspaceMobileState.shell
        && aiWorkspaceMobileState.offenders === 0,
      `CopilotChat mobile workspace is invalid: ${JSON.stringify(aiWorkspaceMobileState)}`,
    );
    await setViewport(client, VIEWPORTS[0]);
    interactionChecks.push('ai-copilot-chat-workspace');

    context = 'interaction:deep-link-refresh';
    await hardNavigate(client, '/assets?device=b1-z1-u1');
    await waitFor(client, `document.querySelector('.ops-detail-drawer.ant-drawer-open')?.textContent.includes('b1-z1-u1')`, 'asset deep link drawer');
    await client.send('Page.reload', { ignoreCache: true });
    await waitFor(client, `document.querySelector('.ops-detail-drawer.ant-drawer-open')?.textContent.includes('b1-z1-u1')`, 'asset deep link reload');
    interactionChecks.push('deep-link-refresh');

    context = 'interaction:drawer-escape';
    await evaluate(client, `(() => {
      const popup = document.querySelector('.copilotKitPopup');
      if (!popup) return false;
      const close = [...popup.querySelectorAll('button')].find((element) => element.getAttribute('aria-label') === 'Close');
      close?.click();
      return Boolean(close);
    })()`);
    await waitFor(client, `!document.querySelector('.copilotKitPopup')`, 'CopilotPopup cleared before drawer Escape');
    await pause(260);
    const drawerCloseButtonFocused = await evaluate(client, `(() => {
      const button = document.querySelector('.ops-detail-drawer.ant-drawer-open .ant-drawer-close');
      button?.focus();
      return document.activeElement === button;
    })()`);
    assert(drawerCloseButtonFocused, 'Asset drawer close control could not receive focus');
    await pressEscape(client);
    await pause(500);
    const drawerEscapeState = await evaluate(client, `({
      drawerOpen: Boolean(document.querySelector('.ops-detail-drawer.ant-drawer-open')),
      hasDeviceParam: new URLSearchParams(location.search).has('device'),
      popupOpen: Boolean(document.querySelector('.copilotKitPopup')),
      activeElement: document.activeElement?.className ?? document.activeElement?.tagName ?? '',
    })`);
    assert(
      !drawerEscapeState.drawerOpen && !drawerEscapeState.hasDeviceParam,
      `Drawer Escape did not close the asset detail: ${JSON.stringify(drawerEscapeState)}`,
    );
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

    context = 'interaction:energy-first-entry';
    await evaluate(client, `(() => {
      history.pushState({}, '', '/energy');
      window.dispatchEvent(new PopStateEvent('popstate'));
      return true;
    })()`);
    await waitFor(client, `location.pathname === '/energy/month'`, 'energy index redirect');
    await waitFor(
      client,
      `document.body.innerText.includes('月度能耗分析') && document.querySelectorAll('.ops-chart-card').length >= 4 && document.querySelectorAll('.ops-metric').length >= 4`,
      'energy first-entry content',
    );
    const energyFirstEntryState = await evaluate(client, `({
      pathname: location.pathname,
      chartCount: document.querySelectorAll('.ops-chart-card').length,
      metricCount: document.querySelectorAll('.ops-metric').length,
      contentHeight: document.querySelector('.energy-system-root')?.scrollHeight ?? 0,
    })`);
    assert(
      energyFirstEntryState.pathname === '/energy/month'
        && energyFirstEntryState.chartCount >= 4
        && energyFirstEntryState.metricCount >= 4
        && energyFirstEntryState.contentHeight > 900,
      `Energy first entry rendered an incomplete workspace: ${JSON.stringify(energyFirstEntryState)}`,
    );
    interactionChecks.push('energy-first-entry');

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
    const isKnownCopilotKitDevWarning = (detail) => (
      detail.includes('Function components cannot be given refs')
      && detail.includes('DropdownMenuTrigger2')
      && detail.includes('@copilotkit_react-core_v2')
    );
    const knownThirdPartyWarnings = browserProblems.filter((problem) => isKnownCopilotKitDevWarning(String(problem.detail)));
    const actionableProblems = browserProblems.filter((problem) => {
      const detail = String(problem.detail);
      return !detail.includes('ResizeObserver loop')
        && !detail.includes('favicon.ico')
        && !isKnownCopilotKitDevWarning(detail);
    });
    assert(actionableProblems.length === 0, `Browser problems detected: ${JSON.stringify(actionableProblems, null, 2)}`);

    console.log(JSON.stringify({
      passed: accessChecks.length + visualChecks.length + interactionChecks.length,
      accessChecks: accessChecks.length,
      visualChecks: visualChecks.length,
      interactionChecks,
      knownThirdPartyWarnings: knownThirdPartyWarnings.length,
      browserProblems: actionableProblems,
    }, null, 2));
  } finally {
    client.close();
  }
} finally {
  edge.kill();
  await pause(1200);
  await rm(profileDir, {
    recursive: true,
    force: true,
    maxRetries: 8,
    retryDelay: 250,
  });
}

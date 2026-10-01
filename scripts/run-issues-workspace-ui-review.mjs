import { mkdir, rm } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { resolve } from 'node:path';
import { once } from 'node:events';
import { chromium } from '@playwright/test';
import { preview as createVitePreview } from 'vite';

const root = resolve(process.cwd());
const appRoot = resolve(root, 'apps/hvac-web');
const outputRoot = resolve(root, 'out/issues-workspace-ui-review');
const siteId = '01940000-0001-7000-8000-000000000001';

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

async function noHorizontalOverflow(page) {
  return page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);
}

async function run() {
  await rm(outputRoot, { recursive: true, force: true });
  await mkdir(outputRoot, { recursive: true });

  process.env.HVAC_WEB_FRONTEND_REVIEW = 'true';
  const port = await findAvailablePort();
  const vite = await createVitePreview({
    root: appRoot,
    configFile: resolve(appRoot, 'vite.config.ts'),
    logLevel: 'error',
    preview: {
      host: '127.0.0.1',
      port,
      strictPort: true,
    },
  });

  let browser;
  try {
    const base = `http://127.0.0.1:${port}`;
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage({ viewport: { width: 1672, height: 941 } });
    page.setDefaultTimeout(12_000);

    const runtimeErrors = [];
    page.on('pageerror', (error) => runtimeErrors.push(error.message));

    await page.goto(`${base}/sites/${siteId}/issues`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="alarm-center"]').waitFor();
    await page.locator('[data-testid="problem-queue"]').waitFor();

    const navLabels = await page.locator('[data-workspace-id]').allTextContents();
    assert(navLabels.length === 10, `Expected 10 workspace navigation entries, got ${navLabels.length}`);
    assert(await page.locator('[data-workspace-id="issues"][aria-current="page"]').count() === 1, 'Issues workspace is not active.');
    assert((await page.locator('[data-testid="real-shell-surface-identity"]').innerText()).trim() === '告警与诊断', 'Workspace shell title is not 告警与诊断.');

    const taskTabs = page.getByRole('tab');
    const taskLabels = (await taskTabs.allTextContents()).map((text) => text.trim()).filter(Boolean);
    assert(JSON.stringify(taskLabels) === JSON.stringify(['问题处置', '告警分析']), `Task modes drifted: ${JSON.stringify(taskLabels)}`);
    for (const rowFilterLabel of ['当前活动', '全部记录', '已搁置', '诊断']) {
      assert(!taskLabels.includes(rowFilterLabel), `${rowFilterLabel} regressed to a peer task tab.`);
    }

    const queueToggle = page.locator('[data-slot="toggle-group"]');
    const queueToggleText = await queueToggle.innerText();
    assert(queueToggleText.includes('待处理问题') && queueToggleText.includes('原始告警'), 'Problem/raw alarm switch is missing.');
    assert(await page.getByRole('combobox', { name: '记录范围' }).count() === 1, 'Record scope must have one owner.');

    const problemRows = page.locator('[data-testid="problem-queue"] [data-slot="table-body"] [data-slot="table-row"]');
    assert(await problemRows.count() === 2, `Expected 2 active grouped problems, got ${await problemRows.count()}`);
    const problemHeaders = (await page.locator('[data-testid="problem-queue"] [data-slot="table-head"]').allTextContents())
      .map((text) => text.trim())
      .filter(Boolean);
    for (const header of ['问题', '关联告警', '处理进度', '影响', '负责人', '下一步', '最近变化']) {
      assert(problemHeaders.includes(header), `Problem queue lost column ${header}: ${JSON.stringify(problemHeaders)}`);
    }

    const centerText = await page.locator('[data-testid="alarm-center"]').innerText();
    for (const text of ['待处理问题', '高影响', '冷冻水输配能力异常', '冷却侧换热性能异常', '2 条', '下一步']) {
      assert(centerText.includes(text), `Problem-first queue lost useful operator information: ${text}`);
    }
    for (const jargon of ['IssueQueue', 'Projection', 'Hypothesis', 'Root Cause']) {
      assert(!centerText.includes(jargon), `Internal jargon leaked into operator UI: ${jargon}`);
    }

    assert(!new URL(page.url()).searchParams.has('selected'), 'Desktop Issues workspace should not auto-open detail.');
    assert(await page.locator('[data-slot="sheet-content"]').count() === 0, 'Desktop Issues workspace opened a Sheet before problem selection.');

    const problemQueueWidthBefore = Math.round((await page.locator('[data-testid="problem-queue"]').boundingBox())?.width ?? 0);
    await page.getByRole('row', { name: /冷却侧换热性能异常/ }).click();
    await page.waitForURL((url) => Boolean(url.searchParams.get('selected')) && Boolean(url.searchParams.get('selectedIssue')));

    const sheet = page.locator('[data-slot="sheet-content"]');
    await sheet.waitFor();
    await page.locator('[data-testid="alarm-diagnosis-panel"]').waitFor();
    const sheetWidth = Math.round((await sheet.boundingBox())?.width ?? 0);
    const problemQueueWidthAfter = Math.round((await page.locator('[data-testid="problem-queue"]').boundingBox())?.width ?? 0);
    assert(sheetWidth >= 480 && sheetWidth <= 640, `Desktop Detail Sheet width drifted: ${sheetWidth}`);
    assert(await page.locator('[data-slot="sheet-overlay"]').count() === 0, 'Desktop Detail Sheet must be non-modal/no-overlay.');
    assert(problemQueueWidthAfter === problemQueueWidthBefore, `Opening Detail Sheet changed queue width: ${problemQueueWidthBefore} -> ${problemQueueWidthAfter}`);

    const selectedSheetText = await sheet.innerText();
    for (const required of [
      '冷却侧换热性能异常',
      '2 条相关告警一起处理',
      '1# 冷水机组 COP 持续偏低',
      '1# 冷却塔逼近温度偏高',
      '下一步',
      '当前告警',
      '诊断与原因排查',
      '诊断结果',
      '影响',
      '可能原因',
      '支持证据',
      '原因结论',
      '验证情况',
      '证据',
      '状态与处置时间线',
    ]) {
      assert(selectedSheetText.includes(required), `Problem detail lost useful information: ${required}`);
    }
    for (const jargon of ['Finding', '调查假设', '验证闭环', 'Root Cause', 'Hypothesis']) {
      assert(!selectedSheetText.includes(jargon), `Problem detail leaked design jargon: ${jargon}`);
    }

    const groupedAlarmButton = sheet.getByRole('button', { name: /1# 冷却塔逼近温度偏高 活动中 · 已确认/ });
    await groupedAlarmButton.click();
    await page.waitForURL((url) => Boolean(url.searchParams.get('selected')) && Boolean(url.searchParams.get('selectedIssue')));
    assert((await sheet.innerText()).includes('2 条相关告警一起处理'), 'Switching a related alarm lost the grouped problem context.');

    assert(await noHorizontalOverflow(page), 'Desktop Issues workspace has page-level horizontal overflow.');
    await page.screenshot({ path: resolve(outputRoot, 'issues-problem-first-desktop.png'), fullPage: true, timeout: 30_000 });

    await sheet.locator('[data-slot="sheet-close"]').click();
    await page.waitForFunction(() => !new URL(location.href).searchParams.has('selected'));

    await page.locator('[data-slot="toggle-group-item"]', { hasText: '原始告警' }).click();
    await page.waitForURL((url) => url.searchParams.get('queueMode') === 'alarms');
    const alarmLedger = page.locator('[data-testid="alarm-triage-ledger"]');
    await alarmLedger.waitFor();
    await page.waitForFunction(() => (
      document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="table-body"] [data-slot="table-row"]').length >= 3
    ));
    const rawHeaders = (await alarmLedger.locator('[data-slot="table-head"]').allTextContents()).map((text) => text.trim()).filter(Boolean);
    for (const header of ['等级', '告警 / 来源', '物理状态', '确认', '诊断']) {
      assert(rawHeaders.includes(header), `Raw Alarm ledger lost ${header}`);
    }

    await page.getByRole('combobox', { name: '记录范围' }).click();
    await page.getByRole('option', { name: '全部记录' }).click();
    await page.waitForURL((url) => url.searchParams.get('alarmView') === 'history');
    await page.waitForFunction(() => (
      document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="table-body"] [data-slot="table-row"]').length >= 4
    ));

    await page.getByRole('tab', { name: '告警分析' }).click();
    await page.waitForURL((url) => url.searchParams.get('alarmView') === 'performance');
    const performance = page.locator('[data-testid="alarm-performance-view"]');
    await performance.waitFor();
    await page.locator('[data-slot="heatmap-chart"]').waitFor();
    const performanceText = await performance.innerText();
    for (const required of ['触发次数', '平均确认', '平均恢复', '诊断覆盖', '告警集中爆发', '可避免能耗', '告警高发时段', '高频贡献对象', '重复问题']) {
      assert(performanceText.includes(required), `Alarm analysis lost required information: ${required}`);
    }
    assert(!performanceText.includes('Alarm Flood'), 'Alarm analysis leaked unnecessary English jargon.');
    assert(await page.locator('[data-slot="bar-list"]').count() >= 2, 'Alarm analysis lost ranked contributor/recurring issue lists.');
    assert(await noHorizontalOverflow(page), 'Alarm analysis has page-level horizontal overflow.');
    await page.screenshot({ path: resolve(outputRoot, 'issues-analysis-desktop.png'), fullPage: true, timeout: 30_000 });

    await page.setViewportSize({ width: 768, height: 900 });
    await page.goto(`${base}/sites/${siteId}/issues`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="problem-queue"]').waitFor();
    assert(!new URL(page.url()).searchParams.has('selected'), 'Narrow Issues workspace should not auto-open detail.');

    await page.getByRole('row', { name: /冷却侧换热性能异常/ }).click();
    await page.locator('[data-slot="sheet-content"]').waitFor();
    const narrowSheetText = await page.locator('[data-slot="sheet-content"]').innerText();
    for (const required of ['2 条相关告警一起处理', '诊断与原因排查', '影响', '可能原因', '验证情况']) {
      assert(narrowSheetText.includes(required), `Narrow problem Sheet lost context: ${required}`);
    }
    assert(await page.locator('[data-slot="sheet-overlay"]').count() === 1, 'Narrow Detail Sheet must use modal overlay.');
    assert(await noHorizontalOverflow(page), 'Narrow Issues workspace has page-level horizontal overflow.');
    await page.screenshot({ path: resolve(outputRoot, 'issues-problem-first-narrow.png'), fullPage: true, timeout: 30_000 });

    assert(runtimeErrors.length === 0, `Runtime errors: ${runtimeErrors.join(' | ')}`);

    console.log(JSON.stringify({
      ok: true,
      workspace: 'issues',
      route: `/sites/${siteId}/issues`,
      model: 'problem-first-operations-analysis',
      navCount: navLabels.length,
      activeProblems: 2,
      groupedSignals: 3,
      runtimeErrors: runtimeErrors.length,
      outputRoot,
    }, null, 2));
  } finally {
    await browser?.close();
    await vite.close();
  }
}

await run();
process.exit(0);

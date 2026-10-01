import { mkdir, rm } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { resolve } from 'node:path';
import { once } from 'node:events';
import { chromium } from '@playwright/test';
import { preview as createVitePreview } from 'vite';

const root = resolve(process.cwd());
const appRoot = resolve(root, 'apps/hvac-web');
const outputRoot = resolve(root, 'out/operations-workspace-ui-review');
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

    await page.goto(`${base}/sites/${siteId}/operations`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="operations-workspace"]').waitFor();

    const navLabels = await page.locator('[data-workspace-id]').allTextContents();
    assert(navLabels.length === 10, `Expected 10 workspace navigation entries, got ${navLabels.length}`);
    assert(await page.locator('[data-workspace-id="operations"][aria-current="page"]').count() === 1, 'Operations workspace is not active.');
    assert((await page.locator('[data-testid="real-shell-surface-identity"]').innerText()).trim() === '运行', 'Workspace shell title is not 运行.');
    assert(JSON.stringify(await page.locator('[aria-label="运行工作区视图"] button').allTextContents()) === JSON.stringify(['系统运行', '空间与环境']), 'Operations peer views are not correct.');
    assert(await page.locator('[data-testid="system-operations"] [data-slot="card"]').count() === 0, 'System Operations regressed to Card dashboard composition.');
    assert(await page.locator('[data-testid="system-operations"] section[aria-label="当前运行事实"]').count() === 1, 'Operational FactStrip is missing.');

    const systemText = await page.locator('[data-testid="system-operations"]').innerText();
    for (const forbidden of ['群控自动优化模式', '自动控制运行中', '设计区间正常', '循环水流稳定', '自动联动控制', '离心/螺杆式冷水主机', '大温差组合式空气处理机组']) {
      assert(!systemText.includes(forbidden), `System Operations contains inferred copy: ${forbidden}`);
    }
    assert(await noHorizontalOverflow(page), 'Desktop System Operations has horizontal overflow.');
    await page.screenshot({ path: resolve(outputRoot, 'system-operations-desktop.png'), fullPage: true });

    await page.getByRole('tab', { name: '空间与环境' }).click();
    await page.locator('[data-testid="comfort-workspace"]').waitFor();
    assert(new URL(page.url()).searchParams.get('view') === 'comfort', 'Comfort peer view is not URL-restorable.');
    assert(await page.locator('[data-workspace-id="operations"][aria-current="page"]').count() === 1, 'Operations workspace lost active state in comfort view.');
    assert(await page.locator('[data-testid="system-operations"]').count() === 0, 'System view remains mounted after switching to comfort.');
    assert(await page.locator('[data-testid="comfort-workspace"] [data-slot="table-body"] [data-slot="table-row"]').count() === 8, 'Frontend review comfort view does not expose the expected zone ledger.');
    assert(await noHorizontalOverflow(page), 'Desktop comfort view has horizontal overflow.');
    await page.screenshot({ path: resolve(outputRoot, 'comfort-desktop.png'), fullPage: true });

    await page.goto(`${base}/sites/${siteId}/operations/trends`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-site-route="operations-trends"]').waitFor();
    assert(await page.locator('[data-workspace-id="operations"][aria-current="page"]').count() === 1, 'Trend Studio is not owned by Operations workspace.');
    assert((await page.locator('[data-testid="real-shell-surface-identity"]').innerText()).trim() === '趋势分析', 'Trend Studio durable route title is wrong.');
    assert(await noHorizontalOverflow(page), 'Trend Studio has horizontal overflow.');

    await page.goto(`${base}/sites/${siteId}/operations/control`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="control-center"]').waitFor();
    assert(await page.locator('[data-workspace-id="operations"][aria-current="page"]').count() === 1, 'Immediate Control is not owned by Operations workspace.');
    assert((await page.locator('[data-testid="real-shell-surface-identity"]').innerText()).trim() === '即时控制', 'Immediate Control durable route title is wrong.');
    assert(await page.locator('[data-slot="skeleton"]').count() === 0, 'Frontend review control route is stuck in loading skeletons.');
    assert(await page.getByText('当前没有完整登记的控制目标').count() === 1, 'Control route did not preserve the no-registered-target empty state.');
    assert(await noHorizontalOverflow(page), 'Immediate Control has horizontal overflow.');

    await page.setViewportSize({ width: 768, height: 900 });
    await page.goto(`${base}/sites/${siteId}/operations?view=comfort`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="comfort-workspace"]').waitFor();
    assert(await noHorizontalOverflow(page), 'Narrow Operations workspace has horizontal overflow.');
    const narrowVisibleHeaders = await page.locator('[data-testid="comfort-workspace"] [data-slot="table-head"]').evaluateAll((nodes) => nodes
      .filter((node) => getComputedStyle(node).display !== 'none')
      .map((node) => node.textContent?.trim() ?? ''));
    assert(JSON.stringify(narrowVisibleHeaders) === JSON.stringify(['空间', '温度', '数据状态']), `Narrow comfort ledger exposed too many columns: ${JSON.stringify(narrowVisibleHeaders)}`);
    await page.screenshot({ path: resolve(outputRoot, 'comfort-narrow.png'), fullPage: true });

    assert(runtimeErrors.length === 0, `Runtime errors: ${runtimeErrors.join(' | ')}`);

    console.log(JSON.stringify({
      ok: true,
      workspace: 'operations',
      routes: {
        primary: `/sites/${siteId}/operations`,
        trends: `/sites/${siteId}/operations/trends`,
        control: `/sites/${siteId}/operations/control`,
      },
      navCount: navLabels.length,
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

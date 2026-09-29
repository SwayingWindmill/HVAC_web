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
    await page.locator('[data-testid="issues-workspace"]').waitFor();

    const navLabels = await page.locator('[data-workspace-id]').allTextContents();
    assert(navLabels.length === 10, `Expected 10 workspace navigation entries, got ${navLabels.length}`);
    assert(await page.locator('[data-workspace-id="issues"][aria-current="page"]').count() === 1, 'Issues workspace is not active.');
    assert((await page.locator('[data-testid="real-shell-surface-identity"]').innerText()).trim() === '告警与诊断', 'Workspace shell title is not 告警与诊断.');
    assert(JSON.stringify(await page.locator('[aria-label="告警与诊断工作区视图"] button').allTextContents()) === JSON.stringify(['告警', '诊断']), 'Issues peer views are not correct.');
    assert(await page.locator('[data-testid="alarm-center"]').count() === 1, 'Alarm peer view is not mounted by default.');
    assert(await page.locator('[data-testid="alarm-load-context"] [data-slot="card"]').count() === 0, 'Alarm facts regressed to Card-wall composition.');
    assert(await page.locator('[data-testid="alarm-triage-ledger"] h2').count() === 0, 'Alarm ledger reintroduced a duplicate table title.');
    assert(await noHorizontalOverflow(page), 'Desktop Alarm view has page-level horizontal overflow.');
    await page.screenshot({ path: resolve(outputRoot, 'issues-alarms-desktop.png'), fullPage: true });

    await page.locator('[aria-label="告警与诊断工作区视图"]').getByRole('tab', { name: '诊断' }).click();
    await page.waitForURL((url) => url.searchParams.get('view') === 'diagnostics');
    await page.locator('[data-testid="issues-workspace"][data-workspace-view="diagnostics"]').waitFor();
    assert(new URL(page.url()).searchParams.get('view') === 'diagnostics', 'Diagnosis peer view is not URL-restorable.');
    assert(await page.locator('[data-workspace-id="issues"][aria-current="page"]').count() === 1, 'Issues workspace lost active state in Diagnosis view.');
    assert(await page.locator('[data-testid="alarm-center"]').count() === 0, 'Alarm peer view remains mounted after switching to Diagnosis.');
    assert(await noHorizontalOverflow(page), 'Desktop Diagnosis view has page-level horizontal overflow.');
    await page.screenshot({ path: resolve(outputRoot, 'issues-diagnostics-route-desktop.png'), fullPage: true });

    await page.setViewportSize({ width: 768, height: 900 });
    await page.goto(`${base}/sites/${siteId}/issues?view=alarms`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="alarm-center"]').waitFor();
    assert(await noHorizontalOverflow(page), 'Narrow Alarm view has page-level horizontal overflow.');
    await page.screenshot({ path: resolve(outputRoot, 'issues-alarms-narrow.png'), fullPage: true });

    assert(runtimeErrors.length === 0, `Runtime errors: ${runtimeErrors.join(' | ')}`);

    console.log(JSON.stringify({
      ok: true,
      workspace: 'issues',
      route: `/sites/${siteId}/issues`,
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

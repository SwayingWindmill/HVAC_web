import { readFileSync } from 'node:fs';
import { chromium } from '@playwright/test';
import { runtimePath } from './local-environment.mjs';
import { resolveLinuxBrowserExecutable } from './browser-runtime.mjs';

// Signs in a local Identity account (the administrator by default); credentials never leave this process.
export async function localAdministratorBrowser(origin, credentialsFile = runtimePath('local-admin.credentials')) {
  const credentials = Object.fromEntries(readFileSync(credentialsFile, 'utf8')
    .trim().split(/\r?\n/).map((line) => { const index = line.indexOf('='); return [line.slice(0, index), line.slice(index + 1)]; }));
  const browser = await chromium.launch({ headless: true, executablePath: resolveLinuxBrowserExecutable() });
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
    const page = await context.newPage();
    await page.goto(origin);
    await page.evaluate(() => {
      const form = document.createElement('form');
      form.method = 'POST'; form.action = '/api/v1/auth/login?returnTo=%2Fsettings%2Fintegrations';
      document.body.append(form); form.submit();
    });
    await page.locator('#username').fill(credentials.username);
    await page.locator('#password').fill(credentials.password);
    await page.getByRole('button', { name: '登录', exact: true }).click();
    await page.waitForURL((url) => !url.pathname.startsWith('/identity') && !url.pathname.startsWith('/api/v1/auth'), { timeout: 30000 });
    const response = await context.request.get(`${origin}/api/v1/principal`);
    if (!response.ok()) throw new Error(`Local administrator session failed (HTTP ${response.status()})`);
    const principal = await response.json();
    return { browser, context, page, principal };
  } catch (error) { await browser.close(); throw error; }
}

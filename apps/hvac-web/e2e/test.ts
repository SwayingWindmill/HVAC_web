import { expect, test as base, type Page, type PageScreenshotOptions } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { Gateway } from './gateway';

const verifyRoot = resolve(import.meta.dirname, '../../../.scratch/verify');

export async function captureVerifyScreenshot(
  page: Page,
  relativePath: string,
  options?: Omit<PageScreenshotOptions, 'path'>,
): Promise<void> {
  if (process.env.HVAC_E2E_SCREENSHOTS !== '1') return;
  const outputPath = resolve(verifyRoot, relativePath);
  await mkdir(dirname(outputPath), { recursive: true });
  await page.screenshot({ fullPage: true, ...options, path: outputPath });
}

// Every test runs against its own recorded Gateway and fails on any call it does not serve.
export const test = base.extend<{ gateway: Gateway }>({
  gateway: [async ({ page }, use) => {
    const gateway = new Gateway();
    await gateway.install(page);
    await use(gateway);
    expect(gateway.unexpected).toEqual([]);
  }, { auto: true }],
});

export { expect };

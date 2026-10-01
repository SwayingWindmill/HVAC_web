import { chromium } from 'playwright';

async function capture() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();

  console.log('Navigating to http://localhost:5174/energy-analysis/consumption ...');
  await page.goto('http://localhost:5174/energy-analysis/consumption', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  console.log('Capturing consumption-overview.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/consumption-overview.png',
    fullPage: true,
  });

  // Click on the first row in the consumption ledger table to open DailyDetailSheet
  console.log('Clicking first table row to open DailyDetailSheet ...');
  const firstRow = page.locator('tbody tr').first();
  await firstRow.click();
  await page.waitForTimeout(600);

  console.log('Capturing consumption-sheet-open.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/consumption-sheet-open.png',
  });

  // Click second tab in the sheet: 设备级分摊
  console.log('Switching to breakdown tab in sheet ...');
  await page.click('button[role="tab"]:has-text("设备级分摊")');
  await page.waitForTimeout(400);

  console.log('Capturing consumption-sheet-breakdown.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/consumption-sheet-breakdown.png',
  });

  await browser.close();
  console.log('Capture completed successfully!');
}

capture().catch((err) => {
  console.error('Error during capture:', err);
  process.exit(1);
});

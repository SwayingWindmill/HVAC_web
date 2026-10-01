import { chromium } from 'playwright';

async function capture() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();

  console.log('Navigating to http://localhost:5174/energy-analysis/breakdown ...');
  await page.goto('http://localhost:5174/energy-analysis/breakdown', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  console.log('Capturing breakdown-pie-view.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/breakdown-pie-view.png',
    fullPage: true,
  });

  // Switch to Energy Flow view
  console.log('Switching to Energy Flow view ...');
  await page.click('button[role="tab"]:has-text("能流拓扑平衡图")');
  await page.waitForTimeout(600);

  console.log('Capturing breakdown-flow-view.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/breakdown-flow-view.png',
    fullPage: true,
  });

  // Switch back to Pie view and click first row to open MeterDetailSheet
  console.log('Switching back to Pie view to click first row ...');
  await page.click('button[role="tab"]:has-text("分项占比分析")');
  await page.waitForTimeout(400);

  console.log('Clicking first submeter row ...');
  const firstRow = page.locator('tbody tr').first();
  await firstRow.click();
  await page.waitForTimeout(600);

  console.log('Capturing breakdown-sheet-open.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/breakdown-sheet-open.png',
  });

  await browser.close();
  console.log('Breakdown capture completed successfully!');
}

capture().catch((err) => {
  console.error('Error during capture:', err);
  process.exit(1);
});

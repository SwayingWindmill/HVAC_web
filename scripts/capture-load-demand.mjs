import { chromium } from 'playwright';

async function capture() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();

  console.log('Navigating to http://localhost:5174/energy-analysis/load-demand ...');
  await page.goto('http://localhost:5174/energy-analysis/load-demand', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  console.log('Capturing load-demand-rolling.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/load-demand-rolling.png',
    fullPage: true,
  });

  // Switch to Load Duration Curve (LDC) tab
  console.log('Switching to LDC tab ...');
  await page.click('button[role="tab"]:has-text("负荷持续时间曲线 (LDC)")');
  await page.waitForTimeout(600);

  console.log('Capturing load-demand-ldc.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/load-demand-ldc.png',
    fullPage: true,
  });

  // Click on "柔性削峰预演" button in toolbar to open SheddingPlanSheet
  console.log('Opening SheddingPlanSheet ...');
  await page.click('button:has-text("柔性削峰预演")');
  await page.waitForTimeout(600);

  console.log('Capturing load-demand-sheet-open.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/load-demand-sheet-open.png',
  });

  await browser.close();
  console.log('Load & Demand capture completed successfully!');
}

capture().catch((err) => {
  console.error('Error during capture:', err);
  process.exit(1);
});

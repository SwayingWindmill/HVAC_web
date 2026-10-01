import { chromium } from 'playwright';

async function capture() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();

  console.log('Navigating to http://localhost:5174/operations/realtime ...');
  await page.goto('http://localhost:5174/operations/realtime', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  console.log('Capturing realtime-schematic.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/realtime-schematic.png',
    fullPage: true,
  });

  // Switch to Equipment Matrix tab
  console.log('Switching to Equipment Matrix tab ...');
  await page.click('button[role="tab"]:has-text("设备运行矩阵")');
  await page.waitForTimeout(600);

  console.log('Capturing realtime-matrix.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/realtime-matrix.png',
    fullPage: true,
  });

  // Switch to Control Loops tab
  console.log('Switching to Control Loops tab ...');
  await page.click('button[role="tab"]:has-text("自控回路与设定值")');
  await page.waitForTimeout(600);

  console.log('Capturing realtime-loops.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/realtime-loops.png',
    fullPage: true,
  });

  // Click on "遥测监视" button on the first equipment to open RealtimeInspectorSheet
  console.log('Opening RealtimeInspectorSheet ...');
  // First switch back to schematic or matrix to find the button
  await page.click('button[role="tab"]:has-text("设备运行矩阵")');
  await page.waitForTimeout(400);

  const detailBtn = await page.locator('td button:has-text("遥测监视")').first();
  await detailBtn.click();
  await page.waitForTimeout(600);

  console.log('Capturing realtime-sheet-open.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/realtime-sheet-open.png',
  });

  await browser.close();
  console.log('Realtime capture completed successfully!');
}

capture().catch((err) => {
  console.error('Error during capture:', err);
  process.exit(1);
});

import { chromium } from 'playwright';

async function capture() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();

  console.log('Navigating to http://localhost:5174/operations/control ...');
  await page.goto('http://localhost:5174/operations/control', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  console.log('Capturing control-strategies.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/control-strategies.png',
    fullPage: true,
  });

  // Switch to Commands tab
  console.log('Switching to Commands tab ...');
  await page.click('button[role="tab"]:has-text("控制指令与回读台账")');
  await page.waitForTimeout(600);

  console.log('Capturing control-commands.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/control-commands.png',
    fullPage: true,
  });

  // Switch to Interlocks tab
  console.log('Switching to Interlocks tab ...');
  await page.click('button[role="tab"]:has-text("安全联锁与保护边界")');
  await page.waitForTimeout(600);

  console.log('Capturing control-interlocks.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/control-interlocks.png',
    fullPage: true,
  });

  // Switch back to Commands tab and click "审计" button to open CommandDetailSheet
  console.log('Opening CommandDetailSheet ...');
  await page.click('button[role="tab"]:has-text("控制指令与回读台账")');
  await page.waitForTimeout(400);

  const auditBtn = await page.locator('td button:has-text("审计")').first();
  await auditBtn.click();
  await page.waitForTimeout(600);

  console.log('Capturing control-sheet-open.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/control-sheet-open.png',
  });

  await browser.close();
  console.log('Control capture completed successfully!');
}

capture().catch((err) => {
  console.error('Error during capture:', err);
  process.exit(1);
});

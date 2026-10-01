import { chromium } from 'playwright';

async function capture() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();

  console.log('Navigating to http://localhost:5174/reports ...');
  await page.goto('http://localhost:5174/reports', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  console.log('Capturing reports-hub.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/reports-hub.png',
    fullPage: true,
  });

  console.log('Navigating to http://localhost:5174/settings ...');
  await page.goto('http://localhost:5174/settings', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  console.log('Capturing settings-workspace.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/settings-workspace.png',
    fullPage: true,
  });

  await browser.close();
  console.log('Reports & Settings capture completed successfully!');
}

capture().catch((err) => {
  console.error('Error during capture:', err);
  process.exit(1);
});

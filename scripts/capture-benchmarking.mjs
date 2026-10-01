import { chromium } from 'playwright';

async function capture() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();

  console.log('Navigating to http://localhost:5174/energy-analysis/benchmarking ...');
  await page.goto('http://localhost:5174/energy-analysis/benchmarking', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  console.log('Capturing benchmarking-standards.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/benchmarking-standards.png',
    fullPage: true,
  });

  // Switch to Peers tab
  console.log('Switching to PEERS tab ...');
  await page.click('button[role="tab"]:has-text("同业商用建筑对标")');
  await page.waitForTimeout(600);

  console.log('Capturing benchmarking-peers.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/benchmarking-peers.png',
    fullPage: true,
  });

  // Click on "对标" button in the table row
  console.log('Opening PeerDetailSheet ...');
  const detailBtn = await page.locator('td button:has-text("对标")').first();
  await detailBtn.click();
  await page.waitForTimeout(600);

  console.log('Capturing benchmarking-sheet-open.png ...');
  await page.screenshot({
    path: '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b/benchmarking-sheet-open.png',
  });

  await browser.close();
  console.log('Benchmarking capture completed successfully!');
}

capture().catch((err) => {
  console.error('Error during capture:', err);
  process.exit(1);
});

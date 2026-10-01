import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';

const ARTIFACT_DIR = 'C:/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b';

async function main() {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1200 } });

  const routes = [
    { url: 'http://localhost:3001/dashboard/default', name: 'arham-default' },
    { url: 'http://localhost:3001/dashboard/analytics', name: 'arham-analytics' },
    { url: 'http://localhost:3001/dashboard/infrastructure', name: 'arham-infrastructure' },
    { url: 'http://localhost:3001/dashboard/crm', name: 'arham-crm' },
    { url: 'http://localhost:3001/dashboard/finance', name: 'arham-finance' },
  ];

  if (!fs.existsSync('out')) {
    fs.mkdirSync('out', { recursive: true });
  }

  for (const route of routes) {
    try {
      console.log(`Navigating to ${route.url}...`);
      await page.goto(route.url, { waitUntil: 'networkidle', timeout: 30000 });
      await page.waitForTimeout(1500);

      const outPath = `out/${route.name}.png`;
      await page.screenshot({ path: outPath, fullPage: true });
      console.log(`Saved screenshot to ${outPath}`);

      // Also copy to artifact dir for view_file
      const artifactPath = path.join(ARTIFACT_DIR, `${route.name}.png`);
      fs.copyFileSync(outPath, artifactPath);
      console.log(`Copied to ${artifactPath}`);
    } catch (e) {
      console.error(`Failed to capture ${route.url}:`, e.message);
    }
  }

  await browser.close();
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});

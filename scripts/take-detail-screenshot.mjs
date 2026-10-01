import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';

const ARTIFACT_DIR = '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b';

async function main() {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1800 } });

  await page.goto('http://localhost:5174/optimization/opportunities', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);

  // Click on the first row's "详情" button
  const detailBtn = await page.$('button:has-text("详情")');
  if (detailBtn) {
    await detailBtn.click();
    await page.waitForTimeout(600);
  }

  // Click on "流转闭环历史" tab
  const auditTab = await page.$('button:has-text("流转闭环历史")');
  if (auditTab) {
    await auditTab.click();
    await page.waitForTimeout(500);
  }

  const outPath = 'out/opportunities-sheet-audit.png';
  await page.screenshot({ path: outPath, fullPage: true });
  console.log(`Saved screenshot to ${outPath}`);

  const artifactPath = path.join(ARTIFACT_DIR, 'opportunities-sheet-audit.png');
  try {
    fs.copyFileSync(outPath, artifactPath);
    console.log(`Copied screenshot to ${artifactPath}`);
  } catch (err) {
    console.error('Failed to copy to artifact dir:', err.message);
  }

  await browser.close();
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});

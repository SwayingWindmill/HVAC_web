import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';

const ARTIFACT_DIR = '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b';

async function main() {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1800 } });

  // 1. Projects Workspace
  console.log('Navigating to /optimization/projects...');
  await page.goto('http://localhost:5174/optimization/projects', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);

  let outPath = 'out/projects-workspace.png';
  await page.screenshot({ path: outPath, fullPage: true });
  fs.copyFileSync(outPath, path.join(ARTIFACT_DIR, 'projects-workspace.png'));
  console.log('Saved projects-workspace.png');

  // Open Project Detail Sheet
  const detailBtn = await page.$('button:has-text("详情")');
  if (detailBtn) {
    await detailBtn.click();
    await page.waitForTimeout(600);
    outPath = 'out/projects-sheet-open.png';
    await page.screenshot({ path: outPath, fullPage: true });
    fs.copyFileSync(outPath, path.join(ARTIFACT_DIR, 'projects-sheet-open.png'));
    console.log('Saved projects-sheet-open.png');
  }

  // 2. Verification (M&V) Workspace
  console.log('Navigating to /optimization/verification...');
  await page.goto('http://localhost:5174/optimization/verification', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);

  outPath = 'out/verification-workspace.png';
  await page.screenshot({ path: outPath, fullPage: true });
  fs.copyFileSync(outPath, path.join(ARTIFACT_DIR, 'verification-workspace.png'));
  console.log('Saved verification-workspace.png');

  // Open Verification Detail Sheet
  const mvDetailBtn = await page.$('button:has-text("详情")');
  if (mvDetailBtn) {
    await mvDetailBtn.click();
    await page.waitForTimeout(600);
    outPath = 'out/verification-sheet-open.png';
    await page.screenshot({ path: outPath, fullPage: true });
    fs.copyFileSync(outPath, path.join(ARTIFACT_DIR, 'verification-sheet-open.png'));
    console.log('Saved verification-sheet-open.png');
  }

  await browser.close();
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});

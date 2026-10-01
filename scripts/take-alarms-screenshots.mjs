import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';

const ARTIFACT_DIR = '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b';

async function main() {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1800 } });

  page.on('console', msg => console.log('PAGE LOG:', msg.text()));
  page.on('pageerror', error => console.error('PAGE ERROR:', error));

  console.log('Navigating to http://localhost:5174/operations/alarms...');
  await page.goto('http://localhost:5174/operations/alarms', { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);

  // 1. Capture full Problem Queue workspace
  await page.screenshot({ path: 'out/alarms-workspace.png', fullPage: true });
  fs.copyFileSync('out/alarms-workspace.png', path.join(ARTIFACT_DIR, 'alarms-workspace.png'));
  console.log('Saved alarms-workspace.png');

  // 2. Click on the first row "排查" button to open Slide-over Sheet
  const inspectBtn = page.locator('button:has-text("排查")').first();
  if (await inspectBtn.isVisible()) {
    await inspectBtn.click();
    await page.waitForTimeout(1000);
    await page.screenshot({ path: 'out/alarms-sheet-open.png', fullPage: true });
    fs.copyFileSync('out/alarms-sheet-open.png', path.join(ARTIFACT_DIR, 'alarms-sheet-open.png'));
    console.log('Saved alarms-sheet-open.png');

    // 3. Switch to "诊断机理与假设" tab
    const hypothesesTab = page.locator('button:has-text("诊断机理与假设")');
    if (await hypothesesTab.isVisible()) {
      await hypothesesTab.click();
      await page.waitForTimeout(800);
      await page.screenshot({ path: 'out/alarms-sheet-hypotheses.png', fullPage: true });
      fs.copyFileSync('out/alarms-sheet-hypotheses.png', path.join(ARTIFACT_DIR, 'alarms-sheet-hypotheses.png'));
      console.log('Saved alarms-sheet-hypotheses.png');
    }

    // Close the sheet
    const closeBtn = page.locator('button:has-text("Close"), button[aria-label="Close"]').first();
    if (await closeBtn.isVisible()) {
      await closeBtn.click();
      await page.waitForTimeout(500);
    } else {
      await page.keyboard.press('Escape');
      await page.waitForTimeout(500);
    }
  }

  // 4. Click on "告警分析 (Alarm Performance)" tab
  const performanceTab = page.locator('button:has-text("告警分析")');
  if (await performanceTab.isVisible()) {
    await performanceTab.click();
    await page.waitForTimeout(1000);
    await page.screenshot({ path: 'out/alarms-performance.png', fullPage: true });
    fs.copyFileSync('out/alarms-performance.png', path.join(ARTIFACT_DIR, 'alarms-performance.png'));
    console.log('Saved alarms-performance.png');
  }

  await browser.close();
  console.log('All screenshots completed successfully!');
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});

import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';

const ARTIFACT_DIR = '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b';

async function main() {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1800 } });

  page.on('console', msg => console.log('PAGE LOG:', msg.text()));
  page.on('pageerror', error => console.error('PAGE ERROR:', error));

  console.log('Navigating to http://localhost:5174/operations/work-center...');
  await page.goto('http://localhost:5174/operations/work-center', { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);

  // 1. Capture full Ledger Table view
  await page.screenshot({ path: 'out/work-center-ledger.png', fullPage: true });
  fs.copyFileSync('out/work-center-ledger.png', path.join(ARTIFACT_DIR, 'work-center-ledger.png'));
  console.log('Saved work-center-ledger.png');

  // 2. Click on the first row "详情" button to open Slide-over Sheet
  const detailBtn = page.locator('button:has-text("详情")').first();
  if (await detailBtn.isVisible()) {
    await detailBtn.click();
    await page.waitForTimeout(1000);
    await page.screenshot({ path: 'out/work-center-sheet-open.png', fullPage: true });
    fs.copyFileSync('out/work-center-sheet-open.png', path.join(ARTIFACT_DIR, 'work-center-sheet-open.png'));
    console.log('Saved work-center-sheet-open.png');

    // 3. Switch to "作业清单" tab
    const tasksTab = page.locator('button:has-text("作业清单")');
    if (await tasksTab.isVisible()) {
      await tasksTab.click();
      await page.waitForTimeout(800);
      await page.screenshot({ path: 'out/work-center-sheet-tasks.png', fullPage: true });
      fs.copyFileSync('out/work-center-sheet-tasks.png', path.join(ARTIFACT_DIR, 'work-center-sheet-tasks.png'));
      console.log('Saved work-center-sheet-tasks.png');
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

  // 4. Click on "状态看板 (Kanban)" tab
  const kanbanTab = page.locator('button:has-text("状态看板")');
  if (await kanbanTab.isVisible()) {
    await kanbanTab.click();
    await page.waitForTimeout(1000);
    await page.screenshot({ path: 'out/work-center-kanban.png', fullPage: true });
    fs.copyFileSync('out/work-center-kanban.png', path.join(ARTIFACT_DIR, 'work-center-kanban.png'));
    console.log('Saved work-center-kanban.png');
  }

  await browser.close();
  console.log('All work center screenshots completed successfully!');
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});

import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';

const ARTIFACT_DIR = '/mnt/c/Users/HaoZhang/.gemini/antigravity/brain/b8764afc-ea23-46f9-adbb-7283d526ea9b';

async function main() {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1800 } });
  
  page.on('console', msg => console.log('PAGE LOG:', msg.text()));
  page.on('pageerror', error => console.error('PAGE ERROR:', error));

  const targetPath = process.argv[2] || '/overview';
  const outName = process.argv[3] || 'overview-check-v18.png';

  await page.goto(`http://localhost:5174${targetPath}`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  
  const outPath = `out/${outName}`;
  await page.screenshot({ path: outPath, fullPage: true });
  console.log(`Saved screenshot to ${outPath}`);

  const artifactPath = path.join(ARTIFACT_DIR, outName);
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

// Draws the twin alone at chosen film times and tiles them into one sheet, to check camera and light
// without a render. Usage (WSL): node scripts/twin-stills.ts <out.png> 0,4,10 [width]
import { chromium } from '../../../node_modules/playwright/index.mjs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const [out, list = '0,2,4,7,10,12,14,20,21.5,23,26,29,31.5,33,35,37,40,43,45,46.5,49', width = '480'] = process.argv.slice(2);
const times = list.split(',').map(Number);
const w = Number(width);
const h = Math.round((w * 9) / 16);
const browser = await chromium.launch({ args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader'] });
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } });
page.on('pageerror', (error) => console.log('PAGEERROR', error.message.slice(0, 300)));
await page.goto(pathToFileURL(join(here, '..', 'film', 'twin-only.html')).href);
const tiles = [];
for (const t of times) {
  const data = await page.evaluate(async ({ t, w, h }) => {
    const film = await window.twinReady;
    film.renderAt(t);
    const canvas = document.getElementById('twin');
    const small = document.createElement('canvas');
    small.width = w;
    small.height = h;
    const context = small.getContext('2d');
    context.drawImage(canvas, 0, 0, w, h);
    context.fillStyle = 'rgba(0,0,0,.55)';
    context.fillRect(0, 0, 64, 22);
    context.fillStyle = '#fff';
    context.font = '14px sans-serif';
    context.fillText(`${t}s`, 6, 16);
    return small.toDataURL('image/png');
  }, { t, w, h });
  tiles.push(data);
}
const columns = 4;
const sheet = await page.evaluate(async ({ tiles, w, h, columns }) => {
  const rows = Math.ceil(tiles.length / columns);
  const canvas = document.createElement('canvas');
  canvas.width = w * columns;
  canvas.height = h * rows;
  const context = canvas.getContext('2d');
  await Promise.all(tiles.map((src, index) => new Promise((resolve) => {
    const image = new Image();
    image.onload = () => { context.drawImage(image, (index % columns) * w, Math.floor(index / columns) * h); resolve(); };
    image.src = src;
  })));
  return canvas.toDataURL('image/png');
}, { tiles, w, h, columns });
const { writeFileSync } = await import('node:fs');
writeFileSync(out, Buffer.from(sheet.split(',')[1], 'base64'));
await browser.close();
console.log('wrote', out);

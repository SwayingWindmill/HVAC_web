// Copies the web's framework-free domain modules into the mini program, which WeChat's compiler
// can only build from inside miniprogram/. `--check` fails when a copy no longer matches its
// source, so a change to the web's vocabulary reaches the phone in the same pull request.
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const app = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repository = resolve(app, '..', '..');

const SHARED = [
  { from: 'apps/hvac-web/src/domain/centralPlantTelemetry.ts', to: 'miniprogram/shared/centralPlantTelemetry.ts' },
] as const;

const header = (from: string) => `// 由 apps/hvac-miniprogram/scripts/sync-shared.ts 从 ${from} 复制，不要在这里改。\n`;

const check = process.argv.includes('--check');
const stale: string[] = [];
for (const file of SHARED) {
  const expected = header(file.from) + await readFile(resolve(repository, file.from), 'utf8');
  const target = resolve(app, file.to);
  if (check) {
    const actual = await readFile(target, 'utf8').catch(() => null);
    if (actual !== expected) stale.push(file.to);
  } else {
    await writeFile(target, expected);
  }
}
if (stale.length > 0) {
  console.error(`These copies differ from the web; run npm -w @hvac/miniprogram run sync: ${stale.join(', ')}`);
  process.exit(1);
}

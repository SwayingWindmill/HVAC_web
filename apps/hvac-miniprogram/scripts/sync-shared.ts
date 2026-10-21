// Copies the web's framework-free domain modules into the mini program, which WeChat's compiler
// can only build from inside miniprogram/. `--check` fails when a copy no longer matches its
// source, so a change to the web's rules reaches the phone in the same pull request. A copy's
// imports of the web's generated contract point at the mini program's own types instead.
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const app = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repository = resolve(app, '..', '..');

const AGENT_TYPES: readonly [string, string] = ["from '@/api/generated/operationsAgentSessions.gen'", "from '../lib/agent-types'"];

const SHARED: readonly { readonly from: string; readonly to: string; readonly imports?: readonly (readonly [string, string])[] }[] = [
  { from: 'apps/hvac-web/src/domain/centralPlantTelemetry.ts', to: 'miniprogram/shared/centralPlantTelemetry.ts' },
  { from: 'apps/hvac-web/src/features/assistant/thread-model.ts', to: 'miniprogram/shared/thread-model.ts', imports: [AGENT_TYPES] },
  { from: 'apps/hvac-web/src/features/assistant/tool-labels.ts', to: 'miniprogram/shared/tool-labels.ts' },
  { from: 'apps/hvac-web/src/features/assistant/answer-markdown.ts', to: 'miniprogram/shared/answer-markdown.ts' },
];

const header = (from: string) => `// 由 apps/hvac-miniprogram/scripts/sync-shared.ts 从 ${from} 复制，不要在这里改。\n`;

async function copyOf(file: (typeof SHARED)[number]): Promise<string> {
  let source = await readFile(resolve(repository, file.from), 'utf8');
  for (const [web, phone] of file.imports ?? []) {
    if (!source.includes(web)) throw new Error(`${file.from} no longer imports ${web}; update scripts/sync-shared.ts`);
    source = source.split(web).join(phone);
  }
  return header(file.from) + source;
}

const check = process.argv.includes('--check');
const stale: string[] = [];
for (const file of SHARED) {
  const expected = await copyOf(file);
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

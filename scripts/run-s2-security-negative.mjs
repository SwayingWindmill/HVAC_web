import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-security-observability/security-command-evidence.json');
const npmExecPath = process.env.npm_execpath;
if (!npmExecPath) throw new Error('npm_execpath is unavailable; run this script through npm');
const scripts = [
  'test:security-negative',
  's2:live-client:check',
  's2:live-client:browser',
  's2:hvac-web:check',
  's2:hvac-web:browser',
];
const commands = [];
for (const script of scripts) {
  const started = Date.now();
  const result = spawnSync(process.argv0, [npmExecPath, 'run', script], { cwd: root, stdio: 'inherit', shell: false, windowsHide: true });
  const evidence = { script, exitCode: result.status ?? 1, durationMs: Date.now() - started };
  commands.push(evidence);
  if (result.error) throw result.error;
  if (result.status !== 0) {
    await mkdir(dirname(output), { recursive: true });
    await writeFile(output, `${JSON.stringify({ schemaVersion: 1, status: 'failed', commands }, null, 2)}\n`);
    process.exit(result.status ?? 1);
  }
}
await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify({ schemaVersion: 1, status: 'passed', commands }, null, 2)}\n`);
console.log(`S2 security-negative command evidence passed: ${output}`);

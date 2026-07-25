import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, relative, resolve } from 'node:path';

const root = resolve(process.cwd());
const arg = (name, fallback = '') => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const name = arg('name');
if (!/^[a-z0-9][a-z0-9._-]+\.json$/.test(name)) throw new Error('a bounded JSON report --name is required');
const output = resolve(root, arg('directory', 'out/s2-release-evidence'), name);
const commands = arg('commands').split(',').map((value) => value.trim()).filter(Boolean);
const details = arg('details') ? JSON.parse(arg('details')) : {};
const sourcePaths = arg('sources').split(',').map((value) => value.trim()).filter(Boolean);
const sources = [];
for (const sourcePath of sourcePaths) {
  const absolute = resolve(root, sourcePath);
  const raw = await readFile(absolute);
  const parsed = JSON.parse(raw.toString('utf8'));
  if (parsed.status && parsed.status !== 'passed') throw new Error(`source evidence is not passed: ${sourcePath}`);
  sources.push({
    path: relative(root, absolute).replaceAll('\\', '/'),
    sha256: createHash('sha256').update(raw).digest('hex'),
    evidence: parsed,
  });
}
const report = {
  schemaVersion: 1,
  kind: arg('kind', name.replace(/-report\.json$/, '')),
  status: 'passed',
  repositorySha: process.env.GITHUB_SHA ?? 'local-uncommitted',
  workflowRunId: process.env.GITHUB_RUN_ID ?? 'local',
  cleanRunner: process.env.CI === 'true',
  commands,
  details,
  sources,
};
await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify(report, null, 2)}\n`);
console.log(`S2 release report written: ${output}`);

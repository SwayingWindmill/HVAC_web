import { spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const directory = await mkdtemp(join(tmpdir(), 'npm-audit-retry-'));
const fakeCLI = join(directory, 'fake-npm-cli.mjs');
const statePath = join(directory, 'attempts.txt');

const fakeSource = `
import { readFileSync, writeFileSync } from 'node:fs';

const statePath = process.env.FAKE_NPM_AUDIT_STATE;
let attempt = 0;
try { attempt = Number.parseInt(readFileSync(statePath, 'utf8'), 10) || 0; } catch {}
attempt += 1;
writeFileSync(statePath, String(attempt));

const succeedOn = Number.parseInt(process.env.FAKE_NPM_AUDIT_SUCCEED_ON, 10);
if (attempt < succeedOn) {
  process.stdout.write(JSON.stringify({ error: { summary: '', detail: '' } }));
  process.exitCode = 1;
} else {
  process.stdout.write(JSON.stringify({
    auditReportVersion: 2,
    vulnerabilities: {},
    metadata: {
      vulnerabilities: { info: 0, low: 5, moderate: 2, high: 0, critical: 0, total: 7 },
    },
  }));
  process.exitCode = 1;
}
`;

const run = (attempts, succeedOn) => spawnSync(process.execPath, [resolve(root, 'scripts/check-npm-production-audit.mjs')], {
  cwd: root,
  encoding: 'utf8',
  windowsHide: true,
  env: {
    ...process.env,
    npm_execpath: fakeCLI,
    NPM_AUDIT_ATTEMPTS: String(attempts),
    NPM_AUDIT_RETRY_BASE_MS: '0',
    FAKE_NPM_AUDIT_STATE: statePath,
    FAKE_NPM_AUDIT_SUCCEED_ON: String(succeedOn),
  },
});

try {
  await writeFile(fakeCLI, fakeSource);
  await writeFile(statePath, '0');
  const recovered = run(5, 4);
  if (recovered.status !== 0) throw new Error(`dependency audit did not recover after transient empty responses: ${recovered.stdout}\n${recovered.stderr}`);
  if (Number.parseInt(await readFile(statePath, 'utf8'), 10) !== 4) throw new Error('dependency audit retry count drifted');
  if (!recovered.stderr.includes('attempt 3/5')) throw new Error('dependency audit retry diagnostics are incomplete');

  await writeFile(statePath, '0');
  const exhausted = run(3, 99);
  const exhaustedDetail = `${exhausted.stdout}\n${exhausted.stderr}`;
  if (exhausted.status === 0) throw new Error('dependency audit passed after retry exhaustion');
  if (!exhaustedDetail.includes('after 3 attempts') || !exhaustedDetail.includes('"exitCode":1') || !exhaustedDetail.includes('"npmError"')) {
    throw new Error(`dependency audit exhaustion diagnostics are incomplete: ${exhaustedDetail}`);
  }
  console.log('Production dependency audit transient-retry and fail-closed tests passed.');
} finally {
  await rm(directory, { recursive: true, force: true });
}

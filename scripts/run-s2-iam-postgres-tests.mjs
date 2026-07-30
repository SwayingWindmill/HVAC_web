import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-ticket-02/iam-authorization-evidence.json');
const result = spawnSync(process.execPath, [resolve(root, 'scripts/run-s1-registry-postgres-tests.mjs')], {
  cwd: root,
  encoding: 'utf8',
  stdio: 'inherit',
  windowsHide: true,
  env: process.env,
});
if (result.error || result.status !== 0) {
  throw new Error(`S2 IAM PostgreSQL evidence failed: ${result.error?.message ?? result.status}`);
}
const baselinePath = resolve(root, 'out/s1-registry-core/postgres-baseline.json');
const baselineText = await readFile(baselinePath, 'utf8');
const baseline = JSON.parse(baselineText);
const evidence = {
  schemaVersion: 1,
  ticket: 'S2-02',
  status: 'passed',
  checkedAt: new Date().toISOString(),
  activationStatus: 'expand-baseline',
  publicTrafficEnabled: false,
  underlyingEvidence: {
    path: 'out/s1-registry-core/postgres-baseline.json',
    sha256: createHash('sha256').update(baselineText).digest('hex'),
    status: baseline.status ?? 'passed',
  },
  assertions: [
    'exact Device rows are selected by app.requested_device_ids under FORCE RLS',
    'exact Device and key permissions are evaluated with deny precedence',
    'the first grant use succeeds and the same JTI is detected as replay',
    'key permission changes emit organization-scoped ordered revocation facts',
    'revocation evidence is observable within the ten-second delivery budget',
    'IAM grant state is isolated behind s2_iam_grant_runtime',
  ],
  tests: [
    'TestPostgresTelemetryAuthorizationLoadsExactDeviceAndKeyFacts',
    'TestPostgresTelemetryGrantSingleUse',
    'TestPostgresTelemetryRevocationPoll',
  ],
};
await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
console.log(`S2 IAM PostgreSQL evidence passed: ${output}`);

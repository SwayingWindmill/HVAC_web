import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'out/s0-release-evidence-test');
const imagesRoot = resolve(fixtureRoot, 'images');
const trivyRoot = resolve(fixtureRoot, 'trivy');
const reportPath = resolve(fixtureRoot, 'image-verification-report.json');
const expectedNames = [
  'audit-ledger-service',
  'iam-service',
  'oidc-test-provider',
  'outbox-relay',
  'platform-gateway',
  's0-migrator',
];

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function runNode(args, expectSuccess = true) {
  const child = spawn(process.execPath, args, {
    cwd: root,
    env: {
      ...process.env,
      GITHUB_REPOSITORY: 'SwayingWindmill/HVAC_web',
      GITHUB_REF: 'refs/heads/test-release-evidence',
      GITHUB_SHA: 'a'.repeat(40),
      GITHUB_WORKFLOW: 'S0 release evidence fixture',
      GITHUB_WORKFLOW_REF: 'SwayingWindmill/HVAC_web/.github/workflows/s0-supply-chain.yml@refs/heads/test-release-evidence',
      GITHUB_RUN_ID: '1',
      GITHUB_RUN_ATTEMPT: '1',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
    shell: false,
    windowsHide: true,
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += String(chunk); });
  child.stderr.on('data', (chunk) => { stderr += String(chunk); });
  const exit = await new Promise((resolveExit) => {
    child.once('error', (error) => resolveExit({ code: null, signal: null, error }));
    child.once('exit', (code, signal) => resolveExit({ code, signal, error: null }));
  });
  const succeeded = !exit.error && exit.code === 0 && !exit.signal;
  if (succeeded !== expectSuccess) {
    const detail = stderr.trim() || stdout.trim() || exit.error?.message || `exit ${exit.code ?? exit.signal}`;
    throw new Error(`node ${args.join(' ')} ${expectSuccess ? 'failed' : 'unexpectedly passed'}: ${detail}`);
  }
  return { stdout, stderr };
}

function cleanTrivyReport() {
  return {
    SchemaVersion: 2,
    ArtifactName: 'fixture',
    ArtifactType: 'container_image',
    Results: [],
  };
}

await rm(fixtureRoot, { recursive: true, force: true });
await mkdir(imagesRoot, { recursive: true });
await mkdir(trivyRoot, { recursive: true });

try {
  for (const name of expectedNames) {
    const digest = `sha256:${createHash('sha256').update(`s0-release-evidence-${name}`).digest('hex')}`;
    const trivyName = `trivy-secrets-${name}.json`;
    await writeFile(resolve(trivyRoot, trivyName), `${JSON.stringify(cleanTrivyReport(), null, 2)}\n`);
    await runNode([
      'scripts/write-s0-release-image-evidence.mjs',
      `--name=${name}`,
      `--image=ghcr.io/swayingwindmill/hvac-web/${name}`,
      `--digest=${digest}`,
      `--trivy=${trivyName}`,
      '--github-attestation=published',
      `--output=out/s0-release-evidence-test/images/${name}.json`,
    ]);
  }

  await runNode([
    'scripts/verify-s0-release-images.mjs',
    '--offline=true',
    '--images=out/s0-release-evidence-test/images',
    '--trivy=out/s0-release-evidence-test/trivy',
    '--report=out/s0-release-evidence-test/image-verification-report.json',
  ]);
  const passed = JSON.parse(await readFile(reportPath, 'utf8'));
  assert(passed.status === 'passed', 'clean image evidence fixture did not pass');
  assert(passed.results.length === expectedNames.length, `clean image evidence fixture verified ${passed.results.length} images`);
  assert(passed.results.every((result) => result.secretFindings === 0), 'clean image evidence fixture reported a secret finding');

  const poisonedPath = resolve(trivyRoot, 'trivy-secrets-platform-gateway.json');
  const poisoned = cleanTrivyReport();
  poisoned.Results = [{ Target: 'fixture', Class: 'secret', Type: 'secret', Secrets: [{ RuleID: 'fixture-secret', Category: 'fixture', Title: 'Fixture finding', Severity: 'HIGH' }] }];
  await writeFile(poisonedPath, `${JSON.stringify(poisoned, null, 2)}\n`);
  await runNode([
    'scripts/verify-s0-release-images.mjs',
    '--offline=true',
    '--images=out/s0-release-evidence-test/images',
    '--trivy=out/s0-release-evidence-test/trivy',
    '--report=out/s0-release-evidence-test/image-verification-report.json',
  ], false);
  const failed = JSON.parse(await readFile(reportPath, 'utf8'));
  assert(failed.status === 'failed', 'poisoned image evidence fixture did not fail');
  assert(String(failed.error).includes('embedded-secret findings'), `poisoned failure reason was unexpected: ${failed.error}`);

  console.log(`S0 release image evidence fixture tests passed for ${expectedNames.length} images and one injected failure.`);
} finally {
  await rm(fixtureRoot, { recursive: true, force: true });
}

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { basename, dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const argument = (name) => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3);
const imagesRoot = resolve(root, argument('images') ?? 'out/s0-release-input/images');
const trivyRoot = resolve(root, argument('trivy') ?? 'out/s0-release-input/trivy');
const reportPath = resolve(root, argument('report') ?? 'out/s0-release-evidence/image-verification-report.json');
const offline = argument('offline') === 'true';
const repository = process.env.GITHUB_REPOSITORY ?? 'SwayingWindmill/HVAC_web';
const startedAt = new Date();
const results = [];

const expectedNames = [
  'audit-ledger-service',
  'iam-service',
  'outbox-relay',
  'platform-gateway',
  's0-migrator',
];

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function filesUnder(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) files.push(...await filesUnder(path));
    else files.push(path);
  }
  return files;
}

async function run(command, args) {
  const child = spawn(command, args, {
    cwd: root,
    env: process.env,
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
  if (exit.error || exit.code !== 0 || exit.signal) {
    const detail = stderr.trim() || stdout.trim() || exit.error?.message || `exit ${exit.code ?? exit.signal}`;
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return { stdout, stderr };
}

function secretFindings(report) {
  if (!report || typeof report !== 'object') return null;
  const resultsArray = Array.isArray(report.Results) ? report.Results : [];
  return resultsArray.reduce((count, result) => count + (Array.isArray(result.Secrets) ? result.Secrets.length : 0), 0);
}

async function writeReport(status, error = null) {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify({
    schemaVersion: 1,
    ticket: '08-s0-release-evidence',
    type: 'immutable-image-reverification',
    status,
    offline,
    repository,
    startedAt: startedAt.toISOString(),
    finishedAt: new Date().toISOString(),
    expectedImages: expectedNames,
    results,
    error,
  }, null, 2)}\n`);
}

try {
  assert(existsSync(imagesRoot), `release image evidence directory does not exist: ${imagesRoot}`);
  assert(existsSync(trivyRoot), `Trivy evidence directory does not exist: ${trivyRoot}`);
  const manifestFiles = (await filesUnder(imagesRoot)).filter((path) => path.endsWith('.json'));
  const manifests = [];
  for (const path of manifestFiles) {
    const parsed = JSON.parse(await readFile(path, 'utf8'));
    if (parsed.ticket === '08-s0-release-evidence' && parsed.immutableReference) manifests.push({ path, value: parsed });
  }
  assert(manifests.length === expectedNames.length, `expected ${expectedNames.length} release image manifests, found ${manifests.length}`);
  const actualNames = manifests.map(({ value }) => value.name).sort();
  assert(JSON.stringify(actualNames) === JSON.stringify(expectedNames), `release image set mismatch: ${JSON.stringify(actualNames)}`);

  const trivyFiles = (await filesUnder(trivyRoot)).filter((path) => path.endsWith('.json'));
  for (const { path, value } of manifests.sort((left, right) => left.value.name.localeCompare(right.value.name))) {
    assert(/^sha256:[a-f0-9]{64}$/.test(value.digest), `${value.name} digest is invalid`);
    assert(value.immutableReference === `${value.image}@${value.digest}`, `${value.name} immutable reference is inconsistent`);
    assert(value.security?.embeddedSecretScan?.status === 'passed', `${value.name} did not record a passed Trivy scan`);
    assert(value.security?.cosign?.status === 'verified', `${value.name} did not record Cosign verification`);
    assert(['published', 'skipped-non-public'].includes(value.security?.githubBuildAttestation?.status), `${value.name} attestation state is invalid`);

    const expectedTrivyName = basename(value.security.embeddedSecretScan.report);
    const trivyPath = trivyFiles.find((candidate) => basename(candidate) === expectedTrivyName);
    assert(trivyPath, `${value.name} Trivy JSON artifact is missing: ${expectedTrivyName}`);
    const trivy = JSON.parse(await readFile(trivyPath, 'utf8'));
    const findings = secretFindings(trivy);
    assert(findings !== null, `${value.name} Trivy report shape is invalid`);
    assert(findings === 0, `${value.name} Trivy report contains ${findings} embedded-secret findings`);

    const result = {
      name: value.name,
      manifest: path,
      immutableReference: value.immutableReference,
      trivyReport: trivyPath,
      secretFindings: findings,
      cosign: { status: offline ? 'recorded-only' : 'pending', stdoutTail: null },
      githubAttestation: { status: offline ? 'recorded-only' : 'pending', stdoutTail: null },
    };
    results.push(result);

    if (!offline) {
      const sourceRef = process.env.GITHUB_REF;
      const sourceDigest = process.env.GITHUB_SHA;
      assert(sourceRef?.startsWith('refs/'), 'GITHUB_REF is required for exact release verification');
      assert(/^[a-f0-9]{40,64}$/.test(sourceDigest ?? ''), 'GITHUB_SHA is required for exact release verification');
      const signerWorkflow = `${repository}/.github/workflows/s0-supply-chain.yml`;
      const certificateIdentity = `https://github.com/${signerWorkflow}@${sourceRef}`;
      const cosign = await run('cosign', [
        'verify', value.immutableReference,
        '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com',
        '--certificate-identity', certificateIdentity,
      ]);
      result.cosign = { status: 'verified', certificateIdentity, stdoutTail: cosign.stdout.slice(-2000) };

      assert(value.security.githubBuildAttestation.status === 'published', `${value.name} requires a published GitHub attestation in the public repository`);
      const attestation = await run('gh', [
        'attestation', 'verify', `oci://${value.immutableReference}`,
        '--repo', repository,
        '--signer-workflow', signerWorkflow,
        '--source-digest', sourceDigest,
        '--source-ref', sourceRef,
        '--format', 'json',
      ]);
      result.githubAttestation = { status: 'verified', signerWorkflow, sourceDigest, sourceRef, stdoutTail: attestation.stdout.slice(-2000) };
    }
  }

  await writeReport('passed');
  console.log(`S0 immutable release image verification passed for ${results.length} images; report: ${reportPath}`);
} catch (error) {
  await writeReport('failed', error instanceof Error ? error.message : String(error));
  throw error;
}

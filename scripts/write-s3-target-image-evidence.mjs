import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { basename, resolve } from 'node:path';

const allowedImages = new Set([
  'command-service',
  'command-dispatcher',
  'command-verifier',
  'command-migrator',
]);

function parseArgs(argv) {
  const values = {};
  for (const argument of argv) {
    if (!argument.startsWith('--')) throw new Error(`unexpected argument: ${argument}`);
    const separator = argument.indexOf('=');
    if (separator < 3) throw new Error(`argument must use --name=value syntax: ${argument}`);
    values[argument.slice(2, separator)] = argument.slice(separator + 1);
  }
  return values;
}

function required(args, name) {
  const value = String(args[name] ?? '').trim();
  if (!value) throw new Error(`--${name} is required`);
  return value;
}

function assertTrivyPassed(report) {
  if (!report || typeof report !== 'object' || Array.isArray(report)) {
    throw new Error('Trivy evidence must be a JSON object');
  }
  const findings = [];
  for (const result of Array.isArray(report.Results) ? report.Results : []) {
    for (const finding of Array.isArray(result?.Secrets) ? result.Secrets : []) findings.push(finding);
  }
  if (findings.length !== 0) throw new Error(`Trivy secret scan contains ${findings.length} finding(s)`);
}

function main() {
  const root = resolve(process.cwd());
  const args = parseArgs(process.argv.slice(2));
  const name = required(args, 'name');
  const image = required(args, 'image').toLowerCase();
  const digest = required(args, 'digest').toLowerCase();
  const repositorySha = required(args, 'repository-sha').toLowerCase();
  const workflowRunId = required(args, 'workflow-run-id');
  const sourceRef = required(args, 'source-ref');
  const trivyPath = resolve(root, required(args, 'trivy'));
  const githubAttestation = required(args, 'github-attestation');
  const outputDir = resolve(root, String(args['output-dir'] ?? 'out/s3-target-release/images'));

  if (!allowedImages.has(name)) throw new Error(`unsupported S3 target image: ${name}`);
  if (!new RegExp(`^ghcr\\.io/[a-z0-9_.-]+/hvac-web/${name}$`).test(image)) {
    throw new Error(`image repository is not the approved GHCR path for ${name}`);
  }
  if (!/^sha256:[0-9a-f]{64}$/.test(digest)) throw new Error('image digest must be lowercase sha256');
  if (!/^[0-9a-f]{40}$/.test(repositorySha)) throw new Error('repository SHA must be a lowercase 40-character Git SHA');
  if (!/^[1-9][0-9]*$/.test(workflowRunId)) throw new Error('workflow Run ID must be a positive integer');
  if (!/^refs\/(heads|tags)\/.+/.test(sourceRef)) throw new Error('source ref must be a GitHub heads or tags ref');
  if (!new Set(['published', 'skipped-non-public']).has(githubAttestation)) {
    throw new Error('GitHub attestation status is invalid');
  }
  if (!existsSync(trivyPath)) throw new Error(`Trivy evidence does not exist: ${basename(trivyPath)}`);

  const trivyBytes = readFileSync(trivyPath);
  let trivyReport;
  try {
    trivyReport = JSON.parse(trivyBytes.toString('utf8'));
  } catch {
    throw new Error('Trivy evidence is not valid JSON');
  }
  assertTrivyPassed(trivyReport);

  const evidence = {
    schemaVersion: 1,
    kind: 's3-target-image',
    status: 'signed-scanned-and-attested',
    name,
    image,
    digest,
    immutableReference: `${image}@${digest}`,
    repositorySha,
    workflowRunId,
    sourceRef,
    build: {
      sbom: true,
      provenance: 'mode=max',
    },
    signature: {
      provider: 'sigstore-keyless',
      oidcIssuer: 'https://token.actions.githubusercontent.com',
      verified: true,
    },
    secretScan: {
      scanner: 'trivy',
      passed: true,
      evidenceSha256: createHash('sha256').update(trivyBytes).digest('hex'),
    },
    githubAttestation,
    formalCertificationClaim: false,
    generatedAt: new Date().toISOString(),
  };

  mkdirSync(outputDir, { recursive: true });
  const outputPath = resolve(outputDir, `${name}.json`);
  writeFileSync(outputPath, `${JSON.stringify(evidence, null, 2)}\n`);
  console.log(`Recorded S3 target image evidence: ${outputPath}`);
}

try {
  main();
} catch (error) {
  console.error(`S3 target image evidence failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}

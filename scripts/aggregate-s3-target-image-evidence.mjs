import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const expectedNames = [
  'command-dispatcher',
  'command-migrator',
  'command-service',
  'command-verifier',
];

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

function assertImageEvidence(name, evidence) {
  if (evidence?.schemaVersion !== 1 || evidence?.kind !== 's3-target-image') {
    throw new Error(`${name} evidence schema is invalid`);
  }
  if (evidence.name !== name || evidence.status !== 'signed-scanned-and-attested') {
    throw new Error(`${name} evidence status is invalid`);
  }
  const image = String(evidence.image ?? '');
  if (!/^ghcr\.io\/[a-z0-9_.-]+\/hvac-web\/[a-z0-9-]+$/.test(image) || !image.endsWith(`/hvac-web/${name}`)) {
    throw new Error(`${name} image repository is invalid`);
  }
  if (!/^sha256:[0-9a-f]{64}$/.test(String(evidence.digest ?? ''))) {
    throw new Error(`${name} digest is invalid`);
  }
  if (evidence.immutableReference !== `${evidence.image}@${evidence.digest}`) {
    throw new Error(`${name} immutable reference does not match image and digest`);
  }
  if (!/^[0-9a-f]{40}$/.test(String(evidence.repositorySha ?? ''))) {
    throw new Error(`${name} repository SHA is invalid`);
  }
  if (!/^[1-9][0-9]*$/.test(String(evidence.workflowRunId ?? ''))) {
    throw new Error(`${name} workflow Run ID is invalid`);
  }
  if (!/^refs\/(heads|tags)\/.+/.test(String(evidence.sourceRef ?? ''))) {
    throw new Error(`${name} source ref is invalid`);
  }
  if (evidence.build?.sbom !== true || evidence.build?.provenance !== 'mode=max') {
    throw new Error(`${name} build attestations are incomplete`);
  }
  if (evidence.signature?.verified !== true || evidence.signature?.provider !== 'sigstore-keyless') {
    throw new Error(`${name} signature verification is incomplete`);
  }
  if (evidence.secretScan?.passed !== true || !/^[0-9a-f]{64}$/.test(String(evidence.secretScan?.evidenceSha256 ?? ''))) {
    throw new Error(`${name} secret scan evidence is invalid`);
  }
  if (!new Set(['published', 'skipped-non-public']).has(evidence.githubAttestation)) {
    throw new Error(`${name} GitHub attestation status is invalid`);
  }
  if (evidence.formalCertificationClaim !== false) {
    throw new Error(`${name} evidence must not claim formal certification`);
  }
}

function main() {
  const root = resolve(process.cwd());
  const args = parseArgs(process.argv.slice(2));
  const inputDir = resolve(root, String(args['input-dir'] ?? 'out/s3-target-release/images'));
  const outputPath = resolve(root, String(args.output ?? 'out/s3-target-release/image-manifest.json'));

  const records = [];
  for (const name of expectedNames) {
    const path = resolve(inputDir, `${name}.json`);
    if (!existsSync(path)) throw new Error(`missing image evidence: ${name}.json`);
    const bytes = readFileSync(path);
    let evidence;
    try {
      evidence = JSON.parse(bytes.toString('utf8'));
    } catch {
      throw new Error(`${name} evidence is not valid JSON`);
    }
    assertImageEvidence(name, evidence);
    records.push({ evidence, evidenceSha256: createHash('sha256').update(bytes).digest('hex') });
  }

  const repositoryShas = new Set(records.map(({ evidence }) => evidence.repositorySha));
  const workflowRunIds = new Set(records.map(({ evidence }) => evidence.workflowRunId));
  const sourceRefs = new Set(records.map(({ evidence }) => evidence.sourceRef));
  if (repositoryShas.size !== 1) throw new Error('image evidence does not share one repository SHA');
  if (workflowRunIds.size !== 1) throw new Error('image evidence does not share one workflow Run ID');
  if (sourceRefs.size !== 1) throw new Error('image evidence does not share one source ref');

  const manifest = {
    schemaVersion: 1,
    kind: 's3-target-image-manifest',
    status: 'candidate-target-images-published',
    repositorySha: records[0].evidence.repositorySha,
    workflowRunId: records[0].evidence.workflowRunId,
    sourceRef: records[0].evidence.sourceRef,
    images: Object.fromEntries(records.map(({ evidence, evidenceSha256 }) => [evidence.name, {
      image: evidence.image,
      digest: evidence.digest,
      immutableReference: evidence.immutableReference,
      evidenceSha256,
    }])),
    releaseRequirements: {
      sbom: true,
      provenance: 'mode=max',
      cosignVerified: true,
      secretScanPassed: true,
    },
    formalCertificationClaim: false,
    generatedAt: new Date().toISOString(),
  };

  mkdirSync(resolve(outputPath, '..'), { recursive: true });
  writeFileSync(outputPath, `${JSON.stringify(manifest, null, 2)}\n`);
  console.log(`Aggregated S3 target image manifest: ${outputPath}`);
}

try {
  main();
} catch (error) {
  console.error(`S3 target image manifest failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}

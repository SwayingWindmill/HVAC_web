import { createHash } from 'node:crypto';
import { copyFileSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const root = resolve(process.cwd());
const writer = resolve(root, 'scripts/write-s3-target-image-evidence.mjs');
const aggregator = resolve(root, 'scripts/aggregate-s3-target-image-evidence.mjs');
const names = ['command-service', 'command-dispatcher', 'command-verifier', 'command-migrator'];

function run(script, args, expectedStatus = 0) {
  const result = spawnSync(process.execPath, [script, ...args], {
    cwd: root,
    encoding: 'utf8',
  });
  if (result.status !== expectedStatus) {
    throw new Error(`unexpected exit status for ${script}: expected=${expectedStatus} actual=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}`);
  }
  return result;
}

const directory = mkdtempSync(join(tmpdir(), 's3-target-image-evidence-'));
try {
  const imageDirectory = join(directory, 'images');
  const trivyDirectory = join(directory, 'trivy');
  mkdirSync(imageDirectory, { recursive: true });
  mkdirSync(trivyDirectory, { recursive: true });

  for (const name of names) {
    const trivyPath = join(trivyDirectory, `${name}.json`);
    writeFileSync(trivyPath, `${JSON.stringify({ SchemaVersion: 2, Results: [] }, null, 2)}\n`);
    const digest = `sha256:${createHash('sha256').update(name).digest('hex')}`;
    run(writer, [
      `--name=${name}`,
      `--image=ghcr.io/example/hvac-web/${name}`,
      `--digest=${digest}`,
      '--repository-sha=0123456789abcdef0123456789abcdef01234567',
      '--workflow-run-id=123456789',
      '--source-ref=refs/tags/s3-v-test',
      `--trivy=${trivyPath}`,
      '--github-attestation=published',
      `--output-dir=${imageDirectory}`,
    ]);
  }

  const manifestPath = join(directory, 'image-manifest.json');
  run(aggregator, [`--input-dir=${imageDirectory}`, `--output=${manifestPath}`]);
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  if (manifest.status !== 'candidate-target-images-published' || manifest.repositorySha !== '0123456789abcdef0123456789abcdef01234567') {
    throw new Error('aggregated image manifest has unexpected identity or status');
  }
  if (Object.keys(manifest.images ?? {}).sort().join(',') !== names.sort().join(',')) {
    throw new Error('aggregated image manifest does not contain the exact image set');
  }
  if (manifest.formalCertificationClaim !== false) {
    throw new Error('image manifest must not claim formal certification');
  }

  const incompleteDirectory = join(directory, 'incomplete');
  mkdirSync(incompleteDirectory, { recursive: true });
  for (const name of names.slice(0, -1)) {
    copyFileSync(join(imageDirectory, `${name}.json`), join(incompleteDirectory, `${name}.json`));
  }
  const missingResult = spawnSync(process.execPath, [aggregator, `--input-dir=${incompleteDirectory}`, `--output=${join(directory, 'invalid.json')}`], {
    cwd: root,
    encoding: 'utf8',
  });
  if (missingResult.status === 0 || !missingResult.stderr.includes('missing image evidence')) {
    throw new Error('aggregate must fail closed when one image evidence file is missing');
  }

  const tamperedDirectory = join(directory, 'tampered');
  mkdirSync(tamperedDirectory, { recursive: true });
  for (const name of names) {
    copyFileSync(join(imageDirectory, `${name}.json`), join(tamperedDirectory, `${name}.json`));
  }
  const tamperedPath = join(tamperedDirectory, 'command-service.json');
  const tamperedEvidence = JSON.parse(readFileSync(tamperedPath, 'utf8'));
  tamperedEvidence.image = 'ghcr.io/example/hvac-web/command-verifier';
  tamperedEvidence.immutableReference = `${tamperedEvidence.image}@${tamperedEvidence.digest}`;
  writeFileSync(tamperedPath, `${JSON.stringify(tamperedEvidence, null, 2)}\n`);
  const tamperedResult = spawnSync(process.execPath, [aggregator, `--input-dir=${tamperedDirectory}`, `--output=${join(directory, 'tampered.json')}`], {
    cwd: root,
    encoding: 'utf8',
  });
  if (tamperedResult.status === 0 || !tamperedResult.stderr.includes('image repository is invalid')) {
    throw new Error('aggregate must fail closed when image evidence points at another repository');
  }

  const findingPath = join(trivyDirectory, 'finding.json');
  writeFileSync(findingPath, `${JSON.stringify({ SchemaVersion: 2, Results: [{ Secrets: [{ RuleID: 'fixture' }] }] }, null, 2)}\n`);
  const findingResult = spawnSync(process.execPath, [writer,
    '--name=command-service',
    '--image=ghcr.io/example/hvac-web/command-service',
    `--digest=sha256:${createHash('sha256').update('finding').digest('hex')}`,
    '--repository-sha=0123456789abcdef0123456789abcdef01234567',
    '--workflow-run-id=123456789',
    '--source-ref=refs/tags/s3-v-test',
    `--trivy=${findingPath}`,
    '--github-attestation=published',
    `--output-dir=${join(directory, 'finding-output')}`,
  ], { cwd: root, encoding: 'utf8' });
  if (findingResult.status === 0 || !findingResult.stderr.includes('Trivy secret scan contains')) {
    throw new Error('writer must fail closed when Trivy reports a secret finding');
  }

  console.log('S3 target image evidence tests passed: exact four-image manifest and negative gates');
} finally {
  rmSync(directory, { recursive: true, force: true });
}

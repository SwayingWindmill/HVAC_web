import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { buildRealShellCertificationEnvelope } from './rms-certification-evidence-lib.mjs';

const root = resolve(process.cwd());
const outputDirectory = join(root, 'out', 'rms-08');
const reportPath = join(outputDirectory, 'real-shell-certification.json');
const checksumPath = join(outputDirectory, 'SHA256SUMS');

const paths = {
  gates: join(outputDirectory, 'gate-results.json'),
  browser: join(outputDirectory, 'browser-evidence.json'),
  graph: join(root, 'out', 'rms-01', 'real-dependency-graph.json'),
  bundle: join(root, 'out', 'rms-01', 'build-artifact-audit.json'),
  policy: join(root, 'deploy', 'rms', 'real-shell-release-policy.v1.json'),
  tooling: join(root, 'contracts', 'http', 'tooling.lock.json'),
  contract: join(root, 'contracts', 'http', 'platform-gateway.openapi.yaml'),
};

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'));
}

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

function workspacePath(path) {
  return relative(root, path).replaceAll('\\', '/');
}

function repositorySha() {
  const configured = process.env.GITHUB_SHA?.trim();
  if (configured && /^[0-9a-f]{40}$/.test(configured)) return configured;
  const result = spawnSync('git', ['rev-parse', 'HEAD'], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  const value = result.stdout?.trim();
  if (result.status !== 0 || !/^[0-9a-f]{40}$/.test(value ?? '')) {
    throw new Error(`RMS-08 could not resolve an immutable repository SHA: ${result.stderr || result.error?.message || 'unknown error'}`);
  }
  return value;
}

const gates = readJson(paths.gates);
if (gates.passed !== true) throw new Error('RMS-08 gate-results.json is not passing');

const tooling = readJson(paths.tooling);
const policy = readJson(paths.policy);
const graph = readJson(paths.graph);
const bundle = readJson(paths.bundle);
const browser = readJson(paths.browser);

const envelope = buildRealShellCertificationEnvelope({
  repositorySha: repositorySha(),
  gates: gates.gates,
  contract: {
    generatorVersion: tooling.generatorVersion,
    generatedClientDrift: false,
    contractSha256: sha256(paths.contract),
  },
  graph,
  bundle,
  browser,
  policy,
});

const sourceArtifacts = [
  paths.gates,
  paths.browser,
  paths.graph,
  paths.bundle,
  paths.policy,
  paths.tooling,
  paths.contract,
];
const artifacts = Object.fromEntries(sourceArtifacts.map((path) => [workspacePath(path), sha256(path)]));

const report = {
  ...envelope,
  generatedAt: new Date().toISOString(),
  scopeStatement: policy.scopeStatement,
  evidenceArtifacts: artifacts,
};

mkdirSync(dirname(reportPath), { recursive: true });
writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');

const checksumArtifacts = [...sourceArtifacts, reportPath];
const checksums = checksumArtifacts
  .map((path) => `${sha256(path)}  ${workspacePath(path)}`)
  .sort()
  .join('\n');
writeFileSync(checksumPath, `${checksums}\n`, 'utf8');

console.log(`RMS-08 Real Shell certification evidence built: ${workspacePath(reportPath)}`);

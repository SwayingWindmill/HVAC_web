import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';

import {
  OPERATIONS_AGENT_SAFETY_GATES,
  buildOperationsAgentSafetyCertificationReport,
  validateOperationsAgentSafetyCertificationReport,
} from './operations-agent-safety-certification.v1.mjs';

const root = resolve(process.cwd());
const outputDirectory = join(root, 'out', 'operations-agent-safety-certification');
const logsDirectory = join(outputDirectory, 'logs');
const supportingDirectory = join(outputDirectory, 'supporting');
const reportPath = join(outputDirectory, 'certification.json');
const checksumPath = join(outputDirectory, 'SHA256SUMS');
const browserEvidencePath = join(root, 'out', 'operations-reconnect-certification', 'browser-evidence.json');
const postgresEvidencePath = join(root, 'out', 'operations-agent', 'postgres-persistence.json');
const npmCli = process.env.npm_execpath;

if (!npmCli) throw new Error('Operations Agent safety certification must be started through npm.');

const npmGate = (id, script) => ({
  id,
  command: process.execPath,
  args: [npmCli, 'run', script],
  display: `npm run ${script}`,
});

const gates = [
  npmGate('gateway-contract', 'operations-agent:gateway:check'),
  {
    id: 'gateway-negative',
    command: process.execPath,
    args: [
      'scripts/run-go.mjs',
      'test',
      '-count=1',
      '-v',
      './libs/registryauth/...',
      './modules/iam/...',
      './modules/registry/...',
      './modules/telemetry/...',
      './cmd/energy-api/...',
    ],
    display: 'node scripts/run-go.mjs test -count=1 -v <Operations authorization boundary packages>',
  },
  npmGate('agent-service', 'operations-agent-service:check'),
  npmGate('operations-postgres', 'operations-agent-service:postgres'),
  npmGate('durable-postgres', 'test:durable-postgres'),
  npmGate('workspace-unit', 'operations-workspace:test'),
  npmGate('workspace-browser', 'operations-workspace:browser'),
];

if (gates.length !== OPERATIONS_AGENT_SAFETY_GATES.length
  || gates.some(({ id }, index) => id !== OPERATIONS_AGENT_SAFETY_GATES[index])) {
  throw new Error('Operations Agent safety gate order drifted from the versioned contract.');
}

const sha256File = (path) => createHash('sha256').update(readFileSync(path)).digest('hex');

function resolveRepositorySha() {
  const configured = process.env.GITHUB_SHA?.trim();
  if (configured && /^[0-9a-f]{40}$/u.test(configured)) return configured;
  const result = spawnSync('git', ['rev-parse', 'HEAD'], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  const value = result.stdout?.trim();
  if (result.status !== 0 || !/^[0-9a-f]{40}$/u.test(value ?? '')) {
    throw new Error(`Could not resolve repository SHA: ${result.stderr || result.error?.message || 'unknown error'}`);
  }
  return value;
}

rmSync(outputDirectory, { recursive: true, force: true });
mkdirSync(logsDirectory, { recursive: true });
mkdirSync(supportingDirectory, { recursive: true });

const startedAt = new Date().toISOString();
const gateResults = {};

for (const gate of gates) {
  const gateStartedAt = Date.now();
  console.log(`\n[Operations Map 5.5] ${gate.id}: ${gate.display}`);
  const result = spawnSync(gate.command, gate.args, {
    cwd: root,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    windowsHide: true,
  });
  const stdout = result.stdout ?? '';
  const stderr = result.stderr ?? '';
  const stdoutPath = join(logsDirectory, `${gate.id}.stdout.log`);
  const stderrPath = join(logsDirectory, `${gate.id}.stderr.log`);
  writeFileSync(stdoutPath, stdout, 'utf8');
  writeFileSync(stderrPath, stderr, 'utf8');
  const passed = !result.error && result.status === 0;
  gateResults[gate.id] = {
    passed,
    command: gate.display,
    durationMs: Date.now() - gateStartedAt,
    exitCode: result.status ?? null,
    signal: result.signal ?? null,
    stdout,
    stderr,
    stdoutPath: relative(outputDirectory, stdoutPath).replaceAll('\\', '/'),
    stderrPath: relative(outputDirectory, stderrPath).replaceAll('\\', '/'),
    stdoutSha256: sha256File(stdoutPath),
    stderrSha256: sha256File(stderrPath),
  };
  if (!passed) {
    process.stdout.write(stdout);
    process.stderr.write(stderr);
    console.error(`[Operations Map 5.5] gate ${gate.id} failed.`);
    process.exit(result.status || 1);
  }
  const summary = stdout.trim().split(/\r?\n/u).slice(-3).join('\n');
  if (summary) console.log(summary);
}

const browserEvidence = JSON.parse(readFileSync(browserEvidencePath, 'utf8'));
const postgresEvidence = JSON.parse(readFileSync(postgresEvidencePath, 'utf8'));
if (browserEvidence.passed !== true) throw new Error('Operations Workspace browser evidence did not pass.');
if (postgresEvidence.passed !== true && postgresEvidence.status !== 'passed') {
  throw new Error('Operations PostgreSQL evidence did not pass.');
}
const copiedBrowserEvidencePath = join(supportingDirectory, 'browser-evidence.json');
const copiedPostgresEvidencePath = join(supportingDirectory, 'postgres-persistence.json');
copyFileSync(browserEvidencePath, copiedBrowserEvidencePath);
copyFileSync(postgresEvidencePath, copiedPostgresEvidencePath);

const report = buildOperationsAgentSafetyCertificationReport({
  repositorySha: resolveRepositorySha(),
  startedAt,
  completedAt: new Date().toISOString(),
  gateResults,
  browserEvidence,
});
report.supportingArtifacts = [
  ...gates.flatMap(({ id }) => [`logs/${id}.stdout.log`, `logs/${id}.stderr.log`]),
  'supporting/browser-evidence.json',
  'supporting/postgres-persistence.json',
];
report.postgresEvidence = {
  schemaVersion: postgresEvidence.schemaVersion ?? null,
  passed: postgresEvidence.passed === true || postgresEvidence.status === 'passed',
  digest: sha256File(postgresEvidencePath),
};

const validation = validateOperationsAgentSafetyCertificationReport(report);
if (!validation.valid) {
  console.error(validation.failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}
writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');

const checksumArtifacts = [
  ...gates.flatMap(({ id }) => [
    join(logsDirectory, `${id}.stdout.log`),
    join(logsDirectory, `${id}.stderr.log`),
  ]),
  copiedBrowserEvidencePath,
  copiedPostgresEvidencePath,
];
const checksumLines = checksumArtifacts.map((path) => (
  `${sha256File(path)}  ${relative(outputDirectory, path).replaceAll('\\', '/')}`
));
writeFileSync(checksumPath, `${checksumLines.join('\n')}\n`, 'utf8');

console.log(`[Operations Map 5.5] certification passed. Evidence: ${reportPath}`);

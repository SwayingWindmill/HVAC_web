import { spawnSync } from 'node:child_process';
import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const outputDirectory = join(root, 'out', 'rms-web-certification');
const gateResultsPath = join(outputDirectory, 'gate-results.json');
const npmCli = process.env.npm_execpath;

if (!npmCli) {
  throw new Error('RMS-08 certification must be started through npm so npm_execpath is available.');
}

function resolveRepositorySha() {
  const configured = process.env.GITHUB_SHA?.trim();
  if (configured && /^[0-9a-f]{40}$/.test(configured)) return configured;
  const result = spawnSync('git', ['rev-parse', 'HEAD'], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  const value = result.stdout?.trim();
  if (result.status !== 0 || !/^[0-9a-f]{40}$/.test(value ?? '')) {
    throw new Error(`RMS-08 could not resolve repository SHA: ${result.stderr || result.error?.message || 'unknown error'}`);
  }
  return value;
}

function npmGate(name, script) {
  return {
    name,
    command: process.execPath,
    args: [npmCli, 'run', script],
    display: `npm run ${script}`,
  };
}

const gates = [
  npmGate('contracts', 'contracts:check'),
  npmGate('shell-tests', 'rms:certification:test'),
  {
    name: 'go-tests',
    command: process.execPath,
    args: [
      'scripts/run-go.mjs',
      'test',
      '-count=1',
      './libs/identitycontext/...',
      './libs/ownershipregistry/...',
      './libs/registryauth/...',
      './services/iam-service/...',
      './services/platform-gateway/...',
    ],
    display: 'node scripts/run-go.mjs test -count=1 <RMS IAM/Gateway packages>',
  },
  npmGate('typecheck', 'lint'),
  npmGate('real-graph', 'rms:real:graph'),
  npmGate('real-build', 'build:real'),
  npmGate('demo-build', 'build:demo'),
  npmGate('bundle', 'rms:real:bundle'),
  npmGate('browser', 'rms:web-routing:browser'),
];

rmSync(outputDirectory, { recursive: true, force: true });
mkdirSync(outputDirectory, { recursive: true });

const report = {
  schemaVersion: 1,
  certification: 'RMS-08_REAL_MODE_SHELL',
  repositorySha: resolveRepositorySha(),
  startedAt: new Date().toISOString(),
  passed: false,
  gates: [],
};

function persist() {
  writeFileSync(gateResultsPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
}

for (const gate of gates) {
  const startedAt = Date.now();
  console.log(`\n[RMS-08] ${gate.name}: ${gate.display}`);
  const result = spawnSync(gate.command, gate.args, {
    cwd: root,
    env: process.env,
    stdio: 'inherit',
    windowsHide: true,
  });
  const durationMs = Date.now() - startedAt;
  const passed = !result.error && result.status === 0;
  report.gates.push({
    name: gate.name,
    command: gate.display,
    passed,
    durationMs,
    exitCode: result.status ?? null,
    signal: result.signal ?? null,
    error: result.error?.message ?? null,
  });
  persist();
  if (!passed) {
    console.error(`[RMS-08] gate ${gate.name} failed.`);
    process.exit(result.status || 1);
  }
}

report.passed = true;
report.completedAt = new Date().toISOString();
persist();

const evidence = spawnSync(process.execPath, ['scripts/build-rms-real-shell-certification.mjs'], {
  cwd: root,
  env: process.env,
  stdio: 'inherit',
  windowsHide: true,
});
if (evidence.error || evidence.status !== 0) {
  console.error(`[RMS-08] evidence builder failed: ${evidence.error?.message ?? `exit ${evidence.status}`}`);
  process.exit(evidence.status || 1);
}

console.log(`[RMS-08] certification passed. Evidence directory: ${outputDirectory}`);

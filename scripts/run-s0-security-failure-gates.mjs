import { spawn } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const reportArgument = process.argv.find((value) => value.startsWith('--report='))?.slice('--report='.length);
const reportPath = resolve(root, reportArgument ?? 'out/s0-security/security-failure-gate-report.json');
const startedAt = new Date();
const results = [];

const cases = [
  {
    id: 'assets',
    command: process.execPath,
    args: [resolve(root, 'scripts/check-s0-security-failure-assets.mjs')],
  },
  {
    id: 'negative-identity-tenant',
    command: process.execPath,
    args: [
      resolve(root, 'scripts/run-go.mjs'), 'test', '-count=1',
      './libs/identitycontext/...',
      './libs/oidctest/...',
      './libs/ownershipregistry/...',
      './services/iam-service/...',
      './services/audit-ledger-service/...',
      './services/outbox-relay/...',
      './services/platform-gateway/...',
    ],
  },
  {
    id: 'network-policy',
    command: process.execPath,
    args: [resolve(root, 'scripts/check-s0-network-policies.mjs'), '--report=out/s0-security/network-policy-report.json'],
  },
  {
    id: 'durable-postgres',
    command: process.execPath,
    args: [resolve(root, 'scripts/run-durable-postgres-tests.mjs')],
  },
  {
    id: 'production-shaped-browser-failures',
    command: process.execPath,
    args: [resolve(root, 'scripts/run-durable-session-browser-audit.mjs')],
  },
];

async function runCase(testCase) {
  const caseStarted = Date.now();
  const child = spawn(testCase.command, testCase.args, {
    cwd: root,
    env: process.env,
    stdio: ['ignore', 'pipe', 'pipe'],
    shell: false,
    windowsHide: true,
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => {
    const text = String(chunk);
    stdout += text;
    process.stdout.write(text);
  });
  child.stderr.on('data', (chunk) => {
    const text = String(chunk);
    stderr += text;
    process.stderr.write(text);
  });
  const exit = await new Promise((resolveExit) => {
    child.once('error', (error) => resolveExit({ code: null, signal: null, error }));
    child.once('exit', (code, signal) => resolveExit({ code, signal, error: null }));
  });
  const result = {
    id: testCase.id,
    status: !exit.error && exit.code === 0 && !exit.signal ? 'passed' : 'failed',
    durationMs: Date.now() - caseStarted,
    command: [testCase.command, ...testCase.args].join(' '),
    exitCode: exit.code,
    signal: exit.signal,
    error: exit.error?.message ?? null,
    stdoutTail: stdout.slice(-4000),
    stderrTail: stderr.slice(-4000),
  };
  results.push(result);
  return result;
}

async function writeReport(status, error = null) {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify({
    schemaVersion: 1,
    ticket: '07-security-and-failure-gates',
    status,
    startedAt: startedAt.toISOString(),
    finishedAt: new Date().toISOString(),
    durationMs: Date.now() - startedAt.getTime(),
    upstream: {
      toxiproxy: 'Shopify/toxiproxy@v2.12.0',
      netpolAnalyzer: 'np-guard/netpol-analyzer@v1.4.4',
      trivyAction: 'aquasecurity/trivy-action@ed142fd0673e97e23eac54620cfb913e5ce36c25',
    },
    results,
    error,
  }, null, 2)}\n`);
}

try {
  for (const testCase of cases) {
    const result = await runCase(testCase);
    if (result.status !== 'passed') throw new Error(`${testCase.id} failed`);
  }
  await writeReport('passed');
  console.log(`S0 security and failure-injection release gate passed; report: ${reportPath}`);
} catch (error) {
  await writeReport('failed', error instanceof Error ? error.message : String(error));
  throw error;
}

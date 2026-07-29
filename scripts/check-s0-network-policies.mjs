import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { cp, mkdir, rm, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';

const root = resolve(process.cwd());
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const reportArgument = process.argv.find((value) => value.startsWith('--report='))?.slice('--report='.length);
const reportPath = resolve(root, reportArgument ?? 'out/s0-security/network-policy-report.json');
const workspace = join(tmpdir(), `hvac-s0-netpol-${process.pid}`);
const baseRoot = join(workspace, 'base');
const overlayRoot = join(workspace, 'overlay');
const renderedRoot = join(workspace, 'rendered');
const binaryRoot = join(workspace, 'bin');
const analyzerBinary = join(binaryRoot, process.platform === 'win32' ? 'netpolicy.exe' : 'netpolicy');
const goCacheRoot = join(tmpdir(), 'hvac-go-build-cache');
const KUSTOMIZE_VERSION = 'v5.7.1';
const NETPOL_ANALYZER_VERSION = 'v1.4.4';

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    maxBuffer: 16 * 1024 * 1024,
    ...options,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

const selectedResources = [
  'namespace.yaml',
  'networkpolicies.yaml',
];

const checks = [
  { id: 'public-ingress-to-gateway', source: 'browser-probe', sourceNamespace: 'public-ingress', destination: 'platform-gateway', destinationNamespace: 's0-staging', port: '8080', expected: true },
  { id: 'browser-to-iam-denied', source: 'browser-probe', sourceNamespace: 'public-ingress', destination: 'iam-service', destinationNamespace: 's0-staging', port: '8444', expected: false },
  { id: 'browser-to-audit-denied', source: 'browser-probe', sourceNamespace: 'public-ingress', destination: 'audit-ledger-service', destinationNamespace: 's0-staging', port: '8446', expected: false },
  { id: 'browser-to-postgres-denied', source: 'browser-probe', sourceNamespace: 'public-ingress', destination: 'postgres', destinationNamespace: 's0-staging', port: '5432', expected: false },
  { id: 'browser-to-broker-denied', source: 'browser-probe', sourceNamespace: 'public-ingress', destination: 'redpanda', destinationNamespace: 's0-staging', port: '9092', expected: false },
  { id: 'gateway-to-iam-allowed', source: 'platform-gateway', sourceNamespace: 's0-staging', destination: 'iam-service', destinationNamespace: 's0-staging', port: '8444', expected: true },
  { id: 'gateway-to-audit-allowed', source: 'platform-gateway', sourceNamespace: 's0-staging', destination: 'audit-ledger-service', destinationNamespace: 's0-staging', port: '8446', expected: true },
  { id: 'gateway-to-postgres-allowed', source: 'platform-gateway', sourceNamespace: 's0-staging', destination: 'postgres', destinationNamespace: 's0-staging', port: '5432', expected: true },
  { id: 'audit-to-redpanda-allowed', source: 'audit-ledger-service', sourceNamespace: 's0-staging', destination: 'redpanda', destinationNamespace: 's0-staging', port: '9092', expected: true },
  { id: 'relay-to-redpanda-allowed', source: 'outbox-relay', sourceNamespace: 's0-staging', destination: 'redpanda', destinationNamespace: 's0-staging', port: '9092', expected: true },
  { id: 'iam-to-otel-allowed', source: 'iam-service', sourceNamespace: 's0-staging', destination: 'otel-collector', destinationNamespace: 's0-staging', port: '4318', expected: true },
  { id: 'migrator-to-postgres-allowed', source: 's0-schema-migration', sourceNamespace: 's0-staging', destination: 'postgres', destinationNamespace: 's0-staging', port: '5432', expected: true },
  { id: 'gateway-to-metadata-denied', source: 'platform-gateway', sourceNamespace: 's0-staging', destinationIP: '169.254.169.254', port: '80', expected: false },
  { id: 'gateway-dns-rebinding-private-target-denied', source: 'platform-gateway', sourceNamespace: 's0-staging', destinationIP: '10.0.0.1', port: '443', expected: false },
];

async function writeReport(results, status, error = null) {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify({
    schemaVersion: 1,
    gate: 's0-network-policy',
    status,
    generatedAt: new Date().toISOString(),
    tools: {
      kustomize: `sigs.k8s.io/kustomize/kustomize/v5@${KUSTOMIZE_VERSION}`,
      netpolAnalyzer: `github.com/np-guard/netpol-analyzer/cmd/netpolicy@${NETPOL_ANALYZER_VERSION}`,
    },
    results,
    error,
  }, null, 2)}\n`);
}

const results = [];
try {
  await rm(workspace, { recursive: true, force: true });
  await mkdir(join(baseRoot, 'workloads'), { recursive: true });
  await mkdir(overlayRoot, { recursive: true });
  await mkdir(renderedRoot, { recursive: true });
  await mkdir(binaryRoot, { recursive: true });
  await mkdir(goCacheRoot, { recursive: true });

  for (const resource of selectedResources) {
    const source = resolve(root, 'deploy/s0/staging', resource);
    const destination = join(baseRoot, resource);
    await mkdir(dirname(destination), { recursive: true });
    await cp(source, destination);
  }
  await writeFile(join(baseRoot, 'kustomization.yaml'), `apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: s0-staging\nresources:\n${selectedResources.map((resource) => `  - ${resource}`).join('\n')}\n`);
  await cp(resolve(root, 'tests/s0-security/network-policy/fixtures.yaml'), join(overlayRoot, 'fixtures.yaml'));
  await writeFile(join(overlayRoot, 'kustomization.yaml'), 'apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../base\n  - fixtures.yaml\n');

  const rendered = run(goBinary, ['run', `sigs.k8s.io/kustomize/kustomize/v5@${KUSTOMIZE_VERSION}`, 'build', overlayRoot, '--load-restrictor', 'LoadRestrictionsNone'], {
    env: { ...process.env, GOCACHE: goCacheRoot },
  });
  await writeFile(join(renderedRoot, 'resources.yaml'), `${rendered}\n`);
  run(goBinary, ['install', `github.com/np-guard/netpol-analyzer/cmd/netpolicy@${NETPOL_ANALYZER_VERSION}`], {
    env: { ...process.env, GOBIN: binaryRoot, GOCACHE: goCacheRoot },
  });

  for (const check of checks) {
    const args = [
      'eval', '--dirpath', renderedRoot,
      '--source-pod', check.source,
      '--source-namespace', check.sourceNamespace,
      '--destination-port', check.port,
      '--quiet',
    ];
    if (check.destination) args.push('--destination-pod', check.destination, '--destination-namespace', check.destinationNamespace);
    else args.push('--destination-ip', check.destinationIP);
    const output = run(analyzerBinary, args);
    const match = output.match(/:\s*(true|false)\s*$/i);
    if (!match) throw new Error(`netpol-analyzer returned an unrecognized result for ${check.id}: ${output}`);
    const actual = match[1].toLowerCase() === 'true';
    const result = { ...check, actual, passed: actual === check.expected, output };
    results.push(result);
    if (!result.passed) throw new Error(`${check.id} was ${actual}, expected ${check.expected}`);
  }

  await writeReport(results, 'passed');
  console.log(`S0 NetworkPolicy and private-target checks passed; report: ${reportPath}`);
} catch (error) {
  await writeReport(results, 'failed', error instanceof Error ? error.message : String(error));
  throw error;
} finally {
  await rm(workspace, { recursive: true, force: true });
}

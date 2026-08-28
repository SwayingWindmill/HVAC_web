import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const outputPath = resolve(root, 'out/s1-iam-provider-poc/logto-comparison.json');
const sdkOutputPath = resolve(root, 'out/s1-iam-provider-poc/logto-sdk-adoption.json');
const isolatedModule = 'pocs/logto-sdk-adoption';

function run(label, args) {
  const result = spawnSync(process.execPath, args, {
    cwd: root,
    env: process.env,
    encoding: 'utf8',
    windowsHide: true,
  });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  if (result.error || result.status !== 0) {
    throw new Error(`${label} failed: ${result.error?.message ?? result.status}`);
  }
  return {
    label,
    command: [process.execPath, ...args].join(' '),
    status: 'passed',
    stdoutTail: result.stdout.slice(-2000),
    stderrTail: result.stderr.slice(-2000),
  };
}

function runInherited(label, args) {
  const result = spawnSync(process.execPath, args, {
    cwd: root,
    env: process.env,
    stdio: 'inherit',
    windowsHide: true,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${label} failed: ${result.error?.message ?? result.status}`);
  }
  return {
    label,
    command: [process.execPath, ...args].join(' '),
    status: 'passed',
    output: 'inherited by the parent process to avoid retaining browser child-process pipes',
  };
}

const baselineTests = run('existing-gateway-identity-tests', [
  resolve(root, 'scripts/run-go.mjs'),
  'test', '-count=1', './cmd/energy-api/...',
]);
const baselineBrowser = runInherited('existing-gateway-browser-audit', [
  resolve(root, 'scripts/run-auth-principal-browser-audit.mjs'),
]);
const moduleVerify = run('logto-sdk-module-verify', [
  resolve(root, 'scripts/run-isolated-go.mjs'), `--module=${isolatedModule}`, 'mod', 'verify',
]);
const sdkTests = run('logto-sdk-black-box-tests', [
  resolve(root, 'scripts/run-isolated-go.mjs'), `--module=${isolatedModule}`, 'test', '-count=1', './...',
]);
const vulnerabilityScan = run('logto-sdk-vulnerability-scan', [
  resolve(root, 'scripts/run-isolated-go.mjs'), `--module=${isolatedModule}`,
  'run', 'golang.org/x/vuln/cmd/govulncheck@v1.1.4', './...',
]);
run('logto-sdk-evidence-report', [
  resolve(root, 'scripts/run-isolated-go.mjs'), `--module=${isolatedModule}`,
  'run', './cmd/report', `--output=${sdkOutputPath}`,
]);

const sdkReport = JSON.parse(await readFile(sdkOutputPath, 'utf8'));
const report = {
  schemaVersion: 1,
  ticket: '02-iam-registry-read-authorization',
  status: 'passed',
  existingImplementation: {
    status: 'passed',
    evidence: {
      goTests: baselineTests,
      browserAudit: baselineBrowser,
    },
    provenControls: [
      'Authorization Code with S256 PKCE, state and nonce',
      'issuer, audience, signature, token type, expiry and not-before rejection',
      'JWKS rotation',
      'opaque HttpOnly BFF Session with CSRF and Origin checks',
      'local and administrative Session revocation with cross-Organization isolation',
      'credential-free public URL and logs',
    ],
    knownGap: 'does not yet implement Logto refresh and global end-session reconciliation',
  },
  officialSDK: {
    status: 'passed',
    moduleVerify,
    tests: sdkTests,
    vulnerabilityScan,
    report: sdkReport,
  },
  selectedMode: sdkReport.decision.mode,
  productionDirection: sdkReport.decision,
};

await mkdir(dirname(outputPath), { recursive: true });
await writeFile(outputPath, `${JSON.stringify(report, null, 2)}\n`);
console.log(`S1 Logto SDK adoption POC passed: ${sdkReport.decision.mode}; report: ${outputPath}`);

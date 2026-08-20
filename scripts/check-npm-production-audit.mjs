import { spawnSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const severities = ['low', 'moderate', 'high', 'critical'];
const blockingSeverities = ['moderate', 'high', 'critical'];

const boundedInteger = (name, fallback, minimum, maximum) => {
  const parsed = Number.parseInt(process.env[name] ?? String(fallback), 10);
  if (!Number.isInteger(parsed) || parsed < minimum || parsed > maximum) {
    throw new Error(`${name} must be an integer between ${minimum} and ${maximum}`);
  }
  return parsed;
};

const auditAttempts = boundedInteger('NPM_AUDIT_ATTEMPTS', 5, 1, 10);
const auditRetryBaseMs = boundedInteger('NPM_AUDIT_RETRY_BASE_MS', 1000, 0, 10000);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const boundedDetail = (value) => String(value ?? '').trim().slice(0, 2000);

const args = ['audit', '--omit=dev', '--json'];
const npmCLI = process.env.npm_execpath || resolve(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js');
let report;
let lastAuditFailure = '';

for (let attempt = 1; attempt <= auditAttempts; attempt += 1) {
  const result = spawnSync(process.execPath, [npmCLI, ...args], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (result.error) throw result.error;
  try {
    const candidate = JSON.parse(result.stdout);
    if (candidate.metadata?.vulnerabilities) {
      report = candidate;
      break;
    }
    lastAuditFailure = JSON.stringify({
      exitCode: result.status,
      signal: result.signal,
      npmError: boundedDetail(JSON.stringify(candidate.error ?? candidate)),
      stderr: boundedDetail(result.stderr),
    });
  } catch (error) {
    lastAuditFailure = JSON.stringify({
      exitCode: result.status,
      signal: result.signal,
      parseError: error instanceof Error ? error.message : String(error),
      stderr: boundedDetail(result.stderr),
      stdout: boundedDetail(result.stdout),
    });
  }
  if (attempt < auditAttempts) {
    console.warn(`npm audit attempt ${attempt}/${auditAttempts} returned no vulnerability metadata; retrying`);
    await pause(Math.min(attempt * auditRetryBaseMs, 10000));
  }
}

if (!report) throw new Error(`npm audit did not return complete vulnerability metadata after ${auditAttempts} attempts: ${lastAuditFailure}`);

const counts = Object.fromEntries(severities.map((severity) => [severity, Number(report.metadata.vulnerabilities[severity] || 0)]));
for (const severity of blockingSeverities) {
  if (counts[severity] !== 0) throw new Error(`production npm audit has ${counts[severity]} ${severity} vulnerabilities; no waiver is permitted`);
}

console.log(`Production npm audit passed with no moderate/high/critical vulnerabilities: ${severities.map((severity) => `${severity}=${counts[severity]}`).join(', ')}`);

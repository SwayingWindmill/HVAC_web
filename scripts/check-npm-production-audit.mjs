import { spawnSync } from 'node:child_process';
import { readdir, readFile } from 'node:fs/promises';
import { dirname, extname, join, resolve } from 'node:path';

const root = resolve(process.cwd());
const baseline = JSON.parse(await readFile(resolve(root, 'deploy/s0/security/dependency-audit-baseline.json'), 'utf8'));
const expiry = new Date(`${baseline.expiresOn}T23:59:59Z`);
if (!Number.isFinite(expiry.getTime()) || Date.now() > expiry.getTime()) {
  throw new Error(`Dependency audit baseline expired on ${baseline.expiresOn}; ticket ${baseline.remediationIssue} must refresh or remove it`);
}

const severities = ['low', 'moderate', 'high', 'critical'];
const auditAttempts = 3;
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function exactArray(actual, expected) {
  const left = [...actual].sort((a, b) => String(a).localeCompare(String(b)));
  const right = [...expected].sort((a, b) => String(a).localeCompare(String(b)));
  return JSON.stringify(left) === JSON.stringify(right);
}

async function collectSource(directory) {
  const chunks = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      chunks.push(await collectSource(path));
      continue;
    }
    if (['.js', '.jsx', '.ts', '.tsx'].includes(extname(entry.name))) {
      chunks.push(await readFile(path, 'utf8'));
    }
  }
  return chunks.join('\n');
}

async function verifyGuard(guard) {
  if (guard !== 'react-router-client-only-vite-spa') {
    throw new Error(`Unknown dependency audit exception guard: ${guard}`);
  }
  const packageJSON = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));
  assert(packageJSON.scripts?.build?.includes('vite build apps/hvac-web'), 'React Router exception requires the client-only Vite build');
  const main = await readFile(resolve(root, 'apps/hvac-web/src/main.tsx'), 'utf8');
  assert(main.includes('BrowserRouter'), 'React Router exception requires BrowserRouter');
  const source = await collectSource(resolve(root, 'apps/hvac-web/src'));
  for (const forbidden of [
    'createStaticRouter',
    'ServerRouter',
    'HydratedRouter',
    'createRequestHandler',
    'unstable_RSC',
    '@react-router/node',
    '@react-router/serve',
    'react-router/dom-export',
    'react-router/server',
  ]) {
    assert(!source.includes(forbidden), `React Router exception invalidated by server/RSC marker: ${forbidden}`);
  }
}

for (const [name, project] of Object.entries(baseline.projects)) {
  const args = ['audit', '--omit=dev', '--json'];
  if (project.path !== '.') args.push('--prefix', project.path);
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
      lastAuditFailure = JSON.stringify(candidate.error ?? candidate);
    } catch {
      lastAuditFailure = result.stderr || result.stdout;
    }
    if (attempt < auditAttempts) {
      console.warn(`${name} npm audit attempt ${attempt} returned no vulnerability metadata; retrying`);
      await pause(attempt * 1000);
    }
  }
  if (!report) throw new Error(`${name} npm audit did not return complete vulnerability metadata after ${auditAttempts} attempts: ${lastAuditFailure}`);
  const counts = report.metadata.vulnerabilities;
  if (Number(counts.critical || 0) !== 0) throw new Error(`${name} has ${counts.critical} production critical vulnerabilities`);

  const effectiveCounts = Object.fromEntries(severities.map((severity) => [severity, Number(counts[severity] || 0)]));
  const exceptionPackages = new Set();
  const verifiedGuards = new Set();
  for (const exception of project.exceptions ?? []) {
    assert(!exceptionPackages.has(exception.package), `${name} duplicate dependency exception for ${exception.package}`);
    exceptionPackages.add(exception.package);
    assert(exception.severity !== 'critical', `${name} critical dependency exceptions are forbidden`);
    assert(severities.includes(exception.severity), `${name} exception severity is invalid for ${exception.package}`);
    assert(/^#\d+$/.test(exception.remediationIssue ?? ''), `${name} exception remediation issue is invalid for ${exception.package}`);
    const exceptionExpiry = new Date(`${exception.expiresOn}T23:59:59Z`);
    assert(Number.isFinite(exceptionExpiry.getTime()) && Date.now() <= exceptionExpiry.getTime(), `${name} exception expired for ${exception.package} on ${exception.expiresOn}`);

    const vulnerability = report.vulnerabilities?.[exception.package];
    assert(vulnerability, `${name} exception package is no longer present and must be removed: ${exception.package}`);
    assert(vulnerability.severity === exception.severity, `${name} exception severity drifted for ${exception.package}`);
    const via = Array.isArray(vulnerability.via) ? vulnerability.via : [];
    const advisoryEntries = via.filter((entry) => entry && typeof entry === 'object' && entry.severity === exception.severity);
    const advisorySources = advisoryEntries.map((entry) => Number(entry.source));
    const advisoryURLs = advisoryEntries.map((entry) => String(entry.url));
    const viaPackages = via.filter((entry) => typeof entry === 'string');
    if (exception.advisorySources) {
      assert(exactArray(advisorySources, exception.advisorySources), `${name} advisory set drifted for ${exception.package}`);
    }
    if (exception.advisoryUrls) {
      assert(exactArray(advisoryURLs, exception.advisoryUrls), `${name} advisory URL set drifted for ${exception.package}`);
    }
    if (exception.viaPackages) {
      assert(exactArray(viaPackages, exception.viaPackages), `${name} transitive advisory path drifted for ${exception.package}`);
    }
    if (exception.guard && !verifiedGuards.has(exception.guard)) {
      await verifyGuard(exception.guard);
      verifiedGuards.add(exception.guard);
    }
    assert(effectiveCounts[exception.severity] > 0, `${name} exception count underflow for ${exception.package}`);
    effectiveCounts[exception.severity] -= 1;
    console.log(`${name} exact dependency exception active: package=${exception.package}, severity=${exception.severity}, expires=${exception.expiresOn}, issue=${exception.remediationIssue}`);
  }

  for (const severity of severities) {
    const actual = effectiveCounts[severity];
    const allowed = Number(project.allowed?.[severity] || 0);
    if (actual > allowed) throw new Error(`${name} production ${severity} vulnerabilities increased from ${allowed} to ${actual} after exact exceptions`);
  }
  console.log(`${name} production audit within baseline after exact exceptions: ${severities.map((severity) => `${severity}=${effectiveCounts[severity]}`).join(', ')}`);
}
console.log(`Production dependency baseline is valid through ${baseline.expiresOn}; remediation owner: ${baseline.primaryOwner}.`);

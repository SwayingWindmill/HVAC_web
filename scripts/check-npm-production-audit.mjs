import { spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const baseline = JSON.parse(await readFile(resolve(root, 'deploy/s0/security/dependency-audit-baseline.json'), 'utf8'));
const expiry = new Date(`${baseline.expiresOn}T23:59:59Z`);
if (!Number.isFinite(expiry.getTime()) || Date.now() > expiry.getTime()) {
  throw new Error(`Dependency audit baseline expired on ${baseline.expiresOn}; ticket ${baseline.remediationIssue} must refresh or remove it`);
}

const severities = ['low', 'moderate', 'high', 'critical'];
for (const [name, project] of Object.entries(baseline.projects)) {
  const args = ['audit', '--omit=dev', '--json'];
  if (project.path !== '.') args.push('--prefix', project.path);
  const npmCLI = process.env.npm_execpath || resolve(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js');
  const result = spawnSync(process.execPath, [npmCLI, ...args], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (result.error) throw result.error;
  let report;
  try {
    report = JSON.parse(result.stdout);
  } catch {
    throw new Error(`${name} npm audit did not return JSON: ${result.stderr || result.stdout}`);
  }
  const counts = report.metadata?.vulnerabilities;
  if (!counts) throw new Error(`${name} npm audit response has no vulnerability metadata`);
  if (Number(counts.critical || 0) !== 0) throw new Error(`${name} has ${counts.critical} production critical vulnerabilities`);
  for (const severity of severities) {
    const actual = Number(counts[severity] || 0);
    const allowed = Number(project.allowed?.[severity] || 0);
    if (actual > allowed) throw new Error(`${name} production ${severity} vulnerabilities increased from ${allowed} to ${actual}`);
  }
  console.log(`${name} production audit within baseline: ${severities.map((severity) => `${severity}=${counts[severity] || 0}`).join(', ')}`);
}
console.log(`Production dependency baseline is valid through ${baseline.expiresOn}; remediation owner: ${baseline.primaryOwner}.`);

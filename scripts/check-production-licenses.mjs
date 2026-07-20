import { spawnSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const npmCLI = process.env.npm_execpath || resolve(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js');
const projects = [
  { name: 'root', start: root },
];

function runLicenseChecker(project) {
  const result = spawnSync(process.execPath, [
    npmCLI,
    'exec',
    '--yes',
    '--package=license-checker-rseidelsohn@4.4.2',
    '--',
    'license-checker-rseidelsohn',
    '--production',
    '--json',
    '--start',
    project.start,
  ], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(`${project.name} license inventory failed: ${result.stderr || result.stdout || result.status}`);
  }
  try {
    return JSON.parse(result.stdout);
  } catch {
    throw new Error(`${project.name} license inventory did not return JSON: ${result.stderr || result.stdout.slice(0, 1000)}`);
  }
}

function containsForbiddenLicense(expression) {
  const value = String(expression || '').toUpperCase();
  if (/\bAGPL-3\.0(?:-ONLY|-OR-LATER)?\b/.test(value)) return true;
  return /(?<!L)\bGPL-3\.0(?:-ONLY|-OR-LATER)?\b/.test(value);
}

for (const project of projects) {
  const inventory = runLicenseChecker(project);
  const packages = Object.entries(inventory);
  const forbidden = packages
    .filter(([, metadata]) => containsForbiddenLicense(metadata?.licenses))
    .map(([name, metadata]) => `${name} (${metadata?.licenses || 'unknown'})`)
    .sort();
  if (forbidden.length > 0) {
    throw new Error(`${project.name} production dependency licenses are forbidden: ${forbidden.join(', ')}`);
  }
  const uniqueLicenses = [...new Set(packages.map(([, metadata]) => String(metadata?.licenses || 'UNKNOWN')))].sort();
  console.log(`${project.name} production license gate passed: packages=${packages.length}, uniqueLicenses=${uniqueLicenses.length}`);
}

console.log('Production dependency license policy passed; AGPL-3.0 and GPL-3.0 are absent.');

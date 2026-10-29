import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

import { readGoWorkspace } from './lib/go-workspace.mjs';

const root = resolve(import.meta.dirname, '..');
export const baselinePath = 'scripts/coverage-baseline.json';
export const tolerancePoints = 0.1;

export const roundPoints = (value) => Math.round(value * 100) / 100;

export function statementPercent(profile) {
  let covered = 0;
  let total = 0;
  for (const line of profile.split('\n')) {
    if (!line || line.startsWith('mode:')) continue;
    const parts = line.trim().split(/\s+/);
    if (parts.length < 3) continue;
    const statements = Number(parts[1]);
    const count = Number(parts[2]);
    if (!Number.isFinite(statements) || !Number.isFinite(count)) continue;
    total += statements;
    if (count > 0) covered += statements;
  }
  if (total === 0) return 0;
  return roundPoints((covered / total) * 100);
}

export function regressed(current, baseline) {
  return roundPoints(current) < roundPoints(baseline) - tolerancePoints;
}

function goPercent() {
  const { modules } = readGoWorkspace(root);
  let covered = 0;
  let total = 0;
  for (const modulePath of modules) {
    const dir = mkdtempSync(join(tmpdir(), 'hvac-cover-'));
    const profile = join(dir, 'cover.out');
    console.log(`=== go coverage ${modulePath} ===`);
    const result = spawnSync('go', ['test', '-count=1', `-coverprofile=${profile}`, './...'], {
      cwd: join(root, modulePath),
      stdio: 'inherit',
      env: { ...process.env, GOFLAGS: '' },
    });
    if (result.status !== 0) {
      rmSync(dir, { recursive: true, force: true });
      console.error(`go coverage failed in ${modulePath}`);
      process.exit(result.status ?? 1);
    }
    let text = '';
    try {
      text = readFileSync(profile, 'utf8');
    } catch {
      text = '';
    }
    rmSync(dir, { recursive: true, force: true });
    for (const line of text.split('\n')) {
      if (!line || line.startsWith('mode:')) continue;
      const parts = line.trim().split(/\s+/);
      if (parts.length < 3) continue;
      const statements = Number(parts[1]);
      const count = Number(parts[2]);
      if (!Number.isFinite(statements) || !Number.isFinite(count)) continue;
      total += statements;
      if (count > 0) covered += statements;
    }
  }
  if (total === 0) return 0;
  return roundPoints((covered / total) * 100);
}

function nodeTestArgs(script) {
  const command = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).scripts[script];
  const body = command.replace(/^node\s+/, '');
  return body.split(/\s+/).filter((part) => part !== '--test' && !part.startsWith('--test-concurrency'));
}

function tsPercent() {
  const args = [
    '--test',
    '--test-concurrency=1',
    '--experimental-test-coverage',
    '--test-coverage-include=apps/hvac-web/src/**',
    '--test-reporter=spec',
    ...nodeTestArgs('web:shell:test'),
    ...nodeTestArgs('web:model:test'),
  ];
  console.log('=== typescript coverage apps/hvac-web/src ===');
  const result = spawnSync(process.execPath, args, { cwd: root, encoding: 'utf8' });
  const output = `${result.stdout ?? ''}\n${result.stderr ?? ''}`;
  process.stdout.write(result.stdout ?? '');
  process.stderr.write(result.stderr ?? '');
  if (result.status !== 0) process.exit(result.status ?? 1);
  const match = output.match(/all files\s*\|\s*([0-9.]+)/);
  if (!match) {
    console.error('typescript coverage report had no "all files" line');
    process.exit(1);
  }
  return roundPoints(Number(match[1]));
}

function main() {
  const write = process.argv.includes('--write');
  const current = { goStatements: goPercent(), tsLines: tsPercent() };
  const absolute = resolve(root, baselinePath);
  if (write) {
    writeFileSync(absolute, `${JSON.stringify(current, null, 2)}\n`);
    console.log(`wrote ${baselinePath}: ${JSON.stringify(current)}`);
    return;
  }
  const baseline = JSON.parse(readFileSync(absolute, 'utf8'));
  const failures = [];
  for (const key of ['goStatements', 'tsLines']) {
    if (regressed(current[key], baseline[key])) {
      failures.push(`${key} ${current[key]} is below baseline ${baseline[key]} (tolerance ${tolerancePoints})`);
    } else {
      console.log(`${key}: ${current[key]} (baseline ${baseline[key]})`);
    }
  }
  if (failures.length > 0) {
    console.error(failures.join('\n'));
    process.exit(1);
  }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  main();
}

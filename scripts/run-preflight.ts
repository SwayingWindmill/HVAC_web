import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export interface PreflightClassification {
  contracts: boolean;
  unitGoLint: boolean;
  unitWeb: boolean;
  unitDomain: boolean;
  contractProfiles: string[];
  unitProfiles: string[];
  unitDomainProfiles: string[];
  broad: boolean;
  unknown: boolean;
}

export interface PreflightCommand {
  label: string;
  command: string;
  args: string[];
}

const normalize = (value: string): string => value.replaceAll('\\', '/').replace(/^\.\//u, '').trim();

function cleanGitEnv(): NodeJS.ProcessEnv {
  const env = { ...process.env };
  delete env.GIT_DIR;
  delete env.GIT_WORK_TREE;
  return env;
}

function parsePorcelainStatus(stdout: string): string[] {
  const paths: string[] = [];
  for (const rawLine of stdout.split(/\r?\n/u)) {
    if (rawLine.length < 4) continue;
    let body = rawLine.slice(3).trim();
    if (body.includes(' -> ')) {
      body = body.split(' -> ').at(-1)?.trim() ?? body;
    }
    if (body.startsWith('"') && body.endsWith('"')) {
      body = body.slice(1, -1);
    }
    const normalized = normalize(body);
    if (normalized) paths.push(normalized);
  }
  return paths;
}

export function collectChangedFilesForPreflight(root: string, base = 'origin/main'): string[] {
  const env = cleanGitEnv();
  const diffResult = spawnSync('git', ['diff', '--name-only', '--diff-filter=ACDMRTUXB', `${base}...HEAD`], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    env,
  });
  if (diffResult.error || diffResult.status !== 0) {
    throw new Error(`git diff against ${base} failed: ${diffResult.error?.message ?? diffResult.stderr.trim()}`);
  }

  const statusResult = spawnSync('git', ['status', '--porcelain'], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    env,
  });
  if (statusResult.error || statusResult.status !== 0) {
    throw new Error(`git status --porcelain failed: ${statusResult.error?.message ?? statusResult.stderr.trim()}`);
  }

  const files = new Set<string>();
  for (const line of diffResult.stdout.split(/\r?\n/u)) {
    const normalized = normalize(line);
    if (normalized) files.add(normalized);
  }
  for (const path of parsePorcelainStatus(statusResult.stdout)) {
    files.add(path);
  }
  return [...files].sort();
}

export function classifyFilesForPreflight(root: string, files: readonly string[]): PreflightClassification {
  const scratch = mkdtempSync(join(tmpdir(), 'hvac-preflight-'));
  try {
    const filesListPath = join(scratch, 'changed-files.txt');
    writeFileSync(filesListPath, `${files.join('\n')}\n`, 'utf8');
    const result = spawnSync(
      process.execPath,
      ['--experimental-strip-types', 'scripts/classify-pr-gates.ts', `--files-file=${filesListPath}`],
      { cwd: root, encoding: 'utf8', windowsHide: true },
    );
    if (result.error || result.status !== 0) {
      throw new Error(`classify-pr-gates failed: ${result.error?.message ?? result.stderr.trim()}`);
    }
    const outPath = join(root, 'out/pr-gates/classification.json');
    const jsonText = result.stdout.trim() || readFileSync(outPath, 'utf8');
    return JSON.parse(jsonText) as PreflightClassification;
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

export function buildPreflightPlan(
  classification: PreflightClassification,
  options: { skipE2e?: boolean } = {},
): PreflightCommand[] {
  const commands: PreflightCommand[] = [
    { label: 'npm run check', command: 'npm', args: ['run', 'check'] },
  ];

  if (classification.contracts && classification.contractProfiles.length > 0) {
    commands.push({
      label: `contracts (${classification.contractProfiles.join(',')})`,
      command: process.execPath,
      args: [
        '--experimental-strip-types',
        'scripts/run-pr-gate.ts',
        '--gate=contracts',
        `--profiles=${classification.contractProfiles.join(',')}`,
      ],
    });
  }

  const unitProfiles: string[] = [];
  if (classification.unitGoLint) unitProfiles.push('go');
  if (classification.unitDomain) unitProfiles.push(...classification.unitDomainProfiles);
  if (classification.unitWeb && !options.skipE2e) unitProfiles.push('web');

  const uniqueUnitProfiles = [...new Set(unitProfiles)];
  if (uniqueUnitProfiles.length > 0) {
    commands.push({
      label: `unit (${uniqueUnitProfiles.join(',')})`,
      command: process.execPath,
      args: [
        '--experimental-strip-types',
        'scripts/run-pr-gate.ts',
        '--gate=unit',
        `--profiles=${uniqueUnitProfiles.join(',')}`,
      ],
    });
  }

  return commands;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const root = resolve(process.cwd());
  const args = process.argv.slice(2);
  const dryRun = args.includes('--dry-run');
  const skipE2e = args.includes('--skip-e2e');
  const baseArg = args.find((arg) => arg.startsWith('--base='));
  const filesArg = args.find((arg) => arg.startsWith('--files='));

  const changedFiles = filesArg
    ? filesArg.slice('--files='.length).split(',').map(normalize).filter(Boolean)
    : collectChangedFilesForPreflight(root, baseArg ? baseArg.slice('--base='.length) : 'origin/main');

  const classification = classifyFilesForPreflight(root, changedFiles);
  const plan = buildPreflightPlan(classification, { skipE2e });

  console.log(`Preflight base=origin/main changedFiles=${changedFiles.length} broad=${classification.broad}`);
  if (changedFiles.length > 0) {
    console.log(`Changed paths (${Math.min(changedFiles.length, 20)} shown): ${changedFiles.slice(0, 20).join(', ')}`);
  }

  for (const step of plan) {
    console.log(`\n=== ${step.label} ===`);
    if (dryRun) {
      console.log(`[dry-run] ${step.command} ${step.args.join(' ')}`);
      continue;
    }
    const result = spawnSync(step.command, step.args, { cwd: root, stdio: 'inherit', windowsHide: true });
    if (result.error || result.status !== 0) {
      process.exit(result.status ?? 1);
    }
  }
}

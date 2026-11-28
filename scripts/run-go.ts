import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

import { readGoWorkspace } from './lib/go-workspace.ts';

const root = resolve(process.cwd());
const goBinary = process.env.GO_BINARY ?? 'go';
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const args = process.argv.slice(2);
const modulePattern = /^\.\/((?:cmd|libs|modules|services|tools)\/[^/]+)\/\.\.\.$/;
const categoryPattern = /^\.\/(cmd|libs|modules|services|tools)\/\.\.\.$/;

if (args.length === 0) {
  console.error('usage: node --experimental-strip-types scripts/run-go.ts <go arguments...>');
  process.exit(2);
}

await Promise.all([
  mkdir(join(root, 'out'), { recursive: true }),
  mkdir(goCacheDir, { recursive: true }),
]);

const configuredModules = readGoWorkspace(root).modules.filter((entry) => entry !== '.');

async function runGo(commandArgs: string[], cwd: string): Promise<number> {
  const child = spawn(goBinary, commandArgs, {
    cwd,
    stdio: 'inherit',
    shell: false,
    env: { ...process.env, GOCACHE: goCacheDir },
  });
  const [code, signal] = await once(child, 'exit') as [number | null, NodeJS.Signals | null];
  if (signal) {
    console.error(`go terminated by ${signal}`);
    return 1;
  }
  return code ?? 1;
}

const baseArgs: string[] = [];
const rootPackages: string[] = [];
const workspaceModules: string[] = [];
const addUnique = (list: string[], item: string): void => {
  if (!list.includes(item)) list.push(item);
};
for (const argument of args) {
  const moduleMatch = modulePattern.exec(argument);
  if (moduleMatch) {
    if (configuredModules.includes(moduleMatch[1])) addUnique(workspaceModules, moduleMatch[1]);
    else addUnique(rootPackages, argument);
    continue;
  }
  const categoryMatch = categoryPattern.exec(argument);
  if (categoryMatch) {
    addUnique(rootPackages, argument);
    for (const modulePath of configuredModules.filter((entry) => entry.startsWith(`${categoryMatch[1]}/`))) addUnique(workspaceModules, modulePath);
    continue;
  }
  baseArgs.push(argument);
}

if (rootPackages.length === 0 && workspaceModules.length === 0) {
  process.exitCode = await runGo(args, root);
} else {
  if (rootPackages.length > 0) {
    const code = await runGo([...baseArgs, ...rootPackages], root);
    if (code !== 0) process.exit(code);
  }
  for (const modulePath of workspaceModules) {
    console.log(`=== go ${baseArgs.join(' ')} ./... (${modulePath}) ===`);
    const code = await runGo([...baseArgs, './...'], resolve(root, modulePath));
    if (code !== 0) {
      process.exitCode = code;
      break;
    }
  }
}

import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdir, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const windowsGoPath = 'C:\\Program Files\\Go\\bin\\go.exe';
const goBinary = process.env.GO_BINARY ?? (process.platform === 'win32' && existsSync(windowsGoPath) ? windowsGoPath : 'go');
const goCacheDir = process.env.GOCACHE || join(tmpdir(), 'hvac-go-build-cache');
const args = process.argv.slice(2);
const modulePattern = /^\.\/((?:cmd|libs|modules|services|tools)\/[^/]+)\/\.\.\.$/;
const categoryPattern = /^\.\/(cmd|libs|modules|services|tools)\/\.\.\.$/;

if (args.length === 0) {
  console.error('usage: node scripts/run-go.mjs <go arguments...>');
  process.exit(2);
}

await Promise.all([
  mkdir(join(root, 'out'), { recursive: true }),
  mkdir(goCacheDir, { recursive: true }),
]);

const workspace = await readFile(join(root, 'go.work'), 'utf8');
const configuredModules = [...workspace.matchAll(/^\s*\.\/((?:cmd|libs|modules|services|tools)\/[^\s)]+)\s*$/gm)]
  .map((match) => match[1]);

async function runGo(commandArgs, cwd) {
  const child = spawn(goBinary, commandArgs, {
    cwd,
    stdio: 'inherit',
    shell: false,
    env: { ...process.env, GOCACHE: goCacheDir },
  });
  const [code, signal] = await once(child, 'exit');
  if (signal) {
    console.error(`go terminated by ${signal}`);
    return 1;
  }
  return code ?? 1;
}

const workspaceModules = [];
const rootArgs = [];
const addModule = (modulePath) => {
  if (!workspaceModules.includes(modulePath)) workspaceModules.push(modulePath);
};
for (const argument of args) {
  const moduleMatch = modulePattern.exec(argument);
  if (moduleMatch) {
    addModule(moduleMatch[1]);
    continue;
  }
  const categoryMatch = categoryPattern.exec(argument);
  if (categoryMatch) {
    for (const modulePath of configuredModules.filter((entry) => entry.startsWith(`${categoryMatch[1]}/`))) addModule(modulePath);
    continue;
  }
  rootArgs.push(argument);
}

if (workspaceModules.length === 0) {
  process.exitCode = await runGo(args, root);
} else {
  for (const modulePath of workspaceModules) {
    console.log(`=== go ${rootArgs.join(' ')} ./... (${modulePath}) ===`);
    const code = await runGo([...rootArgs, './...'], resolve(root, modulePath));
    if (code !== 0) {
      process.exitCode = code;
      break;
    }
  }
}

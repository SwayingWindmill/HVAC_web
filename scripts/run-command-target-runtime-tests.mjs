import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const configuredParallelism = process.env.COMMAND_TARGET_GO_MAX_PROCS ?? '2';
const existingFlags = String(process.env.GOFLAGS ?? '').trim();
const goFlags = /(^|\s)-p(?:=|\s)/.test(existingFlags)
  ? existingFlags
  : `${existingFlags} -p=${configuredParallelism}`.trim();
const environment = {
  ...process.env,
  GOMAXPROCS: configuredParallelism,
  GOFLAGS: goFlags,
};

const modules = [
  './libs/commandmodel/...',
  './libs/workloadtls/...',
  './modules/command/...',
  './cmd/connectivity/...',
  './modules/telemetry/...',
];
const commands = [
  [process.execPath, ['scripts/run-go.mjs', 'test', ...modules]],
  [process.execPath, ['scripts/run-go.mjs', 'vet', ...modules]],
  [process.execPath, ['scripts/run-go.mjs', 'build', '-o', 'out/command-owner', './modules/command/cmd/command-owner']],
  [process.execPath, ['scripts/run-go.mjs', 'build', '-o', 'out/connectivity', './cmd/connectivity']],
  [process.execPath, ['scripts/run-go.mjs', 'build', '-o', 'out/telemetry-worker', './cmd/telemetry-worker']],
];

for (const [command, args] of commands) {
  const result = spawnSync(command, args, {
    cwd: root,
    env: environment,
    stdio: 'inherit',
    windowsHide: true,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

console.log(`Command target runtime tests passed with Go parallelism=${configuredParallelism}`);

import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const configuredParallelism = process.env.S3_TARGET_GO_MAX_PROCS ?? '2';
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
  './cmd/iot-service/...',
  './services/thingsboard-connector-control/...',
  './modules/telemetry/...',
];
const commands = [
  [process.execPath, ['scripts/check-s3-target-runtime.mjs']],
  [process.execPath, ['scripts/run-go.mjs', 'test', ...modules]],
  [process.execPath, ['scripts/run-go.mjs', 'vet', ...modules]],
  [process.execPath, ['scripts/run-go.mjs', 'build', '-o', 'out/command-owner', './modules/command/cmd/command-owner']],
  [process.execPath, ['scripts/run-go.mjs', 'build', '-o', 'out/iot-service', './cmd/iot-service']],
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

console.log(`S3 target runtime tests passed with Go parallelism=${configuredParallelism}`);

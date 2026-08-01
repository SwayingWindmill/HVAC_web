import { spawnSync } from 'node:child_process';

import { capabilityTaskMatrix, resolveCapabilityTask } from './domain-task-matrix.mjs';

const argumentsMap = new Map(
  process.argv.slice(2).map((argument) => {
    const separator = argument.indexOf('=');
    return separator === -1
      ? [argument, 'true']
      : [argument.slice(0, separator), argument.slice(separator + 1)];
  }),
);

const task = argumentsMap.get('--task');
const dryRun = argumentsMap.get('--dry-run') === 'true';
const list = argumentsMap.get('--list') === 'true';

if (list) {
  process.stdout.write(`${JSON.stringify({
    schemaVersion: 1,
    tasks: Object.keys(capabilityTaskMatrix),
  })}\n`);
  process.exit(0);
}

if (!task) {
  throw new Error(`Missing --task. Supported tasks: ${Object.keys(capabilityTaskMatrix).join(', ')}`);
}

const commands = resolveCapabilityTask(task);
const plan = {
  schemaVersion: 1,
  task,
  commands: commands.map(({ label }) => label),
};

if (dryRun) {
  process.stdout.write(`${JSON.stringify(plan)}\n`);
  process.exit(0);
}

console.log(`Capability task ${task}`);
for (const command of commands) {
  console.log(`\n=== ${command.label} ===`);
  const result = spawnSync(command.command, command.args, {
    cwd: process.cwd(),
    env: process.env,
    stdio: 'inherit',
    windowsHide: true,
    shell: command.shell,
  });
  if (result.error || result.status !== 0) {
    throw result.error ?? new Error(`${command.label} failed with status ${result.status}`);
  }
}

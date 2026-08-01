import { spawnSync } from 'node:child_process';

import {
  gateCommandMatrix,
  resolveGateCommands,
  resolveGateProfileSet,
} from './domain-task-matrix.mjs';

const argumentsMap = new Map(
  process.argv.slice(2).map((argument) => {
    const separator = argument.indexOf('=');
    return separator === -1
      ? [argument, 'true']
      : [argument.slice(0, separator), argument.slice(separator + 1)];
  }),
);

const gate = argumentsMap.get('--gate');
const explicitProfiles = [...new Set((argumentsMap.get('--profiles') ?? '')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean))];
const profileSet = argumentsMap.get('--profile-set');
const dryRun = argumentsMap.get('--dry-run') === 'true';

if (!gate || !gateCommandMatrix[gate]) {
  throw new Error(`Unsupported PR gate: ${gate ?? '<missing>'}`);
}
if (profileSet && explicitProfiles.length > 0) {
  throw new Error('Use either --profiles or --profile-set, not both.');
}

const profiles = profileSet
  ? resolveGateProfileSet(gate, profileSet)
  : explicitProfiles;
const commands = resolveGateCommands(gate, profiles);
const plan = {
  schemaVersion: 1,
  gate,
  profileSet: profileSet ?? null,
  profiles,
  commands: commands.map(({ label }) => label),
};

if (dryRun) {
  process.stdout.write(`${JSON.stringify(plan)}\n`);
  process.exit(0);
}

if (commands.length === 0) {
  console.log(`PR gate ${gate}: no affected profiles.`);
  process.exit(0);
}

console.log(`PR gate ${gate}: ${profiles.join(', ') || 'default'}`);
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

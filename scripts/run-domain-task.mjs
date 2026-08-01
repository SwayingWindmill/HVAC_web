import { spawnSync } from 'node:child_process';

import { domainTaskProfiles, resolveDomainCommands } from './domain-task-matrix.mjs';

const argumentsMap = new Map(
  process.argv.slice(2).map((argument) => {
    const separator = argument.indexOf('=');
    return separator === -1
      ? [argument, 'true']
      : [argument.slice(0, separator), argument.slice(separator + 1)];
  }),
);

const domain = argumentsMap.get('--domain');
const layers = [...new Set((argumentsMap.get('--layers') ?? 'unit')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean))];
const dryRun = argumentsMap.get('--dry-run') === 'true';
const list = argumentsMap.get('--list') === 'true';

if (list) {
  process.stdout.write(`${JSON.stringify({
    schemaVersion: 1,
    domains: Object.fromEntries(
      Object.entries(domainTaskProfiles).map(([name, profiles]) => [name, Object.keys(profiles)]),
    ),
  })}\n`);
  process.exit(0);
}

if (!domain) {
  throw new Error(`Missing --domain. Supported domains: ${Object.keys(domainTaskProfiles).join(', ')}`);
}

const resolved = resolveDomainCommands(domain, layers);
const plan = {
  schemaVersion: resolved.schemaVersion,
  domain: resolved.domain,
  layers: resolved.layers,
  profiles: resolved.profiles,
  commands: resolved.commands.map(({ label }) => label),
};

if (dryRun) {
  process.stdout.write(`${JSON.stringify(plan)}\n`);
  process.exit(0);
}

if (resolved.commands.length === 0) {
  console.log(`Domain task ${domain}: no commands for ${layers.join(', ')}.`);
  process.exit(0);
}

console.log(`Domain task ${domain}: ${layers.join(', ')}`);
for (const command of resolved.commands) {
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

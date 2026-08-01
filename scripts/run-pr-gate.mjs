import { spawnSync } from 'node:child_process';

const argumentsMap = new Map(
  process.argv.slice(2).map((argument) => {
    const separator = argument.indexOf('=');
    return separator === -1 ? [argument, 'true'] : [argument.slice(0, separator), argument.slice(separator + 1)];
  }),
);

const gate = argumentsMap.get('--gate');
const profiles = [...new Set((argumentsMap.get('--profiles') ?? '').split(',').map((value) => value.trim()).filter(Boolean))];
const dryRun = argumentsMap.get('--dry-run') === 'true';
const npmCommand = (args, label) => process.platform === 'win32'
  ? { command: process.env.ComSpec || 'cmd.exe', args: ['/d', '/s', '/c', `npm ${args.join(' ')}`], label }
  : { command: 'npm', args, label };
const npmRun = (script) => npmCommand(['run', '--silent', script], `npm run ${script}`);
const npmCi = (prefix) => npmCommand(['--prefix', prefix, 'ci'], `npm --prefix ${prefix} ci`);
const nodeRun = (...args) => ({ command: process.execPath, args, label: `node ${args.join(' ')}` });

const commandSets = {
  static: {
    default: [
      npmRun('lint'),
      npmRun('rms:topology:check'),
      npmRun('s1:topology:check'),
      npmRun('s2:topology:check'),
      npmRun('s3:topology:check'),
      npmRun('build:demo'),
    ],
  },
  contracts: {
    core: [npmRun('contracts:check'), npmRun('ownership:check')],
    rms: [npmRun('rms:topology:check')],
    s1: [npmRun('s1:topology:check'), npmRun('s1:registry:check')],
    s2: [npmRun('s2:topology:check'), npmRun('s2:contracts:check')],
    s3: [npmRun('s3:topology:check'), npmRun('s3:baseline:check')],
  },
  unit: {
    web: [npmRun('rms:trusted-shell:test'), npmRun('web:energy:test')],
    s0: [npmRun('test:identity'), npmRun('test:durable-unit')],
    s1: [npmRun('test:registry-routing'), npmRun('test:legacy-migration')],
    s2: [nodeRun('scripts/run-go.mjs', 'test', './libs/telemetryauth/...', './services/telemetry-runtime-service/...', './services/telemetry-shadow-comparator/...', './services/platform-gateway/...')],
    s3: [nodeRun('scripts/run-go.mjs', 'test', './libs/commandauth/...', './libs/commandmodel/...', './services/command-service/...', './services/command-dispatcher/...', './services/thingsboard-connector-control/...')],
    analytics: [npmRun('test:analytics'), npmRun('test:analytics-gateway')],
    'operations-agent': [
      npmCi('services/operations-agent-service'),
      npmRun('operations-agent-service:check'),
      npmRun('operations-agent:benchmark:test'),
      npmRun('operations-agent:gateway:check'),
      npmRun('operations-workspace:test'),
      npmRun('test:gateway'),
    ],
    pocs: [npmRun('pocs:components:check')],
  },
  integration: {
    s0: [npmRun('test:durable-postgres')],
    s1: [npmRun('s1:registry:postgres')],
    's2-baseline': [npmRun('s2:postgres')],
    's2-ingest': [npmRun('s2:ingest:postgres')],
    's2-realtime': [npmRun('s2:realtime:postgres')],
    's2-history': [npmRun('s2:history:integration')],
    s3: [npmRun('s3:postgres')],
    analytics: [npmRun('analytics:history:integration')],
    'operations-agent': [npmCi('services/operations-agent-service'), npmRun('operations-agent-service:postgres')],
  },
  browser: {
    rms: [npmRun('rms:web-browser')],
    'operations-agent': [npmRun('operations-workspace:browser')],
    s0: [npmRun('audit:security-failure')],
    s1: [npmRun('audit:s1-registry-web')],
    s2: [npmRun('s2:hvac-web:browser')],
  },
};

if (!gate || !commandSets[gate]) throw new Error(`Unsupported PR gate: ${gate ?? '<missing>'}`);

const selected = gate === 'static'
  ? commandSets.static.default
  : profiles.flatMap((profile) => {
      const commands = commandSets[gate][profile];
      if (!commands) throw new Error(`Unsupported ${gate} profile: ${profile}`);
      return commands;
    });

const deduplicated = [];
const seen = new Set();
for (const command of selected) {
  const key = `${command.command}\0${command.args.join('\0')}`;
  if (seen.has(key)) continue;
  seen.add(key);
  deduplicated.push(command);
}

const plan = {
  schemaVersion: 1,
  gate,
  profiles,
  commands: deduplicated.map(({ label }) => label),
};

if (dryRun) {
  process.stdout.write(`${JSON.stringify(plan)}\n`);
  process.exit(0);
}

if (deduplicated.length === 0) {
  console.log(`PR gate ${gate}: no affected profiles.`);
  process.exit(0);
}

console.log(`PR gate ${gate}: ${profiles.join(', ') || 'default'}`);
for (const command of deduplicated) {
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

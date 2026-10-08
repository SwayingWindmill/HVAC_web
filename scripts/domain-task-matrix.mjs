const npmCommand = (args, label) => ({ command: 'npm', args, label });

export const npmRun = (script) => npmCommand(['run', '--silent', script], `npm run ${script}`);
export const npmCi = (prefix) => npmCommand(['--prefix', prefix, 'ci'], `npm --prefix ${prefix} ci`);
export const nodeRun = (...args) => ({ command: process.execPath, args, label: `node ${args.join(' ')}` });

export const gateCommandMatrix = Object.freeze({
  static: Object.freeze({
    default: Object.freeze([
      npmRun('repo:check'),
      npmRun('design:check'),
      npmRun('lint'),
      npmRun('build'),
    ]),
  }),
  contracts: Object.freeze({
    core: Object.freeze([
      npmRun('contracts:check'),
      npmRun('ownership:check'),
      npmRun('events:check'),
      npmRun('deployment:phase1:check'),
      npmRun('acceptance:phase1:check'),
      npmRun('docs:phase1:consistency:check'),
      npmRun('architecture:phase1:check'),
      npmRun('backend:architecture:check'),
      npmRun('data:architecture:check'),
      npmRun('observability:phase1:check'),
      npmRun('security:network-policies'),
      npmRun('notification:check'),
      npmRun('analytics:history:check'),
    ]),
    web: Object.freeze([npmRun('web:principal:contract')]),
    registry: Object.freeze([nodeRun('scripts/check-s1-registry-baseline.mjs')]),
    telemetry: Object.freeze([
      nodeRun('scripts/generate-s2-telemetry-contracts.mjs', '--check'),
      nodeRun('scripts/check-s2-telemetry-public-contract.mjs'),
    ]),
    command: Object.freeze([nodeRun('scripts/check-s3-command-gateway.mjs')]),
  }),
  unit: Object.freeze({
    web: Object.freeze([npmRun('web:shell:test'), npmRun('web:model:test'), npmRun('web:e2e')]),
    platform: Object.freeze([npmRun('test:identity'), npmRun('test:durable-unit')]),
    registry: Object.freeze([npmRun('test:registry-routing')]),
    telemetry: Object.freeze([
      nodeRun(
        'scripts/run-go.mjs',
        'test',
        './libs/telemetryauth/...',
        './modules/telemetry/...',
        './cmd/energy-api/...',
      ),
    ]),
    command: Object.freeze([
      nodeRun(
        'scripts/run-go.mjs',
        'test',
        './libs/commandauth/...',
        './libs/commandmodel/...',
        './modules/command/...',
      ),
    ]),
    alarm: Object.freeze([
      nodeRun('scripts/run-go.mjs', 'test', './libs/alarmauth/...', './libs/alarmmodel/...', './modules/alarm/...'),
      npmRun('alarm:test'),
    ]),
    workorder: Object.freeze([
      nodeRun(
        'scripts/run-go.mjs',
        'test',
        './libs/workorderauth/...',
        './libs/workordermodel/...',
        './modules/iam/...',
        './modules/workorder/...',
        './cmd/energy-api/...',
      ),
    ]),
    analytics: Object.freeze([npmRun('test:analytics'), npmRun('test:analytics-gateway')]),
    'operations-agent': Object.freeze([
      npmCi('services/operations-agent-service'),
      npmRun('operations-agent-service:check'),
      nodeRun('scripts/check-operations-agent-service-boundaries.mjs'),
      npmRun('operations-agent:benchmark:test'),
      npmRun('operations-agent:gateway:check'),
      npmRun('test:gateway'),
    ]),
    pocs: Object.freeze([npmRun('pocs:components:check')]),
  }),
  integration: Object.freeze({
    platform: Object.freeze([npmRun('test:durable-postgres')]),
    registry: Object.freeze([nodeRun('scripts/run-s1-registry-postgres-tests.mjs')]),
    telemetry: Object.freeze([
      nodeRun('scripts/run-s2-telemetry-postgres-tests.mjs'),
      nodeRun('scripts/run-s2-telemetry-ingest-postgres-tests.mjs'),
      nodeRun('scripts/run-s2-realtime-postgres-tests.mjs'),
      nodeRun('scripts/run-s2-telemetry-history-tests.mjs'),
    ]),
    command: Object.freeze([nodeRun('--experimental-strip-types', 'scripts/run-s3-command-postgres-tests.ts')]),
    alarm: Object.freeze([nodeRun('--experimental-strip-types', 'scripts/run-s4-alarm-postgres-tests.ts')]),
    workorder: Object.freeze([nodeRun('--experimental-strip-types', 'scripts/run-s5-work-order-postgres-tests.ts')]),
    analytics: Object.freeze([
      nodeRun('scripts/run-analytics-history-tests.mjs'),
      nodeRun('scripts/run-analytics-cube-tests.mjs'),
    ]),
    'operations-agent': Object.freeze([
      npmCi('services/operations-agent-service'),
      npmRun('operations-agent-service:postgres'),
    ]),
  }),
  browser: Object.freeze({
    platform: Object.freeze([npmRun('audit:security-failure')]),
    telemetry: Object.freeze([
      nodeRun('scripts/run-s2-telemetry-live-browser-audit.mjs'),
    ]),
    workorder: Object.freeze([
      nodeRun('scripts/run-s5-work-order-read-browser-audit.mjs'),
      nodeRun('scripts/run-s5-work-order-create-assign-browser-audit.mjs'),
      nodeRun('scripts/run-s5-work-order-lifecycle-browser-audit.mjs'),
    ]),
  }),
});

export const gateProfileSets = Object.freeze({
  all: Object.freeze({
    contracts: Object.freeze(['command', 'core', 'registry', 'telemetry', 'web']),
    unit: Object.freeze([
      'alarm',
      'analytics',
      'command',
      'operations-agent',
      'platform',
      'pocs',
      'registry',
      'telemetry',
      'web',
      'workorder',
    ]),
    integration: Object.freeze([
      'alarm',
      'analytics',
      'command',
      'operations-agent',
      'platform',
      'registry',
      'telemetry',
      'workorder',
    ]),
    browser: Object.freeze([
      'platform',
      'telemetry',
      'workorder',
    ]),
  }),
});

const assertExactProfiles = (label, actual, expected) => {
  const normalizedActual = [...actual].sort();
  const normalizedExpected = [...expected].sort();
  if (new Set(actual).size !== actual.length
    || normalizedActual.length !== normalizedExpected.length
    || normalizedActual.some((profile, index) => profile !== normalizedExpected[index])) {
    throw new Error(`${label} must contain every supported profile exactly once.`);
  }
};

for (const gate of ['contracts', 'unit', 'integration', 'browser']) {
  assertExactProfiles(
    `Profile set all.${gate}`,
    gateProfileSets.all[gate],
    Object.keys(gateCommandMatrix[gate]),
  );
}

export const resolveGateProfileSet = (gate, profileSet) => {
  const profiles = gateProfileSets[profileSet]?.[gate];
  if (!profiles) {
    throw new Error(`Unsupported ${gate} profile set: ${profileSet ?? '<missing>'}`);
  }
  return [...profiles];
};

export const domainTaskProfiles = Object.freeze({
  web: Object.freeze({
    contracts: Object.freeze(['web']),
    unit: Object.freeze(['web']),
    integration: Object.freeze([]),
    browser: Object.freeze([]),
  }),
  platform: Object.freeze({
    contracts: Object.freeze(['core']),
    unit: Object.freeze(['platform']),
    integration: Object.freeze(['platform']),
    browser: Object.freeze(['platform']),
  }),
  registry: Object.freeze({
    contracts: Object.freeze(['core', 'registry']),
    unit: Object.freeze(['registry']),
    integration: Object.freeze(['registry']),
    browser: Object.freeze([]),
  }),
  telemetry: Object.freeze({
    contracts: Object.freeze(['core', 'telemetry']),
    unit: Object.freeze(['telemetry']),
    integration: Object.freeze(['telemetry']),
    browser: Object.freeze(['telemetry']),
  }),
  command: Object.freeze({
    contracts: Object.freeze(['core', 'command']),
    unit: Object.freeze(['command']),
    integration: Object.freeze(['command']),
    browser: Object.freeze([]),
  }),
  alarm: Object.freeze({
    contracts: Object.freeze(['core']),
    unit: Object.freeze(['alarm']),
    integration: Object.freeze(['alarm']),
    browser: Object.freeze([]),
  }),
  workorder: Object.freeze({
    contracts: Object.freeze(['core']),
    unit: Object.freeze(['workorder']),
    integration: Object.freeze(['workorder']),
    browser: Object.freeze(['workorder']),
  }),
  analytics: Object.freeze({
    contracts: Object.freeze(['core']),
    unit: Object.freeze(['analytics']),
    integration: Object.freeze(['analytics']),
    browser: Object.freeze([]),
  }),
  'operations-agent': Object.freeze({
    contracts: Object.freeze(['core']),
    unit: Object.freeze(['operations-agent']),
    integration: Object.freeze(['operations-agent']),
    browser: Object.freeze([]),
  }),
  pocs: Object.freeze({
    contracts: Object.freeze([]),
    unit: Object.freeze(['pocs']),
    integration: Object.freeze([]),
    browser: Object.freeze([]),
  }),
});

const commandIdentity = (command) => `${command.command}\0${command.args.join('\0')}`;

export const deduplicateCommands = (commands) => {
  const deduplicated = [];
  const seen = new Set();
  for (const command of commands) {
    const key = commandIdentity(command);
    if (seen.has(key)) continue;
    seen.add(key);
    deduplicated.push(command);
  }
  return deduplicated;
};

export const resolveGateCommands = (gate, profiles = []) => {
  const gateProfiles = gateCommandMatrix[gate];
  if (!gateProfiles) throw new Error(`Unsupported gate: ${gate ?? '<missing>'}`);
  const selected = gate === 'static'
    ? gateProfiles.default
    : profiles.flatMap((profile) => {
        const commands = gateProfiles[profile];
        if (!commands) throw new Error(`Unsupported ${gate} profile: ${profile}`);
        return commands;
      });
  return deduplicateCommands(selected);
};

export const resolveDomainCommands = (domain, layers = ['unit']) => {
  const profilesByLayer = domainTaskProfiles[domain];
  if (!profilesByLayer) throw new Error(`Unsupported domain: ${domain ?? '<missing>'}`);

  const commands = [];
  const selectedProfiles = {};
  for (const layer of layers) {
    if (!Object.hasOwn(profilesByLayer, layer)) {
      throw new Error(`Unsupported domain layer: ${layer}`);
    }
    const profiles = profilesByLayer[layer];
    selectedProfiles[layer] = [...profiles];
    commands.push(...resolveGateCommands(layer, profiles));
  }

  return {
    schemaVersion: 2,
    domain,
    layers: [...layers],
    profiles: selectedProfiles,
    commands: deduplicateCommands(commands),
  };
};

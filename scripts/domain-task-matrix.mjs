const npmCommand = (args, label) => process.platform === 'win32'
  ? { command: process.env.ComSpec || 'cmd.exe', args: ['/d', '/s', '/c', `npm ${args.join(' ')}`], label }
  : { command: 'npm', args, label };

export const npmRun = (script) => npmCommand(['run', '--silent', script], `npm run ${script}`);
export const npmCi = (prefix) => npmCommand(['--prefix', prefix, 'ci'], `npm --prefix ${prefix} ci`);
export const nodeRun = (...args) => ({ command: process.execPath, args, label: `node ${args.join(' ')}` });

export const gateCommandMatrix = Object.freeze({
  static: Object.freeze({
    default: Object.freeze([
      npmRun('lint'),
      npmRun('rms:topology:check'),
      npmRun('s1:topology:check'),
      npmRun('s2:topology:check'),
      npmRun('s3:topology:check'),
      npmRun('build:demo'),
    ]),
  }),
  contracts: Object.freeze({
    core: Object.freeze([npmRun('contracts:check'), npmRun('ownership:check')]),
    rms: Object.freeze([npmRun('rms:topology:check')]),
    s1: Object.freeze([npmRun('s1:topology:check'), npmRun('s1:registry:check')]),
    s2: Object.freeze([npmRun('s2:topology:check'), npmRun('s2:contracts:check')]),
    s3: Object.freeze([npmRun('s3:topology:check'), npmRun('s3:baseline:check')]),
  }),
  unit: Object.freeze({
    web: Object.freeze([npmRun('rms:trusted-shell:test'), npmRun('web:energy:test')]),
    s0: Object.freeze([npmRun('test:identity'), npmRun('test:durable-unit')]),
    s1: Object.freeze([npmRun('test:registry-routing'), npmRun('test:legacy-migration')]),
    s2: Object.freeze([
      nodeRun(
        'scripts/run-go.mjs',
        'test',
        './libs/telemetryauth/...',
        './services/telemetry-runtime-service/...',
        './services/telemetry-shadow-comparator/...',
        './services/platform-gateway/...',
      ),
    ]),
    s3: Object.freeze([
      nodeRun(
        'scripts/run-go.mjs',
        'test',
        './libs/commandauth/...',
        './libs/commandmodel/...',
        './services/command-service/...',
        './services/command-dispatcher/...',
        './services/thingsboard-connector-control/...',
      ),
    ]),
    analytics: Object.freeze([npmRun('test:analytics'), npmRun('test:analytics-gateway')]),
    'operations-agent': Object.freeze([
      npmCi('services/operations-agent-service'),
      npmRun('operations-agent-service:check'),
      npmRun('operations-agent:benchmark:test'),
      npmRun('operations-agent:gateway:check'),
      npmRun('operations-workspace:test'),
      npmRun('test:gateway'),
    ]),
    pocs: Object.freeze([npmRun('pocs:components:check')]),
  }),
  integration: Object.freeze({
    s0: Object.freeze([npmRun('test:durable-postgres')]),
    s1: Object.freeze([npmRun('s1:registry:postgres')]),
    's2-baseline': Object.freeze([npmRun('s2:postgres')]),
    's2-ingest': Object.freeze([npmRun('s2:ingest:postgres')]),
    's2-realtime': Object.freeze([npmRun('s2:realtime:postgres')]),
    's2-history': Object.freeze([npmRun('s2:history:integration')]),
    s3: Object.freeze([npmRun('s3:postgres')]),
    analytics: Object.freeze([npmRun('analytics:history:integration')]),
    'operations-agent': Object.freeze([
      npmCi('services/operations-agent-service'),
      npmRun('operations-agent-service:postgres'),
    ]),
  }),
  browser: Object.freeze({
    rms: Object.freeze([npmRun('rms:web-browser')]),
    'operations-agent': Object.freeze([npmRun('operations-workspace:browser')]),
    s0: Object.freeze([npmRun('audit:security-failure')]),
    s1: Object.freeze([npmRun('audit:s1-registry-web')]),
    s2: Object.freeze([npmRun('s2:hvac-web:browser')]),
  }),
});

export const gateProfileSets = Object.freeze({
  all: Object.freeze({
    contracts: Object.freeze(['core', 'rms', 's1', 's2', 's3']),
    unit: Object.freeze(['analytics', 'operations-agent', 'pocs', 's0', 's1', 's2', 's3', 'web']),
    integration: Object.freeze([
      'analytics',
      'operations-agent',
      's0',
      's1',
      's2-baseline',
      's2-history',
      's2-ingest',
      's2-realtime',
      's3',
    ]),
    browser: Object.freeze(['operations-agent', 'rms', 's0', 's1', 's2']),
  }),
  'browser-linux': Object.freeze({
    browser: Object.freeze(['operations-agent', 's0', 's1', 's2']),
  }),
  'browser-windows': Object.freeze({
    browser: Object.freeze(['rms']),
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
assertExactProfiles(
  'Browser platform profile sets',
  [
    ...gateProfileSets['browser-linux'].browser,
    ...gateProfileSets['browser-windows'].browser,
  ],
  gateProfileSets.all.browser,
);

export const resolveGateProfileSet = (gate, profileSet) => {
  const profiles = gateProfileSets[profileSet]?.[gate];
  if (!profiles) {
    throw new Error(`Unsupported ${gate} profile set: ${profileSet ?? '<missing>'}`);
  }
  return [...profiles];
};

export const domainTaskProfiles = Object.freeze({
  web: Object.freeze({
    contracts: Object.freeze(['rms']),
    unit: Object.freeze(['web']),
    integration: Object.freeze([]),
    browser: Object.freeze(['rms', 's0', 's1', 's2']),
  }),
  platform: Object.freeze({
    contracts: Object.freeze(['core']),
    unit: Object.freeze(['s0']),
    integration: Object.freeze(['s0']),
    browser: Object.freeze(['s0']),
  }),
  registry: Object.freeze({
    contracts: Object.freeze(['core', 's1']),
    unit: Object.freeze(['s1']),
    integration: Object.freeze(['s1']),
    browser: Object.freeze(['s1']),
  }),
  telemetry: Object.freeze({
    contracts: Object.freeze(['core', 's2']),
    unit: Object.freeze(['s2']),
    integration: Object.freeze(['s2-baseline', 's2-ingest', 's2-realtime', 's2-history']),
    browser: Object.freeze(['s2']),
  }),
  command: Object.freeze({
    contracts: Object.freeze(['core', 's3']),
    unit: Object.freeze(['s3']),
    integration: Object.freeze(['s3']),
    browser: Object.freeze([]),
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
    browser: Object.freeze(['operations-agent']),
  }),
  pocs: Object.freeze({
    contracts: Object.freeze([]),
    unit: Object.freeze(['pocs']),
    integration: Object.freeze([]),
    browser: Object.freeze([]),
  }),
});

export const capabilityTaskMatrix = Object.freeze({
  's2:telemetry-baseline': Object.freeze([
    npmRun('s2:topology:check'),
    npmRun('s2:contracts:check'),
    npmRun('ownership:check'),
    npmRun('s2:baseline:check'),
    npmRun('s2:ownership:check'),
    npmRun('s2:public-contract:check'),
    npmRun('s2:rollout-gates:check'),
    npmRun('s2:implementation-plan:check'),
    npmRun('contracts:check'),
    npmRun('release:evidence-assets'),
    npmRun('s1:registry:check'),
    npmRun('test:ownership'),
    npmRun('test:registry-routing'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './services/telemetry-runtime-service/...',
    ),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:postgres'),
  ]),
  's2:iam-authorization': Object.freeze([
    npmRun('s2:topology:check'),
    npmRun('s2:iam:check'),
    nodeRun('scripts/run-go.mjs', 'test', './libs/telemetryauth/...'),
    nodeRun('scripts/run-go.mjs', 'test', './services/iam-service/...'),
    npmRun('ownership:check'),
    npmRun('s2:baseline:check'),
    npmRun('s1:registry:check'),
    npmRun('release:evidence-assets'),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:iam:postgres'),
  ]),
  's2:telemetry-runtime-snapshot': Object.freeze([
    npmRun('s2:topology:check'),
    npmRun('s2:runtime:check'),
    nodeRun('scripts/run-go.mjs', 'test', './services/telemetry-runtime-service/...'),
    nodeRun('scripts/run-go.mjs', 'vet', './services/telemetry-runtime-service/...'),
    npmRun('build:telemetry-runtime'),
    npmRun('ownership:check'),
    npmRun('s2:baseline:check'),
    npmRun('s2:iam:check'),
    npmRun('s2:contracts:check'),
    npmRun('contracts:check'),
    npmRun('release:evidence-assets'),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:runtime:postgres'),
  ]),
  's2:history': Object.freeze([
    npmRun('s2:history:check'),
    nodeRun('scripts/run-go.mjs', 'test', './services/telemetry-runtime-service/...'),
    nodeRun('scripts/run-go.mjs', 'vet', './services/telemetry-runtime-service/...'),
    npmRun('build:telemetry-history-projector'),
    npmRun('s2:history:integration'),
  ]),
  's2:telemetry-ingest': Object.freeze([
    npmRun('s2:topology:check'),
    npmRun('s2:ingest:check'),
    nodeRun('scripts/run-go.mjs', 'test', './services/telemetry-runtime-service/...'),
    nodeRun('scripts/run-go.mjs', 'vet', './services/telemetry-runtime-service/...'),
    npmRun('build:telemetry-runtime'),
    npmRun('ownership:check'),
    npmRun('s2:baseline:check'),
    npmRun('s2:runtime:check'),
    npmRun('s2:iam:check'),
    npmRun('s2:contracts:check'),
    npmRun('contracts:check'),
    npmRun('release:evidence-assets'),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:ingest:postgres'),
  ]),
  's2:gateway-snapshot': Object.freeze([
    npmRun('s2:topology:check'),
    npmRun('s2:gateway:check'),
    npmRun('s2:contracts:check'),
    npmRun('ownership:check'),
    npmRun('s2:baseline:check'),
    npmRun('s2:ownership:check'),
    npmRun('s2:public-contract:check'),
    npmRun('s2:rollout-gates:check'),
    npmRun('s2:implementation-plan:check'),
    npmRun('s2:iam:check'),
    npmRun('s2:runtime:check'),
    npmRun('contracts:check'),
    npmRun('release:evidence-assets'),
    npmRun('test:gateway'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './libs/telemetryauth/...',
      './services/iam-service/...',
      './services/telemetry-runtime-service/...',
    ),
    nodeRun('scripts/run-go.mjs', 'vet', './services/platform-gateway/...'),
    npmRun('build:gateway'),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:gateway:browser'),
  ]),
  's2:realtime-backend': Object.freeze([
    npmRun('s2:topology:check'),
    npmRun('s2:realtime:check'),
    npmRun('s2:contracts:check'),
    npmRun('ownership:check'),
    npmRun('s2:baseline:check'),
    npmRun('s2:ownership:check'),
    npmRun('s2:public-contract:check'),
    npmRun('s2:rollout-gates:check'),
    npmRun('s2:iam:check'),
    npmRun('s2:runtime:check'),
    npmRun('s2:centrifugo:check'),
    npmRun('contracts:check'),
    npmRun('release:evidence-assets'),
    npmRun('test:gateway'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './libs/telemetryauth/...',
      './services/iam-service/...',
      './services/telemetry-runtime-service/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './services/platform-gateway/...',
      './services/telemetry-runtime-service/...',
    ),
    npmRun('build:gateway'),
    npmRun('build:telemetry-runtime'),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:realtime:postgres'),
    npmRun('s2:realtime:config'),
    npmRun('s2:realtime:transport'),
  ]),
  's3:command-safety': Object.freeze([
    npmRun('s3:baseline:check'),
    npmRun('ownership:check'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './libs/commandmodel/...',
      './services/command-service/...',
      './services/command-dispatcher/...',
      './services/thingsboard-connector-control/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './libs/commandmodel/...',
      './services/command-service/...',
      './services/command-dispatcher/...',
      './services/thingsboard-connector-control/...',
    ),
  ]),
  's3:command-authority': Object.freeze([
    npmRun('s3:postgres:check'),
    npmRun('s3:governance-dispatch:check'),
    npmRun('s3:verification:check'),
    npmRun('ownership:check'),
    npmRun('s3:postgres'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './libs/commandauth/...',
      './libs/commandmodel/...',
      './services/command-service/...',
      './services/command-dispatcher/...',
      './services/thingsboard-connector-control/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './libs/commandauth/...',
      './libs/commandmodel/...',
      './services/command-service/...',
      './services/command-dispatcher/...',
      './services/thingsboard-connector-control/...',
    ),
    npmRun('lint'),
    npmRun('build'),
  ]),
  's3:command-api': Object.freeze([
    npmRun('s3:gateway:check'),
    npmRun('ownership:check'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './libs/commandauth/...',
      './services/iam-service/...',
      './services/command-service/...',
      './services/platform-gateway/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './libs/commandauth/...',
      './services/iam-service/...',
      './services/command-service/...',
      './services/platform-gateway/...',
    ),
  ]),
  's3:thingsboard-contract': Object.freeze([
    npmRun('s3:thingsboard:check'),
    npmRun('ownership:check'),
    nodeRun('scripts/run-go.mjs', 'test', './services/thingsboard-connector-control/...'),
    nodeRun('scripts/run-go.mjs', 'vet', './services/thingsboard-connector-control/...'),
    npmRun('s3:thingsboard:local'),
  ]),
  's3:command-ux': Object.freeze([
    npmRun('s3:command-ux:check'),
    npmRun('s3:gateway:check'),
    npmRun('s3:verification:check'),
    npmRun('ownership:check'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './services/command-service/...',
      './services/platform-gateway/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './services/command-service/...',
      './services/platform-gateway/...',
    ),
    npmRun('lint'),
    npmRun('build'),
  ]),
  's5:work-order:create-assign': Object.freeze([
    npmRun('s5:work-order'),
    npmRun('s5:work-order:read-canary:check'),
    npmRun('s5:work-order:create-assign:check'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      '-count=1',
      './libs/workorderauth/...',
      './libs/workordermodel/...',
      './libs/identitycontext/...',
      './libs/ownershipregistry/...',
      './services/iam-service/...',
      './services/platform-gateway/...',
      './services/work-order-service/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './libs/workorderauth/...',
      './libs/workordermodel/...',
      './libs/identitycontext/...',
      './libs/ownershipregistry/...',
      './services/iam-service/...',
      './services/platform-gateway/...',
      './services/work-order-service/...',
    ),
    nodeRun('scripts/run-s5-work-order-postgres-tests.mjs'),
    npmRun('s5:work-order:create-assign:browser'),
  ]),
});

export const resolveCapabilityTask = (task) => {
  const commands = capabilityTaskMatrix[task];
  if (!commands) throw new Error(`Unsupported capability task: ${task ?? '<missing>'}`);
  return [...commands];
};

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
    schemaVersion: 1,
    domain,
    layers: [...layers],
    profiles: selectedProfiles,
    commands: deduplicateCommands(commands),
  };
};

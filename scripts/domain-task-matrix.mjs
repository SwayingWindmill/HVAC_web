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
      npmRun('build:demo'),
    ]),
  }),
  contracts: Object.freeze({
    core: Object.freeze([npmRun('contracts:check'), npmRun('ownership:check')]),
    rms: Object.freeze([npmRun('rms:topology:check')]),
    s1: Object.freeze([npmRun('s1:topology:check'), npmRun('s1:registry:check')]),
    s2: Object.freeze([npmRun('s2:topology:check'), npmRun('s2:contracts:check'), npmRun('s2:release:check')]),
    s3: Object.freeze([npmRun('s3:baseline:check')]),
  }),
  unit: Object.freeze({
    web: Object.freeze([npmRun('rms:trusted-shell:test'), npmRun('web:energy:test')]),
    s0: Object.freeze([npmRun('test:identity'), npmRun('test:durable-unit')]),
    s1: Object.freeze([npmRun('test:registry-routing')]),
    s2: Object.freeze([
      nodeRun(
        'scripts/run-go.mjs',
        'test',
        './libs/telemetryauth/...',
        './modules/telemetry/...',
        './cmd/energy-api/...',
      ),
    ]),
    s3: Object.freeze([
      nodeRun(
        'scripts/run-go.mjs',
        'test',
        './libs/commandauth/...',
        './libs/commandmodel/...',
        './modules/command/...',
        './services/thingsboard-connector-control/...',
      ),
    ]),
    alarm: Object.freeze([
      nodeRun('scripts/run-go.mjs', 'test', './libs/alarmauth/...', './libs/alarmmodel/...', './modules/alarm/...'),
      npmRun('real-alarms:test'),
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
    alarm: Object.freeze([npmRun('s4:alarm:postgres')]),
    workorder: Object.freeze([nodeRun('--experimental-strip-types', 'scripts/run-s5-work-order-postgres-tests.ts')]),
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
    s2: Object.freeze([npmRun('s2:live-client:browser'), npmRun('s2:hvac-web:browser')]),
    alarm: Object.freeze([npmRun('real-alarms:browser'), npmRun('real-alarms:lifecycle-browser')]),
    workorder: Object.freeze([
      npmRun('s5:work-order:read-canary:browser'),
      npmRun('s5:work-order:create-assign:browser'),
      npmRun('s5:work-order:lifecycle:browser'),
    ]),
  }),
});

export const gateProfileSets = Object.freeze({
  all: Object.freeze({
    contracts: Object.freeze(['core', 'rms', 's1', 's2', 's3']),
    unit: Object.freeze(['alarm', 'analytics', 'operations-agent', 'pocs', 's0', 's1', 's2', 's3', 'web', 'workorder']),
    integration: Object.freeze([
      'alarm',
      'analytics',
      'operations-agent',
      's0',
      's1',
      's2-baseline',
      's2-history',
      's2-ingest',
      's2-realtime',
      's3',
      'workorder',
    ]),
    browser: Object.freeze(['alarm', 'operations-agent', 'rms', 's0', 's1', 's2', 'workorder']),
  }),
  'browser-linux': Object.freeze({
    browser: Object.freeze(['alarm', 'operations-agent', 's0', 's1', 's2', 'workorder']),
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
  alarm: Object.freeze({
    contracts: Object.freeze(['core']),
    unit: Object.freeze(['alarm']),
    integration: Object.freeze(['alarm']),
    browser: Object.freeze(['alarm']),
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
      './modules/telemetry/...',
    ),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:postgres'),
  ]),
  's2:iam-authorization': Object.freeze([
    npmRun('s2:topology:check'),
    npmRun('s2:iam:check'),
    nodeRun('scripts/run-go.mjs', 'test', './libs/telemetryauth/...'),
    nodeRun('scripts/run-go.mjs', 'test', './modules/iam/...'),
    npmRun('ownership:check'),
    npmRun('s2:baseline:check'),
    npmRun('s1:registry:check'),
    npmRun('release:evidence-assets'),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:iam:postgres'),
  ]),
  's2:history': Object.freeze([
    npmRun('s2:history:check'),
    nodeRun('scripts/run-go.mjs', 'test', './modules/telemetry/...'),
    nodeRun('scripts/run-go.mjs', 'vet', './modules/telemetry/...'),
    npmRun('build:telemetry-history-projector'),
    npmRun('s2:history:integration'),
  ]),
  's2:telemetry-ingest': Object.freeze([
    npmRun('s2:topology:check'),
    npmRun('s2:ingest:check'),
    nodeRun('scripts/run-go.mjs', 'test', './modules/telemetry/...'),
    nodeRun('scripts/run-go.mjs', 'vet', './modules/telemetry/...'),
    npmRun('build:telemetry-worker'),
    npmRun('ownership:check'),
    npmRun('s2:baseline:check'),
    npmRun('s2:iam:check'),
    npmRun('s2:contracts:check'),
    npmRun('contracts:check'),
    npmRun('release:evidence-assets'),
    npmRun('lint'),
    npmRun('build'),
    npmRun('s2:ingest:postgres'),
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
    npmRun('s2:centrifugo:check'),
    npmRun('contracts:check'),
    npmRun('release:evidence-assets'),
    npmRun('test:gateway'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './libs/telemetryauth/...',
      './modules/iam/...',
      './modules/telemetry/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './cmd/energy-api/...',
      './modules/telemetry/...',
    ),
    npmRun('build:energy-api'),
    npmRun('build:telemetry-worker'),
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
      './modules/command/...',
      './services/thingsboard-connector-control/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './libs/commandmodel/...',
      './modules/command/...',
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
      './modules/command/...',
      './services/thingsboard-connector-control/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './libs/commandauth/...',
      './libs/commandmodel/...',
      './modules/command/...',
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
      './modules/iam/...',
      './modules/command/...',
      './cmd/energy-api/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './libs/commandauth/...',
      './modules/iam/...',
      './modules/command/...',
      './cmd/energy-api/...',
    ),
  ]),
  's3:command-ux': Object.freeze([
    npmRun('s3:command-ux:check'),
    npmRun('s3:gateway:check'),
    npmRun('s3:verification:check'),
    npmRun('ownership:check'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      './modules/command/...',
      './cmd/energy-api/...',
    ),
    nodeRun(
      'scripts/run-go.mjs',
      'vet',
      './modules/command/...',
      './cmd/energy-api/...',
    ),
    npmRun('lint'),
    npmRun('build'),
  ]),
  's5:work-order:create-assign': Object.freeze([
    npmRun('s5:work-order:create-assign:check'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      '-count=1',
      './libs/workorderauth/...',
      './libs/workordermodel/...',
      './libs/identitycontext/...',
      './libs/ownershipregistry/...',
      './modules/iam/...',
      './cmd/energy-api/...',
      './modules/workorder/...',
    ),
    nodeRun('--experimental-strip-types', 'scripts/run-s5-work-order-postgres-tests.ts'),
    npmRun('s5:work-order:create-assign:browser'),
  ]),
  's5:work-order:lifecycle': Object.freeze([
    npmRun('s5:work-order:lifecycle:check'),
    nodeRun(
      'scripts/run-go.mjs',
      'test',
      '-count=1',
      './libs/workorderauth/...',
      './libs/workordermodel/...',
      './libs/identitycontext/...',
      './libs/ownershipregistry/...',
      './modules/iam/...',
      './cmd/energy-api/...',
      './modules/workorder/...',
    ),
    nodeRun('--experimental-strip-types', 'scripts/run-s5-work-order-postgres-tests.ts'),
    npmRun('s5:work-order:lifecycle:browser'),
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
